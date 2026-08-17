// Package leasequeue implements the coordinator's pull-based FIFO task queue.
//
// Delivery is deliberately at-least-once. A worker can execute a task and lose
// its result before the coordinator receives it. The lease may then expire and
// the task may execute again, so task implementations must tolerate duplicates.
package leasequeue

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type TaskState string

const (
	StateQueued    TaskState = "queued"
	StateLeased    TaskState = "leased"
	StateCompleted TaskState = "completed"
	StateFailed    TaskState = "failed"
)

type CompletionOutcome string

const (
	OutcomeCompleted CompletionOutcome = "completed"
	OutcomeRequeued  CompletionOutcome = "requeued"
	OutcomeFailed    CompletionOutcome = "failed"
)

var (
	ErrTaskExists       = errors.New("task already exists")
	ErrTaskNotFound     = errors.New("task not found")
	ErrInvalidWorker    = errors.New("worker ID is required")
	ErrStaleLease       = errors.New("stale lease")
	ErrWrongWorker      = errors.New("lease belongs to another worker")
	ErrAlreadyCompleted = errors.New("task already completed")
	ErrTaskFailed       = errors.New("task has permanently failed")
)

type Config struct {
	LeaseDuration time.Duration
	MaxAttempts   int
	Now           func() time.Time
	NewLeaseID    func() string
}

type TaskSpec struct {
	ID    string
	JobID string
	Key   string
	Type  string
	Input string
}

type Result struct {
	Output   string
	Duration time.Duration
	Error    string
}

type Lease struct {
	ID        string
	TaskID    string
	WorkerID  string
	IssuedAt  time.Time
	ExpiresAt time.Time
	Attempt   int
}

type Task struct {
	ID       string
	JobID    string
	Key      string
	Type     string
	Input    string
	State    TaskState
	Attempts int
	WorkerID string
	Lease    *Lease
	Result   *Result
}

type Assignment struct {
	Task  TaskSpec
	Lease Lease
}

type Stats struct {
	Queued    int
	Leased    int
	Completed int
	Failed    int
	Total     int
}

type Queue struct {
	mu            sync.Mutex
	leaseDuration time.Duration
	maxAttempts   int
	now           func() time.Time
	newLeaseID    func() string
	queued        []string
	order         []string
	tasks         map[string]*Task
}

func New(config Config) (*Queue, error) {
	if config.LeaseDuration <= 0 {
		return nil, fmt.Errorf("lease duration must be positive")
	}
	if config.MaxAttempts <= 0 {
		return nil, fmt.Errorf("max attempts must be positive")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewLeaseID == nil {
		config.NewLeaseID = generateLeaseID
	}

	return &Queue{
		leaseDuration: config.LeaseDuration,
		maxAttempts:   config.MaxAttempts,
		now:           config.Now,
		newLeaseID:    config.NewLeaseID,
		tasks:         make(map[string]*Task),
	}, nil
}

func (q *Queue) Enqueue(spec TaskSpec) error {
	if spec.ID == "" {
		return fmt.Errorf("task ID is required")
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if _, exists := q.tasks[spec.ID]; exists {
		return ErrTaskExists
	}

	q.tasks[spec.ID] = &Task{
		ID:    spec.ID,
		JobID: spec.JobID,
		Key:   spec.Key,
		Type:  spec.Type,
		Input: spec.Input,
		State: StateQueued,
	}
	q.queued = append(q.queued, spec.ID)
	q.order = append(q.order, spec.ID)
	return nil
}

// Claim leases the oldest queued task to workerID. A nil assignment means
// there is currently no work. Claim also makes expired work eligible again.
func (q *Queue) Claim(workerID string) (*Assignment, error) {
	if workerID == "" {
		return nil, ErrInvalidWorker
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.now()
	q.expireLocked(now)

	for len(q.queued) > 0 {
		taskID := q.queued[0]
		q.queued = q.queued[1:]
		task := q.tasks[taskID]
		if task == nil || task.State != StateQueued {
			continue
		}

		task.Attempts++
		lease := &Lease{
			ID:        q.newLeaseID(),
			TaskID:    task.ID,
			WorkerID:  workerID,
			IssuedAt:  now,
			ExpiresAt: now.Add(q.leaseDuration),
			Attempt:   task.Attempts,
		}
		task.State = StateLeased
		task.WorkerID = workerID
		task.Lease = lease

		return &Assignment{
			Task:  TaskSpec{ID: task.ID, JobID: task.JobID, Key: task.Key, Type: task.Type, Input: task.Input},
			Lease: *lease,
		}, nil
	}

	return nil, nil
}

func (q *Queue) Renew(taskID, leaseID, workerID string) (Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.now()
	task, err := q.activeLeaseLocked(taskID, leaseID, workerID, now)
	if err != nil {
		return Lease{}, err
	}

	task.Lease.ExpiresAt = now.Add(q.leaseDuration)
	return *task.Lease, nil
}

// Complete records a successful result, or requeues a worker-reported failure
// until the retry policy is exhausted.
func (q *Queue) Complete(taskID, leaseID, workerID string, result Result) (CompletionOutcome, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	task, err := q.activeLeaseLocked(taskID, leaseID, workerID, q.now())
	if err != nil {
		return "", err
	}

	resultCopy := result
	if result.Error == "" {
		task.State = StateCompleted
		task.Result = &resultCopy
		task.Lease = nil
		return OutcomeCompleted, nil
	}

	task.Lease = nil
	if task.Attempts >= q.maxAttempts {
		task.State = StateFailed
		task.Result = &resultCopy
		return OutcomeFailed, nil
	}

	task.State = StateQueued
	task.Result = nil
	q.queued = append(q.queued, task.ID)
	return OutcomeRequeued, nil
}

// RequeueExpired returns expired task IDs. Tasks that exhausted MaxAttempts
// transition to failed instead of being queued again.
func (q *Queue) RequeueExpired() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.expireLocked(q.now())
}

func (q *Queue) Get(taskID string) (Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(q.now())
	task, ok := q.tasks[taskID]
	if !ok {
		return Task{}, false
	}
	return cloneTask(task), true
}

func (q *Queue) JobTasks(jobID string) []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(q.now())

	var tasks []Task
	for _, id := range q.order {
		task := q.tasks[id]
		if task.JobID == jobID {
			tasks = append(tasks, cloneTask(task))
		}
	}
	return tasks
}

// Snapshot returns a consistent copy of every task in enqueue order. Expired
// leases are processed before the snapshot is created.
func (q *Queue) Snapshot() []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(q.now())

	tasks := make([]Task, 0, len(q.order))
	for _, id := range q.order {
		tasks = append(tasks, cloneTask(q.tasks[id]))
	}
	return tasks
}

