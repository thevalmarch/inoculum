package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/inoculum/internal/appconfig"
	"github.com/inoculum/internal/crypto"
	"github.com/inoculum/internal/monitor"
	"github.com/inoculum/internal/types"
)

const (
	defaultClaimInterval = 500 * time.Millisecond
	defaultMinBackoff    = 500 * time.Millisecond
	defaultMaxBackoff    = 8 * time.Second
)

type PullConfig struct {
	CoordinatorAddr string
	WorkerID        string
	Token           string
	Fingerprint     string
	TrustFile       string
	Concurrency     int
	AllowedPaths    []string
}

// PullWorker requests work only when it has local execution capacity. It has
// no listening port and does not advertise a network address.
type PullWorker struct {
	config   PullConfig
	executor *Executor
	client   *http.Client
	state    connectionState
	monitor  *monitor.WorkerTracker
}

type connectionState struct {
	mu            sync.Mutex
	everConnected bool
	unavailable   bool
}

func NewPullWorker(config PullConfig) (*PullWorker, error) {
	if config.CoordinatorAddr == "" {
		return nil, fmt.Errorf("coordinator address is required in pull mode")
	}
	if config.WorkerID == "" {
		return nil, fmt.Errorf("worker ID is required")
	}
	if config.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if config.Concurrency <= 0 {
		return nil, fmt.Errorf("concurrency must be positive")
	}

	trustFile := config.TrustFile
	if trustFile == "" {
		paths, err := appconfig.DefaultPaths()
		if err != nil {
			return nil, err
		}
		trustFile = paths.TrustedCoordinator
	}
	tlsConfig, err := crypto.NewCoordinatorClientConfig(trustFile, config.Fingerprint)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	transport.MaxIdleConnsPerHost = config.Concurrency + 2

	return &PullWorker{
		config:   config,
		executor: NewExecutor(config.AllowedPaths),
		monitor:  monitor.NewWorkerTracker(config.WorkerID, config.CoordinatorAddr, config.Concurrency),
		client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		},
	}, nil
}

func (w *PullWorker) Run(ctx context.Context) error {
	log.Printf("[pull-worker] Starting %d outbound execution loop(s) for coordinator %s", w.config.Concurrency, w.config.CoordinatorAddr)
	var wg sync.WaitGroup
	for slot := 0; slot < w.config.Concurrency; slot++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			w.runSlot(ctx, slot)
		}(slot)
	}
	<-ctx.Done()
	w.monitor.Stopping()
	wg.Wait()
	return nil
}

// MonitorSnapshot returns immutable local observability state. Execution and
// delivery correctness do not depend on consumers calling this method.
func (w *PullWorker) MonitorSnapshot() monitor.WorkerSnapshot {
	return w.monitor.Snapshot(time.Now())
}

func (w *PullWorker) runSlot(ctx context.Context, slot int) {
	backoff := defaultMinBackoff
	retryAttempt := 0
	for ctx.Err() == nil {
		claim, status, err := w.claim(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			retryAttempt++
			w.markUnavailable(err, retryAttempt, backoff)
			if !waitContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		w.markConnected()
		backoff = defaultMinBackoff
		retryAttempt = 0
		if status == types.PullNoTask || claim == nil {
			if !waitContext(ctx, defaultClaimInterval) {
				return
			}
			continue
		}
		if status != types.PullTaskAvailable {
			log.Printf("[pull-worker] Claim rejected with status %s", status)
			if !waitContext(ctx, defaultClaimInterval) {
				return
			}
			continue
		}

		log.Printf("[pull-worker] Slot %d claimed task %s (attempt %d)", slot, claim.TaskID, claim.Attempt)
		w.monitor.TaskStarted(monitor.TaskProgress{
			TaskID: claim.TaskID, State: "running", WorkerID: w.config.WorkerID,
			Attempt: claim.Attempt, StartedAt: time.Now(),
		})
		w.monitor.Record(monitor.SystemEvent{
			Severity: monitor.SeverityInfo, Component: "worker", Kind: "task_started", Message: "Task started",
			Fields: map[string]string{"task_id": claim.TaskID, "attempt": fmt.Sprint(claim.Attempt)},
		})
		w.executeClaim(ctx, claim)
	}
}

func (w *PullWorker) executeClaim(ctx context.Context, task *types.PullTask) {
	renewCtx, cancelRenew := context.WithCancel(ctx)
	var renewWG sync.WaitGroup
	renewWG.Add(1)
	go func() {
		defer renewWG.Done()
		w.renewLoop(renewCtx, task)
	}()

	output, duration, execErr := w.executor.Execute(task.Type, task.Input)
	result := types.Result{
		TaskID:      task.TaskID,
		Output:      output,
		Duration:    duration,
		DurationStr: duration.String(),
	}
	if execErr != nil {
		result.Error = execErr.Error()
	}

	status := w.submitResultUntilResolved(ctx, task, result)
	cancelRenew()
	renewWG.Wait()
	w.monitor.TaskFinished(task.TaskID, status == types.PullTaskCompleted, status == types.PullTaskFailed)
}

func (w *PullWorker) renewLoop(ctx context.Context, task *types.PullTask) {
	leaseLength := time.Until(task.LeaseExpiresAt)
	interval := leaseLength / 3
	if interval < 500*time.Millisecond {
		interval = 500 * time.Millisecond
	}
	if interval > 5*time.Second {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err := w.renew(ctx, task)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				w.markUnavailable(err, 1, interval)
				continue
			}
			w.markConnected()
			if status != types.PullLeaseRenewed {
				log.Printf("[pull-worker] Lease for task %s is no longer renewable: %s", task.TaskID, status)
				return
			}
		}
	}
}

