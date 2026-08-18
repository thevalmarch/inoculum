// Package plain renders stable line-oriented output without terminal control
// sequences. It is used explicitly with --plain and automatically for pipes,
// redirects, CI, and unsupported terminals.
package plain

import (
	"fmt"
	"io"
	"sort"

	"github.com/thevalmarch/inoculum/internal/monitor"
	"github.com/thevalmarch/inoculum/internal/presentation"
)

func CoordinatorStarted(writer io.Writer, snapshot monitor.CoordinatorSnapshot, verbose bool) {
	address := ":unknown"
	if len(snapshot.Addresses) > 0 {
		address = snapshot.Addresses[0]
	}
	fmt.Fprintf(writer, "coordinator online address=%s\n", address)
	if snapshot.Fingerprint != "" {
		fmt.Fprintf(writer, "coordinator fingerprint=%s\n", snapshot.Fingerprint)
	}
	if verbose && len(snapshot.Addresses) > 1 {
		fmt.Fprintf(writer, "coordinator addresses=%q\n", snapshot.Addresses)
	}
}

func WorkerStarted(writer io.Writer, snapshot monitor.WorkerSnapshot) {
	fmt.Fprintf(writer, "worker starting id=%s coordinator=%s concurrency=%d\n",
		presentation.SafeText(snapshot.WorkerID), presentation.SafeText(snapshot.Coordinator), snapshot.Concurrency)
}

func SubmitProgress(writer io.Writer, job monitor.JobProgress) {
	fmt.Fprintf(writer, "job=%s queued=%d running=%d completed=%d failed=%d\n",
		presentation.SafeText(job.JobID), job.Queued, job.Running, job.Completed, job.Failed)
}

func SubmitSummary(writer io.Writer, job monitor.JobProgress, verbose, unicode bool) {
	submitSummary(writer, job, verbose, unicode, true)
}

// ManifestSubmitSummary keeps large batches progress-oriented. Per-task
// details belong in the manifest result export instead of the terminal.
func ManifestSubmitSummary(writer io.Writer, job monitor.JobProgress, unicode bool) {
	submitSummary(writer, job, false, unicode, false)
}

func submitSummary(writer io.Writer, job monitor.JobProgress, verbose, unicode, showFailures bool) {
	symbol := "OK"
	state := "completed"
	if job.Failed > 0 || job.State == "failed" {
		symbol = "X"
		state = "failed"
	} else if unicode {
		symbol = "✓"
	}
	fmt.Fprintf(writer, "%s Job %s\n\n", symbol, state)
	fmt.Fprintf(writer, "Job        %s\n", presentation.SafeText(job.JobID))
	fmt.Fprintf(writer, "Completed  %d\n", job.Completed)
	fmt.Fprintf(writer, "Failed     %d\n", job.Failed)
	fmt.Fprintf(writer, "Elapsed    %s\n", presentation.CompactDuration(job.Elapsed))

	workers := append([]monitor.WorkerContribution(nil), job.Workers...)
	sort.Slice(workers, func(i, j int) bool { return workers[i].WorkerID < workers[j].WorkerID })
	if len(workers) > 0 {
		fmt.Fprintln(writer, "\nWorkers")
		for _, worker := range workers {
			fmt.Fprintf(writer, "%-20s %d completed\n", presentation.SafeText(worker.WorkerID), worker.Completed)
		}
	}

	if job.Failed > 0 && showFailures {
		fmt.Fprintln(writer, "\nFailures")
		for _, task := range job.Tasks {
			if task.State == "failed" {
				fmt.Fprintf(writer, "%s: %s (%d attempts exhausted)\n", presentation.SafeText(task.TaskID), presentation.SafeText(task.Error), task.Attempt)
			}
		}
	}
	if verbose {
		fmt.Fprintln(writer, "\nTasks")
		for _, task := range job.Tasks {
			fmt.Fprintf(writer, "%s state=%s attempts=%d worker=%s duration=%s",
				presentation.SafeText(task.TaskID), presentation.SafeText(task.State), task.Attempt, presentation.SafeText(task.WorkerID), presentation.CompactDuration(task.Duration))
			if task.Error != "" {
				fmt.Fprintf(writer, " error=%q", presentation.SafeText(task.Error))
			} else if task.Output != "" {
				fmt.Fprintf(writer, " output=%q", presentation.SafeText(task.Output))
			}
			fmt.Fprintln(writer)
		}
	}
}

func StoppedWaiting(writer io.Writer, jobID string, wait string) {
	fmt.Fprintf(writer, "! Stopped waiting after %s\n\n", wait)
	fmt.Fprintln(writer, "The job was not marked failed and may still be running.")
	if jobID != "" {
		fmt.Fprintf(writer, "\nJob\n%s\n", presentation.SafeText(jobID))
	}
}
