package presentation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/thevalmarch/inoculum/internal/monitor"
)

func CoordinatorFrame(snapshot monitor.CoordinatorSnapshot, width, height int, caps Capabilities) Frame {
	if width < 20 {
		width = 20
	}
	now := snapshot.ObservedAt
	if now.IsZero() {
		now = time.Now()
	}
	statusSymbol := "*"
	if caps.Unicode {
		statusSymbol = "●"
	}
	header := PadBetween("INOCULUM", "ONLINE "+statusSymbol, width)
	frame := Frame{}
	frame.Add(width, Span{Text: header, Tone: ToneHeading})
	frame.Blank(width)

	address := "listening"
	if len(snapshot.Addresses) > 0 {
		address = strings.Join(snapshot.Addresses, ", ")
	}
	frame.Add(width, Span{Text: "Coordinator   ", Tone: ToneMuted}, Span{Text: address})
	frame.Add(width, Span{Text: "Uptime        ", Tone: ToneMuted}, Span{Text: CompactDuration(snapshot.Uptime)})
	if snapshot.Fingerprint != "" && (caps.Verbose || len(snapshot.Workers) == 0) {
		frame.Add(width, Span{Text: "Fingerprint   ", Tone: ToneMuted})
		for _, line := range Wrap(snapshot.Fingerprint, max(12, width-2)) {
			frame.Add(width, Span{Text: "  " + line, Tone: ToneMuted})
		}
	}
	frame.Blank(width)

	if width < 58 {
		frame.Add(width, Span{Text: fmt.Sprintf("Tasks  %d queued  %d running", snapshot.Tasks.Queued, snapshot.Tasks.Running)})
		frame.Add(width, Span{Text: fmt.Sprintf("       %d done    %d failed", snapshot.Tasks.Completed, snapshot.Tasks.Failed)})
	} else {
		frame.Add(width, Span{Text: "TASKS", Tone: ToneHeading})
		frame.Add(width, Span{Text: fmt.Sprintf("Queued  %-6d Running  %-6d Completed  %-6d Failed  %d",
			snapshot.Tasks.Queued, snapshot.Tasks.Running, snapshot.Tasks.Completed, snapshot.Tasks.Failed)})
	}
	frame.Blank(width)

	frame.Add(width, Span{Text: "WORKERS", Tone: ToneHeading})
	if len(snapshot.Workers) == 0 {
		frame.Add(width, Span{Text: "No workers connected.", Tone: ToneWaiting})
		if width >= 48 {
			frame.Add(width, Span{Text: "Start one with: inoculum worker --coordinator <address>"})
		}
	} else {
		for _, worker := range snapshot.Workers {
			state := "idle"
			tone := ToneHealthy
			if worker.Active > 0 {
				state = fmt.Sprintf("running  %d active", worker.Active)
			}
			if now.Sub(worker.LastActivity) > 3*time.Second {
				state = "not responding"
				tone = ToneWaiting
			}
			if width < 58 {
				frame.Add(width, Span{Text: statusSymbol + " " + ShortID(worker.WorkerID, 18, caps.Unicode) + "  ", Tone: tone}, Span{Text: state})
			} else {
				left := statusSymbol + " " + ShortID(worker.WorkerID, 24, caps.Unicode)
				right := fmt.Sprintf("%-20s seen %s", state, SeenAgo(now, worker.LastActivity))
				frame.Add(width, Span{Text: PadBetween(left, right, width), Tone: tone})
			}
		}
	}

	if snapshot.CurrentJob != nil {
		job := snapshot.CurrentJob
		frame.Blank(width)
		frame.Add(width, Span{Text: PadBetween("CURRENT JOB", ShortID(job.JobID, 20, caps.Unicode), width), Tone: ToneHeading})
		barWidth := min(max(width-14, 10), 40)
		done := job.Completed + job.Failed
		frame.Add(width, Span{Text: fmt.Sprintf("%s  %d / %d", ProgressBar(done, job.Total, barWidth, caps.Unicode), done, job.Total)})
		for _, task := range activeAndRecent(job.Tasks, max(1, height-len(frame.Lines)-2)) {
			state := task.State
			tone := ToneNormal
			if state == "completed" {
				state, tone = successWord(caps), ToneHealthy
			} else if state == "failed" {
				state, tone = failureWord(caps), ToneFailure
			}
			frame.Add(width, Span{Text: fmt.Sprintf("%-18s %-18s %-10s %s",
				ShortID(task.WorkerID, 16, caps.Unicode), ShortID(task.TaskID, 16, caps.Unicode), state, CompactDuration(task.Duration)), Tone: tone})
		}
	} else if snapshot.Tasks.Total == 0 {
		frame.Blank(width)
		frame.Add(width, Span{Text: "No jobs yet.", Tone: ToneMuted})
	}

	footer := Line{{Text: Truncate("q quit    ? help", width), Tone: ToneMuted}}
	frame.Blank(width)
	frame.Lines = append(frame.Lines, footer)
	return frame.FitHeight(height, footer)
}