// Observe returns a consistent copy of every task without advancing lease
// state. It exists for passive monitoring; expiration remains owned by the
// queue's normal claim/status operations.
func (q *Queue) Observe() []Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	tasks := make([]Task, 0, len(q.order))
	for _, id := range q.order {
		tasks = append(tasks, cloneTask(q.tasks[id]))
	}
	return tasks
}

func (q *Queue) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.expireLocked(q.now())

	stats := Stats{Total: len(q.tasks)}
	for _, task := range q.tasks {
		switch task.State {
		case StateQueued:
			stats.Queued++
		case StateLeased:
			stats.Leased++
		case StateCompleted:
			stats.Completed++
		case StateFailed:
			stats.Failed++
		}
	}
	return stats
}

func (q *Queue) activeLeaseLocked(taskID, leaseID, workerID string, now time.Time) (*Task, error) {
	task, ok := q.tasks[taskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	if task.State == StateCompleted {
		return nil, ErrAlreadyCompleted
	}
	if task.State == StateFailed {
		return nil, ErrTaskFailed
	}
	if task.State != StateLeased || task.Lease == nil || task.Lease.ID != leaseID {
		return nil, ErrStaleLease
	}
	if task.Lease.WorkerID != workerID {
		return nil, ErrWrongWorker
	}
	if !now.Before(task.Lease.ExpiresAt) {
		q.expireTaskLocked(task)
		return nil, ErrStaleLease
	}
	return task, nil
}

func (q *Queue) expireLocked(now time.Time) []string {
	var expired []string
	for _, id := range q.order {
		task := q.tasks[id]
		if task.State == StateLeased && task.Lease != nil && !now.Before(task.Lease.ExpiresAt) {
			expired = append(expired, task.ID)
			q.expireTaskLocked(task)
		}
	}
	return expired
}

func (q *Queue) expireTaskLocked(task *Task) {
	task.Lease = nil
	if task.Attempts >= q.maxAttempts {
		task.State = StateFailed
		task.Result = &Result{Error: "lease expired and retry policy was exhausted"}
		return
	}
	task.State = StateQueued
	task.Result = nil
	q.queued = append(q.queued, task.ID)
}

func cloneTask(task *Task) Task {
	copy := *task
	if task.Lease != nil {
		lease := *task.Lease
		copy.Lease = &lease
	}
	if task.Result != nil {
		result := *task.Result
		copy.Result = &result
	}
	return copy
}

var fallbackLeaseID atomic.Uint64

func generateLeaseID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("lease-%d-%d", time.Now().UnixNano(), fallbackLeaseID.Add(1))
}
