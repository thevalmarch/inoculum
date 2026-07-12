package coordinator

import (
	"log"
	"sync"
	"time"

	"github.com/inoculum/internal/types"
)

const (
	// HeartbeatTimeout is how long before a worker is considered stale.
	HeartbeatTimeout = 30 * time.Second
	// CleanupInterval is how often the registry checks for stale workers.
	CleanupInterval = 10 * time.Second
)

// Registry manages the set of known workers.
type Registry struct {
	mu      sync.RWMutex
	workers map[string]*types.WorkerInfo
}

// NewRegistry creates an empty worker registry.
func NewRegistry() *Registry {
	return &Registry{
		workers: make(map[string]*types.WorkerInfo),
	}
}

// Register adds or updates a worker in the registry.
func (r *Registry) Register(req types.RegisterRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.workers[req.ID] = &types.WorkerInfo{
		ID:            req.ID,
		Address:       req.Address,
		Hostname:      req.Hostname,
		CPUCores:      req.CPUCores,
		RAMBytes:      req.RAMBytes,
		GPUInfo:       req.GPUInfo,
		LastHeartbeat: time.Now(),
		Busy:          false,
		ActiveTasks:   0,
	}
	log.Printf("[registry] Worker registered: %s (%s) at %s", req.ID, req.Hostname, req.Address)
}

// Heartbeat updates the last-seen timestamp for a worker.
func (r *Registry) Heartbeat(workerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	w, ok := r.workers[workerID]
	if !ok {
		return false
	}
	w.LastHeartbeat = time.Now()
	return true
}

// GetAvailable returns all workers whose heartbeat is fresh.
func (r *Registry) GetAvailable() []*types.WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var available []*types.WorkerInfo
	cutoff := time.Now().Add(-HeartbeatTimeout)
	for _, w := range r.workers {
		if w.LastHeartbeat.After(cutoff) {
			available = append(available, w)
		}
	}
	return available
}

// GetAll returns all known workers (including stale ones).
func (r *Registry) GetAll() []*types.WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := make([]*types.WorkerInfo, 0, len(r.workers))
	for _, w := range r.workers {
		all = append(all, w)
	}
	return all
}

// ActiveCount returns the number of workers with a fresh heartbeat.
func (r *Registry) ActiveCount() int {
	return len(r.GetAvailable())
}

// TotalCount returns the total number of registered workers.
func (r *Registry) TotalCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.workers)
}

// SetWorkerBusy marks a worker as busy or idle.
func (r *Registry) SetWorkerBusy(workerID string, busy bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if w, ok := r.workers[workerID]; ok {
		w.Busy = busy
	}
}

// IncrementActiveTasks increases the active task count for a worker.
func (r *Registry) IncrementActiveTasks(workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if w, ok := r.workers[workerID]; ok {
		w.ActiveTasks++
		w.Busy = true
	}
}

// DecrementActiveTasks decreases the active task count for a worker.
func (r *Registry) DecrementActiveTasks(workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if w, ok := r.workers[workerID]; ok {
		w.ActiveTasks--
		if w.ActiveTasks <= 0 {
			w.ActiveTasks = 0
			w.Busy = false
		}
	}
}

// RemoveStale removes workers whose heartbeat is older than the timeout.
func (r *Registry) RemoveStale() {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-HeartbeatTimeout)
	for id, w := range r.workers {
		if w.LastHeartbeat.Before(cutoff) {
			log.Printf("[registry] Removing stale worker: %s (%s)", id, w.Hostname)
			delete(r.workers, id)
		}
	}
}

// StartCleanup starts a background goroutine that periodically removes stale workers.
func (r *Registry) StartCleanup(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.RemoveStale()
			case <-stop:
				return
			}
		}
	}()
}
