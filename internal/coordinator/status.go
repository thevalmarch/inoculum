package coordinator

import (
	"net/http"
	"sort"
	"time"

	"github.com/thevalmarch/inoculum/internal/leasequeue"
	"github.com/thevalmarch/inoculum/internal/types"
)

const recentWorkerWindow = 30 * time.Second

func (s *Server) handlePullStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	for _, taskID := range s.pullQueue.RequeueExpired() {
		s.logExpiredLease(taskID)
	}
	tasks := s.pullQueue.Snapshot()
	stats := countTaskStates(tasks)
	activeLeases := make(map[string]int)
	jobs := make(map[string]struct{})
	for _, task := range tasks {
		jobs[task.JobID] = struct{}{}
		if task.State == leasequeue.StateLeased && task.Lease != nil {
			activeLeases[task.Lease.WorkerID]++
		}
	}

	cutoff := time.Now().Add(-recentWorkerWindow)
	s.workerMu.Lock()
	workers := make([]types.PullWorkerStatus, 0, len(s.workerActivity))
	for workerID, lastActivity := range s.workerActivity {
		if lastActivity.Before(cutoff) {
			delete(s.workerActivity, workerID)
			continue
		}
		workers = append(workers, types.PullWorkerStatus{
			WorkerID:     workerID,
			LastActivity: lastActivity,
			ActiveLeases: activeLeases[workerID],
		})
	}
	s.workerMu.Unlock()
	sort.Slice(workers, func(i, j int) bool { return workers[i].WorkerID < workers[j].WorkerID })

	writeJSON(w, types.CoordinatorStatusResponse{
		QueuedTasks:    stats.Queued,
		LeasedTasks:    stats.Leased,
		CompletedTasks: stats.Completed,
		FailedTasks:    stats.Failed,
		TotalTasks:     stats.Total,
		TotalJobs:      len(jobs),
		RecentWorkers:  workers,
		Uptime:         time.Since(s.startTime).String(),
	})
}

func countTaskStates(tasks []leasequeue.Task) leasequeue.Stats {
	stats := leasequeue.Stats{Total: len(tasks)}
	for _, task := range tasks {
		switch task.State {
		case leasequeue.StateQueued:
			stats.Queued++
		case leasequeue.StateLeased:
			stats.Leased++
		case leasequeue.StateCompleted:
			stats.Completed++
		case leasequeue.StateFailed:
			stats.Failed++
		}
	}
	return stats
}