func WorkerFrame(snapshot monitor.WorkerSnapshot, width, height int, caps Capabilities) Frame {
	if width < 20 {
		width = 20
	}
	now := snapshot.ObservedAt
	if now.IsZero() {
		now = time.Now()
	}
	label, symbol, tone := workerConnection(snapshot.Connection, caps)
	frame := Frame{}
	frame.Add(width, Span{Text: PadBetween("INOCULUM WORKER", label+" "+symbol, width), Tone: ToneHeading})
	frame.Blank(width)
	frame.Add(width, Span{Text: "Worker        ", Tone: ToneMuted}, Span{Text: snapshot.WorkerID})
	frame.Add(width, Span{Text: "Coordinator   ", Tone: ToneMuted}, Span{Text: snapshot.Coordinator})
	frame.Add(width, Span{Text: "Concurrency   ", Tone: ToneMuted}, Span{Text: fmt.Sprint(snapshot.Concurrency)})
	frame.Blank(width)
	frame.Add(width, Span{Text: "EXECUTION", Tone: ToneHeading})
	frame.Add(width, Span{Text: fmt.Sprintf("Active  %d / %d    Completed  %d    Failed  %d",
		len(snapshot.ActiveTasks), snapshot.Concurrency, snapshot.Completed, snapshot.Failed)})

	if snapshot.Connection == monitor.ConnectionUnavailable || snapshot.Connection == monitor.ConnectionAuthFailed || snapshot.Connection == monitor.ConnectionUntrusted || snapshot.Connection == monitor.ConnectionIdentity {
		frame.Blank(width)
		switch snapshot.Connection {
		case monitor.ConnectionAuthFailed:
			frame.Add(width, Span{Text: "Authentication failed.", Tone: ToneFailure})
			frame.Add(width, Span{Text: "The coordinator rejected the token."})
			frame.Add(width, Span{Text: "Check INOCULUM_TOKEN or -token."})
		case monitor.ConnectionUntrusted:
			frame.Add(width, Span{Text: "No coordinator identity is trusted yet.", Tone: ToneFailure})
			frame.Add(width, Span{Text: "Copy the coordinator fingerprint and reconnect with:"})
			frame.Add(width, Span{Text: "--coordinator-fingerprint <fingerprint>"})
		case monitor.ConnectionIdentity:
			frame.Add(width, Span{Text: "The coordinator identity does not match the trusted fingerprint.", Tone: ToneFailure})
			frame.Add(width, Span{Text: "Connection refused. The stored trust was not bypassed."})
		default:
			frame.Add(width, Span{Text: "Coordinator unavailable.", Tone: tone})
		}
		if !snapshot.UnavailableSince.IsZero() {
			frame.Add(width, Span{Text: "Disconnected  ", Tone: ToneMuted}, Span{Text: CompactDuration(now.Sub(snapshot.UnavailableSince))})
		}
		if !snapshot.RetryAt.IsZero() {
			frame.Add(width, Span{Text: fmt.Sprintf("Retrying in %s (attempt %d)", CompactDuration(snapshot.RetryAt.Sub(now)), snapshot.RetryAttempt), Tone: ToneWaiting})
		}
		if caps.Verbose && snapshot.LastError != "" {
			frame.Add(width, Span{Text: "Detail        ", Tone: ToneMuted}, Span{Text: snapshot.LastError})
		}
	}

	frame.Blank(width)
	frame.Add(width, Span{Text: "ACTIVE TASKS", Tone: ToneHeading})
	if len(snapshot.ActiveTasks) == 0 {
		if snapshot.Connection == monitor.ConnectionConnected {
			frame.Add(width, Span{Text: "Connected and waiting for work.", Tone: ToneMuted})
		} else {
			frame.Add(width, Span{Text: "No active tasks.", Tone: ToneMuted})
		}
	} else {
		for _, task := range snapshot.ActiveTasks {
			elapsed := now.Sub(task.StartedAt)
			frame.Add(width, Span{Text: fmt.Sprintf("%-22s running  %s", ShortID(task.TaskID, 20, caps.Unicode), CompactDuration(elapsed))})
		}
	}

	footer := Line{{Text: Truncate("q quit    ? help", width), Tone: ToneMuted}}
	frame.Blank(width)
	frame.Lines = append(frame.Lines, footer)
	return frame.FitHeight(height, footer)
}

