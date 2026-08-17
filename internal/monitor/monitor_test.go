package monitor

import (
	"errors"
	"testing"
	"time"

	"github.com/inoculum/internal/types"
)

func TestRecorderIsBoundedAndReturnsCopies(t *testing.T) {
	recorder := NewRecorder(2)
	recorder.Record(SystemEvent{Kind: "one", Fields: map[string]string{"value": "original"}})
	recorder.Record(SystemEvent{Kind: "two"})
	recorder.Record(SystemEvent{Kind: "three"})

	events := recorder.Snapshot()
	if len(events) != 2 || events[0].Kind != "two" || events[1].Kind != "three" {
		t.Fatalf("events = %#v", events)
	}

	recorder = NewRecorder(2)
	recorder.Record(SystemEvent{Kind: "copy", Fields: map[string]string{"value": "original"}})
	events = recorder.Snapshot()
	events[0].Fields["value"] = "changed"
	if got := recorder.Snapshot()[0].Fields["value"]; got != "original" {
		t.Fatalf("recorder field mutated through snapshot: %q", got)
	}
}

func TestJobFromResponseCountsAndWorkers(t *testing.T) {
	started := time.Unix(100, 0)
	observed := started.Add(5 * time.Second)
	response := types.PullJobResponse{
		JobID: "job-1",
		State: types.PullJobRunning,
		Tasks: []types.PullJobTask{
			{TaskID: "task-1", State: "completed", WorkerID: "worker-b", Attempts: 1, Result: &types.Result{Duration: time.Second}},
			{TaskID: "task-2", State: "leased", WorkerID: "worker-a", Attempts: 2},
			{TaskID: "task-3", State: "queued"},
			{TaskID: "task-4", State: "failed", WorkerID: "worker-b", Attempts: 3, Result: &types.Result{Error: "boom"}},
		},
	}

	job := JobFromResponse(response, started, observed)
	if job.Queued != 1 || job.Running != 1 || job.Completed != 1 || job.Failed != 1 || job.Elapsed != 5*time.Second {
		t.Fatalf("job = %#v", job)
	}
	if len(job.Workers) != 2 || job.Workers[0].WorkerID != "worker-a" || job.Workers[0].Active != 1 {
		t.Fatalf("workers = %#v", job.Workers)
	}
}

func TestWorkerTrackerConnectionTasksAndCopies(t *testing.T) {
	tracker := NewWorkerTracker("worker-a", "host:8080", 2)
	now := time.Unix(100, 0)
	tracker.Unavailable(now, errors.New("dial refused"), 2, now.Add(4*time.Second))
	snapshot := tracker.Snapshot(now)
	if snapshot.Connection != ConnectionUnavailable || snapshot.RetryAttempt != 2 {
		t.Fatalf("unavailable snapshot = %#v", snapshot)
	}

	tracker.Connected(now.Add(time.Second))
	tracker.TaskStarted(TaskProgress{TaskID: "task-1", StartedAt: now})
	snapshot = tracker.Snapshot(now.Add(2 * time.Second))
	if snapshot.Connection != ConnectionConnected || len(snapshot.ActiveTasks) != 1 {
		t.Fatalf("connected snapshot = %#v", snapshot)
	}
	snapshot.ActiveTasks[0].TaskID = "mutated"
	if got := tracker.Snapshot(now).ActiveTasks[0].TaskID; got != "task-1" {
		t.Fatalf("tracker mutated through snapshot: %q", got)
	}

	tracker.TaskFinished("task-1", true, false)
	snapshot = tracker.Snapshot(now)
	if len(snapshot.ActiveTasks) != 0 || snapshot.Completed != 1 || snapshot.Failed != 0 {
		t.Fatalf("finished snapshot = %#v", snapshot)
	}
}

func TestWorkerTrackerClassifiesActionableSecurityErrors(t *testing.T) {
	tracker := NewWorkerTracker("worker-a", "host:8080", 1)
	now := time.Now()
	tracker.Unavailable(now, errors.New("authentication rejected by coordinator"), 1, now.Add(time.Second))
	if got := tracker.Snapshot(now).Connection; got != ConnectionAuthFailed {
		t.Fatalf("authentication state = %q", got)
	}
	tracker.Unavailable(now, errors.New("certificate fingerprint mismatch"), 1, now.Add(time.Second))
	if got := tracker.Snapshot(now).Connection; got != ConnectionIdentity {
		t.Fatalf("identity state = %q", got)
	}
	tracker.Unavailable(now, errors.New("no coordinator identity is trusted yet"), 1, now.Add(time.Second))
	if got := tracker.Snapshot(now).Connection; got != ConnectionUntrusted {
		t.Fatalf("untrusted state = %q", got)
	}
}

func TestSubmitTrackerSnapshotIsImmutable(t *testing.T) {
	tracker := NewSubmitTracker(1)
	now := time.Now()
	tracker.Submitted("job-1", 1, now)
	tracker.Update(types.PullJobResponse{JobID: "job-1", State: types.PullJobRunning, Tasks: []types.PullJobTask{{TaskID: "task-1", State: "leased"}}}, now)
	snapshot := tracker.Snapshot(now)
	snapshot.Job.Tasks[0].TaskID = "mutated"
	if got := tracker.Snapshot(now).Job.Tasks[0].TaskID; got != "task-1" {
		t.Fatalf("submit tracker mutated through snapshot: %q", got)
	}
}
