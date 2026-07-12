package coordinator

import (
	"sync"

	"github.com/inoculum/internal/types"
)

// ScheduleStrategy determines how tasks are assigned to workers.
type ScheduleStrategy int

const (
	// RoundRobin assigns tasks in a rotating fashion.
	RoundRobin ScheduleStrategy = iota
	// LeastBusy assigns tasks to the worker with the fewest active tasks.
	LeastBusy
)

// Scheduler assigns tasks to available workers.
type Scheduler struct {
	mu       sync.Mutex
	strategy ScheduleStrategy
	rrIndex  int // round-robin cursor
}

// NewScheduler creates a scheduler with the given strategy.
func NewScheduler(strategy ScheduleStrategy) *Scheduler {
	return &Scheduler{
		strategy: strategy,
	}
}

// SetStrategy changes the scheduling strategy at runtime.
func (s *Scheduler) SetStrategy(strategy ScheduleStrategy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.strategy = strategy
}

// Pick selects the best worker for the next task from the available pool.
// Returns nil if no workers are available.
func (s *Scheduler) Pick(workers []*types.WorkerInfo) *types.WorkerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(workers) == 0 {
		return nil
	}

	switch s.strategy {
	case LeastBusy:
		return s.pickLeastBusy(workers)
	default:
		return s.pickRoundRobin(workers)
	}
}

// pickRoundRobin cycles through workers in order.
func (s *Scheduler) pickRoundRobin(workers []*types.WorkerInfo) *types.WorkerInfo {
	if s.rrIndex >= len(workers) {
		s.rrIndex = 0
	}
	w := workers[s.rrIndex]
	s.rrIndex = (s.rrIndex + 1) % len(workers)
	return w
}

// pickLeastBusy returns the worker with the fewest active tasks.
func (s *Scheduler) pickLeastBusy(workers []*types.WorkerInfo) *types.WorkerInfo {
	best := workers[0]
	for _, w := range workers[1:] {
		if w.ActiveTasks < best.ActiveTasks {
			best = w
		}
	}
	return best
}
