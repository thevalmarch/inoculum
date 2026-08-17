package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/inoculum/internal/presentation"
	"github.com/inoculum/internal/presentation/plain"
	"github.com/inoculum/internal/presentation/tui"
	"github.com/inoculum/internal/worker"
)

type workerOptions struct {
	coordinator  string
	workerID     string
	concurrency  int
	allowed      string
	token        string
	fingerprint  string
	presentation presentationFlags
}

// RunWorker owns worker parsing, presentation, and shutdown. Work claiming,
// execution, renewal, and reconnect behavior remain in internal/worker.
func RunWorker(args []string, streams Streams) error {
	options, err := parseWorkerOptions(args, streams)
	if err != nil {
		return err
	}
	if options.coordinator == "" {
		return usageErrorf("--coordinator is required")
	}
	if options.concurrency <= 0 {
		return usageErrorf("--concurrency must be positive")
	}
	token, err := resolveToken(options.token)
	if err != nil {
		return err
	}
	return runtimeError(runWorker(options, token, streams), options.presentation.verbose)
}

func parseWorkerOptions(args []string, streams Streams) (workerOptions, error) {
	var options workerOptions
	set := newFlagSet("worker", "Connect an outbound worker to an Inoculum coordinator.", "inoculum worker --coordinator <host:port> [flags]", streams)
	set.StringVar(&options.coordinator, "coordinator", "", "Coordinator address (host:port)")
	set.StringVar(&options.workerID, "id", "", "Worker ID; defaults from hostname")
	set.IntVar(&options.concurrency, "concurrency", 1, "Number of independent execution loops")
	set.StringVar(&options.allowed, "allowed-paths", ".", "Comma-separated directories allowed for file_analyze")
	set.StringVar(&options.token, "token", "", "Shared bearer token; prefer INOCULUM_TOKEN")
	set.StringVar(&options.fingerprint, "coordinator-fingerprint", "", "Coordinator fingerprint required on first trust or intentional identity replacement")
	addPresentationFlags(set, &options.presentation, "inoculum-worker.log")
	if err := parseFlagSet(set, args, streams); err != nil {
		return workerOptions{}, err
	}
	return options, nil
}

func runWorker(options workerOptions, token string, streams Streams) error {
	caps, operationalLog := configurePresentation("worker", options.presentation, streams)
	if operationalLog != nil {
		defer operationalLog.Close()
	}

	workerID := options.workerID
	if workerID == "" {
		hostname, _ := os.Hostname()
		workerID = fmt.Sprintf("worker-%s", hostname)
	}

	var allowedPaths []string
	for _, path := range strings.Split(options.allowed, ",") {
		if path = strings.TrimSpace(path); path != "" {
			allowedPaths = append(allowedPaths, path)
		}
	}

	pullWorker, err := worker.NewPullWorker(worker.PullConfig{
		CoordinatorAddr: options.coordinator,
		WorkerID:        workerID,
		Token:           token,
		Fingerprint:     options.fingerprint,
		Concurrency:     options.concurrency,
		AllowedPaths:    allowedPaths,
	})
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if !caps.Interactive {
		plain.WorkerStarted(streams.Stdout, pullWorker.MonitorSnapshot())
		if err := pullWorker.Run(ctx); err != nil {
			return err
		}
		log.Printf("[pull-worker] Shutting down...")
		return nil
	}

	workerDone := make(chan struct{})
	workerErrors := make(chan error, 1)
	go func() {
		defer close(workerDone)
		workerErrors <- pullWorker.Run(ctx)
	}()
	err = tui.Run(ctx, workerDone, caps, func(width, height int) presentation.Frame {
		return presentation.WorkerFrame(pullWorker.MonitorSnapshot(), width, height, caps)
	})
	if err != nil && !errors.Is(err, tui.ErrQuit) {
		log.SetOutput(streams.Stderr)
		fmt.Fprintf(streams.Stderr, "interactive terminal unavailable: %v; continuing in plain mode\n", err)
		plain.WorkerStarted(streams.Stdout, pullWorker.MonitorSnapshot())
		select {
		case <-ctx.Done():
		case workerErr := <-workerErrors:
			return workerErr
		}
	} else if errors.Is(err, tui.ErrQuit) {
		cancel()
	}

	snapshot := pullWorker.MonitorSnapshot()
	if len(snapshot.ActiveTasks) > 0 {
		fmt.Fprintf(streams.Stderr, "Stopping worker. %d active task(s) may continue until the executor returns.\n", len(snapshot.ActiveTasks))
	}
	select {
	case workerErr := <-workerErrors:
		return workerErr
	case <-workerDone:
		return nil
	}
}
