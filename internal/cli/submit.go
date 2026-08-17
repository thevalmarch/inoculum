package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/inoculum/internal/appconfig"
	"github.com/inoculum/internal/crypto"
	"github.com/inoculum/internal/monitor"
	"github.com/inoculum/internal/presentation"
	"github.com/inoculum/internal/presentation/plain"
	"github.com/inoculum/internal/presentation/tui"
	"github.com/inoculum/internal/types"
	"github.com/inoculum/internal/workload"
)

const manifestPlainProgressInterval = 2 * time.Second

type submitOptions struct {
	coordinator  string
	token        string
	fingerprint  string
	taskType     string
	input        string
	taskCount    int
	timeout      time.Duration
	manifestPath string
	outputPath   string
	manifest     *workload.Manifest
	presentation presentationFlags
}

type client struct {
	address string
	token   string
	http    *http.Client
}

type executionResult struct {
	job monitor.JobProgress
	err error
}

type waitTimeoutError struct {
	jobID   string
	timeout time.Duration
	cause   error
}

type jobFailedError struct{ jobID string }

func (e *jobFailedError) Error() string { return fmt.Sprintf("job %s failed", e.jobID) }

func (e *waitTimeoutError) Error() string {
	return fmt.Sprintf("client stopped waiting after %s for job %s: %v; the coordinator job was not marked failed and may still be running", e.timeout, e.jobID, e.cause)
}

func (e *waitTimeoutError) Unwrap() error { return e.cause }

// RunSubmit owns submit parsing, client waiting, and presentation. Job and
// task semantics remain in the coordinator and monitor packages.
func RunSubmit(args []string, streams Streams) error {
	options, err := parseSubmitOptions(args, streams)
	if err != nil {
		return err
	}
	if options.manifestPath != "" {
		manifest, manifestErr := workload.LoadManifest(options.manifestPath)
		if manifestErr != nil {
			return usageErrorf("%v", manifestErr)
		}
		options.manifest = &manifest
		options.taskType = manifest.Type
		options.taskCount = len(manifest.Tasks)
		if options.outputPath == "" {
			fmt.Fprintln(streams.Stderr, "Manifest mode will not save detailed results without --output; using --output is strongly recommended.")
		}
	} else if options.taskCount <= 0 {
		return usageErrorf("--tasks must be positive")
	}
	if options.outputPath != "" {
		if err := validateResultOutputPath(options.outputPath); err != nil {
			return usageErrorf("invalid --output path: %v", err)
		}
	}
	token, err := resolveToken(options.token)
	if err != nil {
		return err
	}
	err = runSubmit(options, token, streams)
	var timeoutErr *waitTimeoutError
	var failedErr *jobFailedError
	if errors.As(err, &timeoutErr) || errors.As(err, &failedErr) {
		return reportedRuntimeError(err)
	}
	return runtimeError(err, options.presentation.verbose)
}

func parseSubmitOptions(args []string, streams Streams) (submitOptions, error) {
	var options submitOptions
	set := newFlagSet("submit", "Submit independent tasks and wait for completion.", "inoculum submit [flags]", streams)
	set.StringVar(&options.coordinator, "coordinator", "localhost:8080", "Coordinator address (host:port)")
	set.StringVar(&options.token, "token", "", "Shared bearer token; prefer INOCULUM_TOKEN")
	set.StringVar(&options.fingerprint, "coordinator-fingerprint", "", "Coordinator fingerprint required on first trust or intentional identity replacement")
	set.StringVar(&options.taskType, "type", "diagnostic_sleep", "Task type")
	set.StringVar(&options.input, "input", "1s", "Input repeated for each task")
	set.IntVar(&options.taskCount, "tasks", 1, "Number of independent tasks")
	set.DurationVar(&options.timeout, "timeout", 30*time.Minute, "Maximum client wait; the coordinator job continues after timeout")
	set.StringVar(&options.manifestPath, "manifest", "", "Versioned JSON workload manifest")
	set.StringVar(&options.outputPath, "output", "", "Final JSON results path; strongly recommended with --manifest")
	addPresentationFlags(set, &options.presentation, "inoculum-submit.log")
	if err := parseFlagSet(set, args, streams); err != nil {
		return submitOptions{}, err
	}
	visited := make(map[string]bool)
	set.Visit(func(flag *flag.Flag) { visited[flag.Name] = true })
	if visited["manifest"] && options.manifestPath == "" {
		return submitOptions{}, usageErrorf("--manifest requires a path")
	}
	if options.manifestPath != "" {
		for _, simpleFlag := range []string{"type", "input", "tasks"} {
			if visited[simpleFlag] {
				return submitOptions{}, usageErrorf("--manifest cannot be combined with --%s", simpleFlag)
			}
		}
	}
	if visited["output"] && options.outputPath == "" {
		return submitOptions{}, usageErrorf("--output requires a path")
	}
	if options.outputPath != "" && options.manifestPath == "" {
		return submitOptions{}, usageErrorf("--output requires --manifest")
	}
	return options, nil
}

