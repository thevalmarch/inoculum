package leasequeue

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestQueue(t *testing.T, clock *fakeClock, maxAttempts int) *Queue {
	t.Helper()
	var leaseNumber atomic.Int64
	q, err := New(Config{
		LeaseDuration: 10 * time.Second,
		MaxAttempts:   maxAttempts,
		Now:           clock.Now,
		NewLeaseID: func() string {
			return fmt.Sprintf("lease-%d", leaseNumber.Add(1))
		},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return q
}

func enqueue(t *testing.T, q *Queue, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := q.Enqueue(TaskSpec{ID: id, JobID: "job-1", Type: "diagnostic_sleep", Input: "1ms"}); err != nil {
			t.Fatalf("Enqueue(%q) error: %v", id, err)
		}
	}
}

func TestFIFOClaimAndNoTaskAvailable(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1", "task-2", "task-3")

	for i, want := range []string{"task-1", "task-2", "task-3"} {
		assignment, err := q.Claim(fmt.Sprintf("worker-%d", i))
		if err != nil {
			t.Fatalf("Claim() error: %v", err)
		}
		if assignment == nil || assignment.Task.ID != want {
			t.Fatalf("Claim() task = %#v, want %s", assignment, want)
		}
		if assignment.Lease.Attempt != 1 {
			t.Fatalf("attempt = %d, want 1", assignment.Lease.Attempt)
		}
	}

	assignment, err := q.Claim("worker-4")
	if err != nil || assignment != nil {
		t.Fatalf("empty Claim() = %#v, %v; want nil, nil", assignment, err)
	}
}

func TestRenewLease(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1")
	assignment, _ := q.Claim("worker-a")

	clock.Advance(8 * time.Second)
	renewed, err := q.Renew("task-1", assignment.Lease.ID, "worker-a")
	if err != nil {
		t.Fatalf("Renew() error: %v", err)
	}
	wantExpiry := clock.Now().Add(10 * time.Second)
	if !renewed.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expiry = %v, want %v", renewed.ExpiresAt, wantExpiry)
	}

	clock.Advance(9 * time.Second)
	if _, err := q.Renew("task-1", assignment.Lease.ID, "worker-a"); err != nil {
		t.Fatalf("Renew() after extension error: %v", err)
	}
}

func TestLeaseExpiryAndReassignment(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1")
	first, _ := q.Claim("worker-a")

	clock.Advance(10 * time.Second)
	second, err := q.Claim("worker-b")
	if err != nil {
		t.Fatalf("reassignment Claim() error: %v", err)
	}
	if second == nil || second.Task.ID != "task-1" {
		t.Fatalf("reassignment = %#v", second)
	}
	if second.Lease.ID == first.Lease.ID {
		t.Fatal("reassignment reused the old lease ID")
	}
	if second.Lease.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", second.Lease.Attempt)
	}
}

func TestUserKeySurvivesLeaseExpiryAndReassignment(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	if err := q.Enqueue(TaskSpec{ID: "task-1", JobID: "job-1", Key: "homepage", Type: "http_probe", Input: "https://example.com"}); err != nil {
		t.Fatal(err)
	}
	first, err := q.Claim("worker-a")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(10 * time.Second)
	second, err := q.Claim("worker-b")
	if err != nil {
		t.Fatal(err)
	}
	if second.Task.ID != first.Task.ID || second.Task.Key != "homepage" || second.Lease.Attempt != 2 {
		t.Fatalf("reassigned task = %#v", second)
	}
	task, _ := q.Get("task-1")
	if task.Key != "homepage" {
		t.Fatalf("stored key = %q", task.Key)
	}
}

func TestCompletionAndDuplicateResult(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1")
	assignment, _ := q.Claim("worker-a")

	outcome, err := q.Complete("task-1", assignment.Lease.ID, "worker-a", Result{Output: "ok"})
	if err != nil || outcome != OutcomeCompleted {
		t.Fatalf("Complete() = %q, %v", outcome, err)
	}
	task, _ := q.Get("task-1")
	if task.State != StateCompleted || task.Result == nil || task.Result.Output != "ok" {
		t.Fatalf("completed task = %#v", task)
	}

	_, err = q.Complete("task-1", assignment.Lease.ID, "worker-a", Result{Output: "duplicate"})
	if !errors.Is(err, ErrAlreadyCompleted) {
		t.Fatalf("duplicate result error = %v, want ErrAlreadyCompleted", err)
	}
}

func TestWrongLeaseAndWorkerAreRejected(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1")
	assignment, _ := q.Claim("worker-a")

	if _, err := q.Complete("task-1", "wrong-lease", "worker-a", Result{}); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("wrong lease error = %v, want ErrStaleLease", err)
	}
	if _, err := q.Complete("task-1", assignment.Lease.ID, "worker-b", Result{}); !errors.Is(err, ErrWrongWorker) {
		t.Fatalf("wrong worker error = %v, want ErrWrongWorker", err)
	}
}

