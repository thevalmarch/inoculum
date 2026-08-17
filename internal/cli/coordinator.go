package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/inoculum/internal/appconfig"
	"github.com/inoculum/internal/audit"
	"github.com/inoculum/internal/coordinator"
	"github.com/inoculum/internal/crypto"
	"github.com/inoculum/internal/presentation"
	"github.com/inoculum/internal/presentation/plain"
	"github.com/inoculum/internal/presentation/tui"
)

type coordinatorOptions struct {
	port          int
	token         string
	auditLog      string
	leaseDuration time.Duration
	maxAttempts   int
	presentation  presentationFlags
}

// RunCoordinator owns coordinator parsing, initialization, presentation, and
// shutdown. The coordinator runtime itself remains in internal/coordinator.
func RunCoordinator(args []string, streams Streams) error {
	options, err := parseCoordinatorOptions(args, streams)
	if err != nil {
		return err
	}
	if options.port < 1 || options.port > 65535 {
		return usageErrorf("--port must be between 1 and 65535")
	}
	if options.leaseDuration <= 0 {
		return usageErrorf("--lease-duration must be greater than 0")
	}
	if options.maxAttempts < 1 {
		return usageErrorf("--max-attempts must be at least 1")
	}
	token, err := resolveToken(options.token)
	if err != nil {
		return err
	}
	return runtimeError(runCoordinator(options, token, streams), options.presentation.verbose)
}

func parseCoordinatorOptions(args []string, streams Streams) (coordinatorOptions, error) {
	var options coordinatorOptions
	set := newFlagSet("coordinator", "Start the Inoculum coordinator.", "inoculum coordinator [flags]", streams)
	set.IntVar(&options.port, "port", 8080, "HTTPS listen port")
	set.DurationVar(&options.leaseDuration, "lease-duration", coordinator.DefaultLeaseDuration, "Time a worker owns a claimed task before the lease expires unless renewed")
	set.IntVar(&options.maxAttempts, "max-attempts", coordinator.DefaultMaxAttempts, "Maximum execution attempts before a task is permanently failed")
	set.StringVar(&options.token, "token", "", "Shared bearer token; prefer INOCULUM_TOKEN")
	set.StringVar(&options.auditLog, "audit-log", "", "Optional path for sanitized JSON security events")
	addPresentationFlags(set, &options.presentation, "inoculum-coordinator.log")
	if err := parseFlagSet(set, args, streams); err != nil {
		return coordinatorOptions{}, err
	}
	return options, nil
}

func runCoordinator(options coordinatorOptions, token string, streams Streams) error {
	caps, operationalLog := configurePresentation("coordinator", options.presentation, streams)
	if operationalLog != nil {
		defer operationalLog.Close()
	}

	if err := audit.InitLogger(options.auditLog); err != nil {
		return fmt.Errorf("open requested audit log %s: %w", options.auditLog, err)
	}
	defer audit.Close()
	if options.auditLog != "" {
		log.Printf("[coordinator] Security audit logging enabled at %s", options.auditLog)
	}

	paths, err := appconfig.DefaultPaths()
	if err != nil {
		return err
	}
	identity, err := crypto.LoadOrCreateCoordinatorIdentity(crypto.IdentityOptions{
		CertFile:       paths.CoordinatorCert,
		KeyFile:        paths.CoordinatorKey,
		LegacyCertFile: ".inoculum-coordinator-cert.pem",
		LegacyKeyFile:  ".inoculum-coordinator-key.pem",
	})
	if err != nil {
		return fmt.Errorf("coordinator identity could not be loaded safely from %s: %w", paths.Dir, err)
	}
	if identity.Migrated {
		log.Printf("[coordinator] Migrated existing coordinator identity to %s", paths.Dir)
		fmt.Fprintf(streams.Stderr, "Existing coordinator identity migrated to %s.\n", paths.Dir)
	} else if identity.Created {
		log.Printf("[coordinator] Created a new coordinator identity in %s", paths.Dir)
		fmt.Fprintf(streams.Stderr, "New coordinator identity created in %s. Clients must explicitly trust the fingerprint shown below.\n", paths.Dir)
	}
	log.Printf("[coordinator] Coordinator fingerprint: %s", identity.Fingerprint)

	server, err := coordinator.NewServer(coordinator.Config{
		Port:          options.port,
		Token:         token,
		Certificate:   identity.Certificate,
		LeaseDuration: options.leaseDuration,
		MaxAttempts:   options.maxAttempts,
	})
	if err != nil {
		return err
	}
	addresses := presentation.ListenAddresses(options.port)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	serverDone := make(chan struct{})
	serverErrors := make(chan error, 1)
	go func() {
		defer close(serverDone)
		serverErrors <- server.Start()
	}()

	if !caps.Interactive {
		plain.CoordinatorStarted(streams.Stdout, server.MonitorSnapshot(addresses, identity.Fingerprint), caps.Verbose)
		select {
		case <-ctx.Done():
			log.Printf("[coordinator] Shutting down...")
			return nil
		case err := <-serverErrors:
			return err
		}
	}

	err = tui.Run(ctx, serverDone, caps, func(width, height int) presentation.Frame {
		return presentation.CoordinatorFrame(server.MonitorSnapshot(addresses, identity.Fingerprint), width, height, caps)
	})
	if err != nil && !errors.Is(err, tui.ErrQuit) {
		log.SetOutput(streams.Stderr)
		fmt.Fprintf(streams.Stderr, "interactive terminal unavailable: %v; continuing in plain mode\n", err)
		plain.CoordinatorStarted(streams.Stdout, server.MonitorSnapshot(addresses, identity.Fingerprint), caps.Verbose)
		select {
		case <-ctx.Done():
			return nil
		case serverErr := <-serverErrors:
			return serverErr
		}
	}
	if errors.Is(err, tui.ErrQuit) {
		cancel()
	}
	select {
	case serverErr := <-serverErrors:
		return serverErr
	default:
		return nil
	}
}