func runSubmit(options submitOptions, token string, streams Streams) error {
	caps, operationalLog := configurePresentation("submit", options.presentation, streams)
	if operationalLog != nil {
		defer operationalLog.Close()
	}

	paths, err := appconfig.DefaultPaths()
	if err != nil {
		return err
	}
	tlsConfig, err := crypto.NewCoordinatorClientConfig(paths.TrustedCoordinator, options.fingerprint)
	if err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	c := &client{
		address: options.coordinator,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}

	request := types.PullSubmitRequest{TaskType: options.taskType}
	if options.manifest != nil {
		request.Tasks = make([]types.PullSubmitTask, len(options.manifest.Tasks))
		for index, task := range options.manifest.Tasks {
			request.Tasks[index] = types.PullSubmitTask{Key: task.Key, Input: task.Input}
		}
	} else {
		request.Inputs = make([]string, options.taskCount)
		for index := range request.Inputs {
			request.Inputs[index] = options.input
		}
	}
	tracker := monitor.NewSubmitTracker(options.taskCount)

	signalContext, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(signalContext, options.timeout)
	defer cancel()

	if !caps.Interactive {
		var lastManifestProgress time.Time
		result := execute(ctx, c, request, options.timeout, tracker, func(job monitor.JobProgress) {
			if options.manifest != nil && job.Completed+job.Failed != job.Total && !lastManifestProgress.IsZero() && time.Since(lastManifestProgress) < manifestPlainProgressInterval {
				return
			}
			plain.SubmitProgress(streams.Stdout, job)
			lastManifestProgress = time.Now()
		})
		return finishSubmit(result, caps, streams, options.outputPath, options.manifest != nil)
	}

	done := make(chan struct{})
	resultChannel := make(chan executionResult, 1)
	go func() {
		defer close(done)
		resultChannel <- execute(ctx, c, request, options.timeout, tracker, nil)
	}()

	tuiErr := tui.Run(ctx, done, caps, func(width, height int) presentation.Frame {
		return presentation.SubmitFrame(tracker.Snapshot(time.Now()), width, height, caps)
	})
	if errors.Is(tuiErr, tui.ErrQuit) {
		cancel()
	}
	if tuiErr != nil && !errors.Is(tuiErr, tui.ErrQuit) {
		fmt.Fprintf(streams.Stderr, "interactive terminal unavailable: %v; continuing in plain mode\n", tuiErr)
		watchPlainSubmit(done, tracker, streams.Stdout)
	}

	result := <-resultChannel
	if errors.Is(tuiErr, tui.ErrQuit) && result.err == nil {
		result.err = clientWaitTimeout(result.job.JobID, options.timeout, context.Canceled)
	}
	return finishSubmit(result, caps, streams, options.outputPath, options.manifest != nil)
}

