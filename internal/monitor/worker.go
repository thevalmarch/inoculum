package monitor

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// WorkerTracker records only local worker observability state. It does not
// participate in claiming, renewing, retrying, or completing tasks.
type WorkerTracker struct {
	mu       sync.RWMutex
	snapshot WorkerSnapshot
	events   *Recorder
}

func NewWorkerTracker(workerID, coordinator string, concurrency int) *WorkerTracker {
	return &WorkerTracker{
		snapshot: WorkerSnapshot{
			WorkerID:    workerID,
			Coordinator: coordinator,
			Connection:  ConnectionStarting,
			Concurrency: concurrency,
		},
		events: NewRecorder(defaultEventLimit),
	}
}

func (t *WorkerTracker) Connected(now time.Time) {
	t.mu.Lock()
	if t.snapshot.Connection != ConnectionConnected {
		t.snapshot.ConnectedAt = now
	}
	t.snapshot.Connection = ConnectionConnected
	t.snapshot.UnavailableSince = time.Time{}
	t.snapshot.LastError = ""
	t.snapshot.RetryAttempt = 0
	t.snapshot.RetryAt = time.Time{}
	t.mu.Unlock()
}

func (t *WorkerTracker) Unavailable(now time.Time, err error, attempt int, retryAt time.Time) {
	t.mu.Lock()
	state := ConnectionUnavailable
	if err != nil {
		lower := strings.ToLower(err.Error())
		switch {
		case strings.Contains(lower, "authentication rejected"), strings.Contains(lower, "http 401"):
			state = ConnectionAuthFailed
		case strings.Contains(lower, "no coordinator identity is trusted"):
			state = ConnectionUntrusted
		case strings.Contains(lower, "fingerprint mismatch"), strings.Contains(lower, "certificate changed"), strings.Contains(lower, "coordinator identity mismatch"):
			state = ConnectionIdentity
		}
	}
	if t.snapshot.Connection != state {
		t.snapshot.UnavailableSince = now
	}
	t.snapshot.Connection = state
	if err != nil {
		t.snapshot.LastError = err.Error()
	}
	t.snapshot.RetryAttempt = attempt
	t.snapshot.RetryAt = retryAt
	t.mu.Unlock()
}

func (t *WorkerTracker) Stopping() {
	t.mu.Lock()
	t.snapshot.Connection = ConnectionStopping
	t.mu.Unlock()
}

func (t *WorkerTracker) TaskStarted(task TaskProgress) {
	t.mu.Lock()
	for i := range t.snapshot.ActiveTasks {
		if t.snapshot.ActiveTasks[i].TaskID == task.TaskID {
			t.snapshot.ActiveTasks[i] = task
			t.mu.Unlock()
			return
		}
	}
	t.snapshot.ActiveTasks = append(t.snapshot.ActiveTasks, task)
	t.mu.Unlock()
}

func (t *WorkerTracker) TaskFinished(taskID string, completed, failed bool) {
	t.mu.Lock()
	for i := range t.snapshot.ActiveTasks {
		if t.snapshot.ActiveTasks[i].TaskID == taskID {
			t.snapshot.ActiveTasks = append(t.snapshot.ActiveTasks[:i], t.snapshot.ActiveTasks[i+1:]...)
			break
		}
	}
	if completed {
		t.snapshot.Completed++
	}
	if failed {
		t.snapshot.Failed++
	}
	t.mu.Unlock()
}

func (t *WorkerTracker) Record(event SystemEvent) {
	t.events.Record(event)
}

func (t *WorkerTracker) Snapshot(now time.Time) WorkerSnapshot {
	t.mu.RLock()
	snapshot := t.snapshot
	snapshot.ActiveTasks = append([]TaskProgress(nil), t.snapshot.ActiveTasks...)
	t.mu.RUnlock()
	snapshot.ObservedAt = now
	snapshot.Events = t.events.Snapshot()
	sort.Slice(snapshot.ActiveTasks, func(i, j int) bool { return snapshot.ActiveTasks[i].TaskID < snapshot.ActiveTasks[j].TaskID })
	return snapshot
}