func SubmitFrame(snapshot monitor.SubmitSnapshot, width, height int, caps Capabilities) Frame {
	if width < 20 {
		width = 20
	}
	frame := Frame{}
	frame.Add(width, Span{Text: "INOCULUM SUBMIT", Tone: ToneHeading})
	frame.Blank(width)
	if !snapshot.Submitted {
		frame.Add(width, Span{Text: "Submitting tasks...", Tone: ToneWaiting})
		return frame.FitHeight(height, nil)
	}
	job := snapshot.Job
	frame.Add(width, Span{Text: "Job        ", Tone: ToneMuted}, Span{Text: ShortID(job.JobID, max(8, width-11), caps.Unicode)})
	frame.Add(width, Span{Text: "State      ", Tone: ToneMuted}, Span{Text: titleState(job.State)})
	frame.Add(width, Span{Text: "Elapsed    ", Tone: ToneMuted}, Span{Text: CompactDuration(job.Elapsed)})
	frame.Blank(width)
	barWidth := min(max(width-14, 10), 40)
	done := job.Completed + job.Failed
	frame.Add(width, Span{Text: fmt.Sprintf("%s  %d / %d", ProgressBar(done, job.Total, barWidth, caps.Unicode), done, job.Total)})
	frame.Blank(width)
	frame.Add(width, Span{Text: fmt.Sprintf("Queued     %d", job.Queued)})
	frame.Add(width, Span{Text: fmt.Sprintf("Running    %d", job.Running)})
	frame.Add(width, Span{Text: fmt.Sprintf("Completed  %d", job.Completed), Tone: ToneHealthy})
	frame.Add(width, Span{Text: fmt.Sprintf("Failed     %d", job.Failed), Tone: failureTone(job.Failed)})

	if len(job.Workers) > 0 && width >= 44 {
		frame.Blank(width)
		frame.Add(width, Span{Text: "WORKERS", Tone: ToneHeading})
		for _, worker := range job.Workers {
			frame.Add(width, Span{Text: fmt.Sprintf("%-22s %d completed  %d active",
				ShortID(worker.WorkerID, 20, caps.Unicode), worker.Completed, worker.Active)})
		}
	}
	footer := Line{{Text: Truncate("Ctrl+C stops waiting; the coordinator job continues.", width), Tone: ToneMuted}}
	frame.Blank(width)
	frame.Lines = append(frame.Lines, footer)
	return frame.FitHeight(height, footer)
}

func activeAndRecent(tasks []monitor.TaskProgress, limit int) []monitor.TaskProgress {
	copyTasks := append([]monitor.TaskProgress(nil), tasks...)
	sort.SliceStable(copyTasks, func(i, j int) bool {
		return taskPriority(copyTasks[i].State) < taskPriority(copyTasks[j].State)
	})
	if len(copyTasks) > limit {
		copyTasks = copyTasks[:limit]
	}
	return copyTasks
}

func taskPriority(state string) int {
	switch state {
	case "leased":
		return 0
	case "failed":
		return 1
	case "completed":
		return 2
	default:
		return 3
	}
}

func workerConnection(state monitor.ConnectionState, caps Capabilities) (string, string, Tone) {
	switch state {
	case monitor.ConnectionConnected:
		if caps.Unicode {
			return "CONNECTED", "●", ToneHealthy
		}
		return "CONNECTED", "*", ToneHealthy
	case monitor.ConnectionUnavailable:
		if caps.Unicode {
			return "UNAVAILABLE", "○", ToneWaiting
		}
		return "UNAVAILABLE", "!", ToneWaiting
	case monitor.ConnectionAuthFailed:
		return "AUTH FAILED", "X", ToneFailure
	case monitor.ConnectionUntrusted:
		return "NO TRUST", "X", ToneFailure
	case monitor.ConnectionIdentity:
		return "IDENTITY ERROR", "X", ToneFailure
	case monitor.ConnectionStopping:
		return "STOPPING", "-", ToneWaiting
	default:
		return "CONNECTING", "-", ToneWaiting
	}
}

func successWord(caps Capabilities) string {
	if caps.Unicode {
		return "✓"
	}
	return "OK"
}

func failureWord(caps Capabilities) string {
	if caps.Unicode {
		return "✗"
	}
	return "X"
}

func failureTone(count int) Tone {
	if count > 0 {
		return ToneFailure
	}
	return ToneNormal
}

func titleState(state string) string {
	if state == "" {
		return "Unknown"
	}
	runes := []rune(state)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}