func execute(ctx context.Context, c *client, request types.PullSubmitRequest, timeout time.Duration, tracker *monitor.SubmitTracker, progress func(monitor.JobProgress)) executionResult {
	job, err := c.submit(ctx, request)
	if err != nil {
		tracker.Finish(err, errors.Is(ctx.Err(), context.DeadlineExceeded))
		return executionResult{err: err}
	}
	startedAt := time.Now()
	tracker.Submitted(job.JobID, len(job.TaskIDs), startedAt)

	var lastProgress string
	for {
		response, requestErr := c.job(ctx, job.JobID)
		if requestErr != nil {
			if ctx.Err() != nil {
				err = clientWaitTimeout(job.JobID, waitedDuration(startedAt, timeout, ctx.Err()), ctx.Err())
			} else {
				err = requestErr
			}
			tracker.Finish(err, errors.Is(ctx.Err(), context.DeadlineExceeded))
			return executionResult{job: tracker.Snapshot(time.Now()).Job, err: err}
		}
		progressJob := tracker.Update(response, time.Now())
		key := progressKey(progressJob)
		if progress != nil && key != lastProgress {
			progress(progressJob)
			lastProgress = key
		}
		if response.State == types.PullJobCompleted || response.State == types.PullJobFailed {
			if response.State == types.PullJobFailed {
				err = &jobFailedError{jobID: response.JobID}
			}
			tracker.Finish(err, false)
			return executionResult{job: progressJob, err: err}
		}
		select {
		case <-ctx.Done():
			err = clientWaitTimeout(job.JobID, waitedDuration(startedAt, timeout, ctx.Err()), ctx.Err())
			tracker.Finish(err, errors.Is(ctx.Err(), context.DeadlineExceeded))
			return executionResult{job: tracker.Snapshot(time.Now()).Job, err: err}
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func finishSubmit(result executionResult, caps presentation.Capabilities, streams Streams, outputPath string, manifestMode bool) error {
	terminal := result.job.JobID != "" && result.job.Completed+result.job.Failed == result.job.Total
	if terminal {
		if manifestMode {
			plain.ManifestSubmitSummary(streams.Stdout, result.job, caps.Unicode)
		} else {
			plain.SubmitSummary(streams.Stdout, result.job, caps.Verbose, caps.Unicode)
		}
	}
	if terminal && outputPath != "" {
		if err := writeManifestResults(outputPath, result.job); err != nil {
			return fmt.Errorf("write manifest results: %w", err)
		}
		fmt.Fprintf(streams.Stdout, "\nResults written to %s\n", outputPath)
	}
	var timeoutErr *waitTimeoutError
	if errors.As(result.err, &timeoutErr) {
		plain.StoppedWaiting(streams.Stdout, timeoutErr.jobID, timeoutErr.timeout.String())
	}
	return result.err
}

func watchPlainSubmit(done <-chan struct{}, tracker *monitor.SubmitTracker, writer io.Writer) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			job := tracker.Snapshot(time.Now()).Job
			key := progressKey(job)
			if job.JobID != "" && key != last {
				plain.SubmitProgress(writer, job)
				last = key
			}
		}
	}
}

func progressKey(job monitor.JobProgress) string {
	return fmt.Sprintf("%d/%d/%d/%d", job.Queued, job.Running, job.Completed, job.Failed)
}

func waitedDuration(startedAt time.Time, configured time.Duration, cause error) time.Duration {
	if errors.Is(cause, context.Canceled) && !startedAt.IsZero() {
		return time.Since(startedAt).Round(100 * time.Millisecond)
	}
	return configured
}

func jobProgress(job types.PullJobResponse) string {
	progress := monitor.JobFromResponse(job, time.Time{}, time.Now())
	return fmt.Sprintf("queued=%d leased=%d completed=%d failed=%d", progress.Queued, progress.Running, progress.Completed, progress.Failed)
}

func clientWaitTimeout(jobID string, timeout time.Duration, cause error) error {
	return &waitTimeoutError{jobID: jobID, timeout: timeout, cause: cause}
}

func (c *client) submit(ctx context.Context, request types.PullSubmitRequest) (types.PullSubmitResponse, error) {
	var response types.PullSubmitResponse
	status, body, err := c.do(ctx, http.MethodPost, "/pull/submit", request)
	if err != nil {
		return response, err
	}
	if status != http.StatusOK {
		return response, fmt.Errorf("coordinator returned HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("decode submit response: %w", err)
	}
	return response, nil
}

func (c *client) job(ctx context.Context, jobID string) (types.PullJobResponse, error) {
	var response types.PullJobResponse
	status, body, err := c.do(ctx, http.MethodGet, "/pull/job?id="+url.QueryEscape(jobID), nil)
	if err != nil {
		return response, err
	}
	if status != http.StatusOK {
		return response, fmt.Errorf("coordinator returned HTTP %d: %s", status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return response, fmt.Errorf("decode job response: %w", err)
	}
	return response, nil
}

func (c *client) do(ctx context.Context, method, path string, value any) (int, []byte, error) {
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("https://%s%s", c.address, path), body)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, err
	}
	return response.StatusCode, responseBody, nil
}
