package monitor

import (
	"sort"
	"time"

	"github.com/thevalmarch/inoculum/internal/types"
)

// JobFromResponse converts the wire representation into presentation-neutral
// progress. It does not alter coordinator state.
func JobFromResponse(response types.PullJobResponse, startedAt, observedAt time.Time) JobProgress {
	job := JobProgress{
		JobID:     response.JobID,
		State:     string(response.State),
		Total:     len(response.Tasks),
		StartedAt: startedAt,
	}
	if !startedAt.IsZero() {
		job.Elapsed = observedAt.Sub(startedAt)
	}

	workers := make(map[string]*WorkerContribution)
	for _, task := range response.Tasks {
		progress := TaskProgress{
			TaskID:   task.TaskID,
			Key:      task.Key,
			State:    task.State,
			WorkerID: task.WorkerID,
			Attempt:  task.Attempts,
		}
		switch task.State {
		case "queued":
			job.Queued++
		case "leased":
			job.Running++
		case "completed":
			job.Completed++
		case "failed":
			job.Failed++
		}
		if task.Result != nil {
			progress.Duration = task.Result.Duration
			progress.Output = task.Result.Output
			progress.Error = task.Result.Error
		}
		job.Tasks = append(job.Tasks, progress)

		if task.WorkerID == "" {
			continue
		}
		worker := workers[task.WorkerID]
		if worker == nil {
			worker = &WorkerContribution{WorkerID: task.WorkerID}
			workers[task.WorkerID] = worker
		}
		switch task.State {
		case "leased":
			worker.Active++
		case "completed":
			worker.Completed++
		case "failed":
			worker.Failed++
		}
	}

	for _, worker := range workers {
		job.Workers = append(job.Workers, *worker)
	}
	sort.Slice(job.Workers, func(i, j int) bool { return job.Workers[i].WorkerID < job.Workers[j].WorkerID })
	return job
}
