package monitor

import (
	"sync"
	"time"

	"github.com/inoculum/internal/types"
)

type SubmitTracker struct {
	mu        sync.RWMutex
	snapshot  SubmitSnapshot
	startedAt time.Time
}

func NewSubmitTracker(total int) *SubmitTracker {
	return &SubmitTracker{snapshot: SubmitSnapshot{Job: JobProgress{Total: total, Queued: total, State: "submitting"}}}
}

func (t *SubmitTracker) Submitted(jobID string, total int, now time.Time) {
	t.mu.Lock()
	t.startedAt = now
	t.snapshot.Submitted = true
	t.snapshot.Job = JobProgress{JobID: jobID, State: "queued", Total: total, Queued: total, StartedAt: now}
	t.mu.Unlock()
}

func (t *SubmitTracker) Update(response types.PullJobResponse, now time.Time) JobProgress {
	t.mu.Lock()
	t.snapshot.Submitted = true
	t.snapshot.Job = JobFromResponse(response, t.startedAt, now)
	job := t.snapshot.Job
	t.mu.Unlock()
	return job
}

func (t *SubmitTracker) Finish(err error, timedOut bool) {
	t.mu.Lock()
	t.snapshot.Done = true
	t.snapshot.TimedOut = timedOut
	if err != nil {
		t.snapshot.Error = err.Error()
	}
	t.mu.Unlock()
}

func (t *SubmitTracker) Snapshot(now time.Time) SubmitSnapshot {
	t.mu.RLock()
	snapshot := t.snapshot
	startedAt := t.startedAt
	snapshot.Job.Tasks = append([]TaskProgress(nil), t.snapshot.Job.Tasks...)
	snapshot.Job.Workers = append([]WorkerContribution(nil), t.snapshot.Job.Workers...)
	t.mu.RUnlock()
	snapshot.ObservedAt = now
	if snapshot.Submitted && !snapshot.Done && !startedAt.IsZero() {
		snapshot.Job.Elapsed = now.Sub(startedAt)
	}
	return snapshot
}
