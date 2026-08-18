package presentation

import (
	"strings"
	"testing"
	"time"

	"github.com/thevalmarch/inoculum/internal/monitor"
)

func TestCoordinatorFrameLayoutsStayWithinTerminal(t *testing.T) {
	now := time.Now()
	snapshot := monitor.CoordinatorSnapshot{
		ObservedAt:  now,
		Online:      true,
		Addresses:   []string{"192.0.2.5:8080"},
		Fingerprint: "55:17:37:57:20:fa:46:ab:b6:89:0a:be:13:86:8f:39",
		Uptime:      18 * time.Minute,
		Tasks:       monitor.TaskCounts{Queued: 2, Running: 2, Completed: 8, Total: 12},
		Workers: []monitor.WorkerSummary{
			{WorkerID: "mac-worker", LastActivity: now, Active: 1},
			{WorkerID: "linux-worker", LastActivity: now, Active: 1},
		},
		CurrentJob: &monitor.JobProgress{JobID: "pull-job-long-8a91", Total: 10, Completed: 8, Running: 2, Tasks: []monitor.TaskProgress{
			{TaskID: "task-17", WorkerID: "mac-worker", State: "completed", Duration: 2 * time.Second},
			{TaskID: "task-18", WorkerID: "linux-worker", State: "leased", Duration: time.Second},
		}},
	}
	for _, size := range []struct{ width, height int }{{120, 30}, {80, 24}, {58, 18}, {40, 12}} {
		frame := CoordinatorFrame(snapshot, size.width, size.height, Capabilities{Unicode: true, Color: true})
		assertFrameFits(t, frame, size.width, size.height)
	}
}

func TestCoordinatorEmptyStateAndASCII(t *testing.T) {
	frame := CoordinatorFrame(monitor.CoordinatorSnapshot{ObservedAt: time.Now(), Addresses: []string{":8080"}, Fingerprint: "AA:BB:CC"}, 80, 24, Capabilities{Unicode: false})
	text := strings.Join(frame.PlainLines(), "\n")
	for _, required := range []string{"Fingerprint", "AA:BB:CC", "No workers connected.", "inoculum worker --coordinator", "No jobs yet."} {
		if !strings.Contains(text, required) {
			t.Fatalf("frame missing %q:\n%s", required, text)
		}
	}
	for _, forbidden := range []string{"●", "█", "░", "✓", "…"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("ASCII frame contains %q:\n%s", forbidden, text)
		}
	}
}

func TestWorkerReconnectAndSecurityStates(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		state monitor.ConnectionState
		want  string
	}{
		{monitor.ConnectionUnavailable, "Coordinator unavailable."},
		{monitor.ConnectionAuthFailed, "Authentication failed."},
		{monitor.ConnectionUntrusted, "--coordinator-fingerprint <fingerprint>"},
		{monitor.ConnectionIdentity, "stored trust was not bypassed"},
	} {
		frame := WorkerFrame(monitor.WorkerSnapshot{
			ObservedAt: now, WorkerID: "linux-worker", Coordinator: "192.0.2.5:8080",
			Connection: test.state, UnavailableSince: now.Add(-12 * time.Second), RetryAt: now.Add(4 * time.Second), RetryAttempt: 3, Concurrency: 4,
		}, 80, 24, Capabilities{Unicode: true})
		text := strings.Join(frame.PlainLines(), "\n")
		if !strings.Contains(text, test.want) {
			t.Fatalf("state %s missing %q:\n%s", test.state, test.want, text)
		}
	}
}

func TestSubmitFrameProgressAndNarrowTerminal(t *testing.T) {
	frame := SubmitFrame(monitor.SubmitSnapshot{Submitted: true, Job: monitor.JobProgress{
		JobID: "pull-job-123", State: "running", Total: 20, Queued: 4, Running: 2, Completed: 14,
		Workers: []monitor.WorkerContribution{{WorkerID: "mac-worker", Completed: 7, Active: 1}},
	}}, 40, 12, Capabilities{Unicode: false})
	assertFrameFits(t, frame, 40, 12)
	text := strings.Join(frame.PlainLines(), "\n")
	if !strings.Contains(text, "14 / 20") || strings.Contains(text, "█") {
		t.Fatalf("unexpected submit frame:\n%s", text)
	}
}

func assertFrameFits(t *testing.T, frame Frame, width, height int) {
	t.Helper()
	if len(frame.Lines) > height {
		t.Fatalf("frame height %d exceeds %d", len(frame.Lines), height)
	}
	for lineNumber, line := range frame.PlainLines() {
		if len([]rune(line)) > width {
			t.Fatalf("line %d width %d exceeds %d: %q", lineNumber, len([]rune(line)), width, line)
		}
	}
}