func TestStaleResultAfterReassignment(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1")
	first, _ := q.Claim("worker-a")
	clock.Advance(10 * time.Second)
	second, _ := q.Claim("worker-b")

	if _, err := q.Complete("task-1", first.Lease.ID, "worker-a", Result{Output: "late"}); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("late result error = %v, want ErrStaleLease", err)
	}
	if outcome, err := q.Complete("task-1", second.Lease.ID, "worker-b", Result{Output: "current"}); err != nil || outcome != OutcomeCompleted {
		t.Fatalf("current result = %q, %v", outcome, err)
	}
}

func TestAttemptTrackingAndRetryExhaustion(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 2)
	enqueue(t, q, "task-1")

	first, _ := q.Claim("worker-a")
	if first.Lease.Attempt != 1 {
		t.Fatalf("first attempt = %d", first.Lease.Attempt)
	}
	clock.Advance(10 * time.Second)
	second, _ := q.Claim("worker-b")
	if second.Lease.Attempt != 2 {
		t.Fatalf("second attempt = %d", second.Lease.Attempt)
	}
	clock.Advance(10 * time.Second)
	expired := q.RequeueExpired()
	if len(expired) != 1 || expired[0] != "task-1" {
		t.Fatalf("expired = %v", expired)
	}

	task, _ := q.Get("task-1")
	if task.State != StateFailed || task.Result == nil || task.Result.Error == "" {
		t.Fatalf("exhausted task = %#v", task)
	}
	if assignment, _ := q.Claim("worker-c"); assignment != nil {
		t.Fatalf("failed task was claimable: %#v", assignment)
	}
}

func TestWorkerReportedFailureRetriesThenFails(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 2)
	enqueue(t, q, "task-1")

	first, _ := q.Claim("worker-a")
	outcome, err := q.Complete("task-1", first.Lease.ID, "worker-a", Result{Error: "executor failed"})
	if err != nil || outcome != OutcomeRequeued {
		t.Fatalf("first failure = %q, %v", outcome, err)
	}
	second, _ := q.Claim("worker-b")
	outcome, err = q.Complete("task-1", second.Lease.ID, "worker-b", Result{Error: "executor failed again"})
	if err != nil || outcome != OutcomeFailed {
		t.Fatalf("second failure = %q, %v", outcome, err)
	}
}

func TestConcurrentClaimsAreUnique(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	const taskCount = 200
	for i := 0; i < taskCount; i++ {
		enqueue(t, q, fmt.Sprintf("task-%03d", i))
	}

	claimed := make(chan string, taskCount)
	var wg sync.WaitGroup
	for worker := 0; worker < 20; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				assignment, err := q.Claim(fmt.Sprintf("worker-%d", worker))
				if err != nil {
					t.Errorf("Claim() error: %v", err)
					return
				}
				if assignment == nil {
					return
				}
				claimed <- assignment.Task.ID
			}
		}(worker)
	}
	wg.Wait()
	close(claimed)

	seen := make(map[string]bool)
	for id := range claimed {
		if seen[id] {
			t.Fatalf("task %s was claimed more than once", id)
		}
		seen[id] = true
	}
	if len(seen) != taskCount {
		t.Fatalf("claimed %d tasks, want %d", len(seen), taskCount)
	}
}

func TestDuplicateTaskIDRejected(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1")
	if err := q.Enqueue(TaskSpec{ID: "task-1"}); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("duplicate enqueue error = %v", err)
	}
}

func TestStatsAndSnapshot(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := newTestQueue(t, clock, 3)
	enqueue(t, q, "task-1", "task-2", "task-3")
	first, _ := q.Claim("worker-a")
	second, _ := q.Claim("worker-b")
	if _, err := q.Complete(first.Task.ID, first.Lease.ID, "worker-a", Result{Output: "ok"}); err != nil {
		t.Fatal(err)
	}

	stats := q.Stats()
	if stats.Queued != 1 || stats.Leased != 1 || stats.Completed != 1 || stats.Failed != 0 || stats.Total != 3 {
		t.Fatalf("Stats() = %#v", stats)
	}
	tasks := q.Snapshot()
	if len(tasks) != 3 || tasks[0].State != StateCompleted || tasks[1].State != StateLeased || tasks[2].State != StateQueued {
		t.Fatalf("Snapshot() = %#v", tasks)
	}
	// Snapshot must not expose the queue's mutable lease object.
	tasks[1].Lease.WorkerID = "modified"
	original, _ := q.Get(second.Task.ID)
	if original.Lease.WorkerID != "worker-b" {
		t.Fatal("Snapshot() returned mutable queue state")
	}
}

func TestObserveDoesNotExpireLease(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	queue := newTestQueue(t, clock, 3)
	if err := queue.Enqueue(TaskSpec{ID: "task-1"}); err != nil {
		t.Fatal(err)
	}
	assignment, err := queue.Claim("worker-a")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(10 * time.Second)

	observed := queue.Observe()
	if len(observed) != 1 || observed[0].State != StateLeased || observed[0].Lease.ID != assignment.Lease.ID {
		t.Fatalf("Observe() changed lease state: %#v", observed)
	}
	if expired := queue.RequeueExpired(); len(expired) != 1 {
		t.Fatalf("RequeueExpired() = %#v", expired)
	}
}
