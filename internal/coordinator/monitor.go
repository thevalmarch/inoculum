package coordinator

import (
	"sort"
	"time"

	"github.com/thevalmarch/inoculum/internal/leasequeue"
	"github.com/thevalmarch/inoculum/internal/monitor"
)

// MonitorSnapshot returns an immutable, presentation-neutral view of the
// coordinator. It observes queue state without changing queue semantics.
func (s *Server) MonitorSnapshot(addresses []string, fingerprint string) monitor.CoordinatorSnapshot {
	now := time.Now()
	tasks := s.pullQueue.Observe()

	snapshot := monitor.CoordinatorSnapshot{
		ObservedAt:  now,
		Online:      true,
		Addresses:   append([]string(nil), addresses...),
		Fingerprint: fingerprint,
		Uptime:      now.Sub(s.startTime),
		Tasks:       monitor.TaskCounts{Total: len(tasks)},
		Events:      s.events.Snapshot(),
	}

	jobs := make(map[string]*monitor.JobProgress)
	jobOrder := make([]string, 0)
	for _, task := range tasks {
		job := jobs[task.JobID]
		if job == nil {
			job = &monitor.JobProgress{JobID: task.JobID}
			jobs[task.JobID] = job
			jobOrder = append(jobOrder, task.JobID)
		}
		job.Total++

		progress := monitor.TaskProgress{
			TaskID:   task.ID,
			Key:      task.Key,
			State:    string(task.State),
			WorkerID: task.WorkerID,
			Attempt:  task.Attempts,
		}
		switch task.State {
		case leasequeue.StateQueued:
			snapshot.Tasks.Queued++
			job.Queued++
		case leasequeue.StateLeased:
			snapshot.Tasks.Running++
			job.Running++
			if task.Lease != nil {
				progress.StartedAt = task.Lease.IssuedAt
				progress.Duration = now.Sub(task.Lease.IssuedAt)
				if job.StartedAt.IsZero() || task.Lease.IssuedAt.Before(job.StartedAt) {
					job.StartedAt = task.Lease.IssuedAt
				}
			}
		case leasequeue.StateCompleted:
			snapshot.Tasks.Completed++
			job.Completed++
		case leasequeue.StateFailed:
			snapshot.Tasks.Failed++
			job.Failed++
		}
		if task.Result != nil {
			progress.Duration = task.Result.Duration
			progress.Output = task.Result.Output
			progress.Error = task.Result.Error
		}
		job.Tasks = append(job.Tasks, progress)
	}
	snapshot.Jobs = len(jobs)

	for _, jobID := range jobOrder {
		job := jobs[jobID]
		switch {
		case job.Failed > 0 && job.Completed+job.Failed == job.Total:
			job.State = "failed"
		case job.Completed == job.Total:
			job.State = "completed"
		case job.Running > 0 || job.Completed > 0 || job.Failed > 0:
			job.State = "running"
		default:
			job.State = "queued"
		}
		if !job.StartedAt.IsZero() {
			job.Elapsed = now.Sub(job.StartedAt)
		}
		if job.State == "queued" || job.State == "running" {
			copy := *job
			copy.Tasks = append([]monitor.TaskProgress(nil), job.Tasks...)
			snapshot.CurrentJob = &copy
		}
	}

	s.workerMu.Lock()
	for workerID, lastActivity := range s.workerActivity {
		if lastActivity.Before(now.Add(-recentWorkerWindow)) {
			delete(s.workerActivity, workerID)
			continue
		}
		worker := monitor.WorkerSummary{WorkerID: workerID, LastActivity: lastActivity}
		for _, task := range tasks {
			if task.State == leasequeue.StateLeased && task.Lease != nil && task.Lease.WorkerID == workerID {
				worker.Active++
			}
		}
		snapshot.Workers = append(snapshot.Workers, worker)
	}
	s.workerMu.Unlock()
	sort.Slice(snapshot.Workers, func(i, j int) bool { return snapshot.Workers[i].WorkerID < snapshot.Workers[j].WorkerID })

	return snapshot
}