func (w *PullWorker) submitResultUntilResolved(ctx context.Context, task *types.PullTask, result types.Result) types.PullStatus {
	backoff := defaultMinBackoff
	retryAttempt := 0
	for ctx.Err() == nil {
		status, err := w.submitResult(ctx, task, result)
		if err != nil {
			if ctx.Err() != nil {
				return ""
			}
			retryAttempt++
			w.markUnavailable(err, retryAttempt, backoff)
			if !waitContext(ctx, backoff) {
				return ""
			}
			backoff = nextBackoff(backoff)
			continue
		}

		w.markConnected()
		switch status {
		case types.PullTaskCompleted:
			log.Printf("[pull-worker] Task %s result accepted", task.TaskID)
			w.monitor.Record(monitor.SystemEvent{Severity: monitor.SeverityInfo, Component: "worker", Kind: "task_completed", Message: "Task completed", Fields: map[string]string{"task_id": task.TaskID}})
		case types.PullTaskRequeued:
			log.Printf("[pull-worker] Task %s execution failed; coordinator requeued it", task.TaskID)
			w.monitor.Record(monitor.SystemEvent{Severity: monitor.SeverityWarning, Component: "worker", Kind: "task_requeued", Message: "Task failed and was requeued", Fields: map[string]string{"task_id": task.TaskID}})
		case types.PullTaskFailed:
			log.Printf("[pull-worker] Task %s failed permanently", task.TaskID)
			w.monitor.Record(monitor.SystemEvent{Severity: monitor.SeverityError, Component: "worker", Kind: "task_failed", Message: "Task failed permanently", Fields: map[string]string{"task_id": task.TaskID}})
		case types.PullStaleLease:
			log.Printf("[pull-worker] Task %s result rejected because its lease is stale", task.TaskID)
		default:
			log.Printf("[pull-worker] Task %s result rejected with status %s", task.TaskID, status)
		}
		return status
	}
	return ""
}

func (w *PullWorker) claim(ctx context.Context) (*types.PullTask, types.PullStatus, error) {
	var response types.PullClaimResponse
	statusCode, err := w.post(ctx, "/worker/claim", types.PullClaimRequest{WorkerID: w.config.WorkerID}, &response)
	if err != nil {
		return nil, "", err
	}
	if statusCode != http.StatusOK {
		return nil, response.Status, fmt.Errorf("claim returned HTTP %d (%s)", statusCode, response.Status)
	}
	return response.Task, response.Status, nil
}

func (w *PullWorker) renew(ctx context.Context, task *types.PullTask) (types.PullStatus, error) {
	var response types.PullRenewResponse
	statusCode, err := w.post(ctx, "/worker/renew", types.PullRenewRequest{
		WorkerID: w.config.WorkerID,
		TaskID:   task.TaskID,
		LeaseID:  task.LeaseID,
	}, &response)
	if err != nil {
		return "", err
	}
	if statusCode == http.StatusUnauthorized {
		return response.Status, fmt.Errorf("renew authentication failed")
	}
	return response.Status, nil
}

func (w *PullWorker) submitResult(ctx context.Context, task *types.PullTask, result types.Result) (types.PullStatus, error) {
	var response types.PullResultResponse
	statusCode, err := w.post(ctx, "/worker/result", types.PullResultRequest{
		WorkerID: w.config.WorkerID,
		TaskID:   task.TaskID,
		LeaseID:  task.LeaseID,
		Result:   result,
	}, &response)
	if err != nil {
		return "", err
	}
	if statusCode == http.StatusUnauthorized {
		return response.Status, fmt.Errorf("result authentication failed")
	}
	return response.Status, nil
}

func (w *PullWorker) post(ctx context.Context, path string, request, response any) (int, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("https://%s%s", w.config.CoordinatorAddr, path), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.config.Token)

	resp, err := w.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return resp.StatusCode, fmt.Errorf("authentication rejected by coordinator")
	}
	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		return resp.StatusCode, fmt.Errorf("decode response: %w", err)
	}
	return resp.StatusCode, nil
}

func (w *PullWorker) markUnavailable(err error, retryAttempt int, retryDelay time.Duration) {
	now := time.Now()
	w.monitor.Unavailable(now, err, retryAttempt, now.Add(retryDelay))
	w.state.mu.Lock()
	if w.state.unavailable {
		w.state.mu.Unlock()
		return
	}
	w.state.unavailable = true
	w.state.mu.Unlock()
	log.Printf("[pull-worker] Coordinator unavailable: %v; retrying with backoff", err)
	w.monitor.Record(monitor.SystemEvent{
		Severity: monitor.SeverityWarning, Component: "worker", Kind: "coordinator_unavailable",
		Message: "Coordinator unavailable", Fields: map[string]string{"error": err.Error()},
	})
}

func (w *PullWorker) markConnected() {
	now := time.Now()
	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	if !w.state.everConnected {
		w.state.everConnected = true
		w.state.unavailable = false
		log.Printf("[pull-worker] Connected to coordinator at %s", w.config.CoordinatorAddr)
		w.monitor.Connected(now)
		w.monitor.Record(monitor.SystemEvent{Severity: monitor.SeverityInfo, Component: "worker", Kind: "connected", Message: "Connected to coordinator"})
		return
	}
	if w.state.unavailable {
		w.state.unavailable = false
		log.Printf("[pull-worker] Coordinator connection restored")
		w.monitor.Connected(now)
		w.monitor.Record(monitor.SystemEvent{Severity: monitor.SeverityInfo, Component: "worker", Kind: "connection_restored", Message: "Coordinator connection restored"})
		return
	}
	w.monitor.Connected(now)
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > defaultMaxBackoff {
		return defaultMaxBackoff
	}
	return next
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
