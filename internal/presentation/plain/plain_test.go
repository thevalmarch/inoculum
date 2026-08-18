package plain

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/thevalmarch/inoculum/internal/monitor"
)

func TestPlainOutputHasNoTerminalControlSequences(t *testing.T) {
	var output bytes.Buffer
	job := monitor.JobProgress{
		JobID: "job-1", State: "completed", Total: 1, Completed: 1, Elapsed: time.Second,
		Tasks: []monitor.TaskProgress{{TaskID: "task-1", State: "completed", WorkerID: "worker-a", Attempt: 1}},
	}
	SubmitProgress(&output, job)
	SubmitSummary(&output, job, true, false)
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("plain output contains ANSI sequence: %q", output.String())
	}
	for _, expected := range []string{"job=job-1", "OK Job completed", "task-1 state=completed"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("plain output missing %q: %s", expected, output.String())
		}
	}
}

func TestPlainOutputEscapesWorkerControlledText(t *testing.T) {
	var output bytes.Buffer
	SubmitSummary(&output, monitor.JobProgress{
		JobID: "job-1", State: "failed", Total: 1, Failed: 1,
		Tasks: []monitor.TaskProgress{{
			TaskID: "task-1\nforged", State: "failed",
			Error: "failure\r\nforged log line", Attempt: 1,
		}},
	}, false, false)
	got := output.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "failure\r\nforged") || strings.Contains(got, "task-1\nforged") {
		t.Fatalf("plain output retained injected control characters: %q", got)
	}
	for _, expected := range []string{`task-1\nforged`, `failure\r\nforged log line`} {
		if !strings.Contains(got, expected) {
			t.Fatalf("plain output = %q, missing escaped %q", got, expected)
		}
	}
}

func TestFailureSummaryIsActionable(t *testing.T) {
	var output bytes.Buffer
	SubmitSummary(&output, monitor.JobProgress{
		JobID: "job-1", State: "failed", Total: 1, Failed: 1,
		Tasks: []monitor.TaskProgress{{TaskID: "task-1", State: "failed", Attempt: 3, Error: "unknown task type"}},
	}, false, false)
	text := output.String()
	for _, expected := range []string{"X Job failed", "unknown task type", "3 attempts exhausted"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("failure summary missing %q: %s", expected, text)
		}
	}
}

func TestCoordinatorStartupAlwaysShowsTrustFingerprint(t *testing.T) {
	var output bytes.Buffer
	CoordinatorStarted(&output, monitor.CoordinatorSnapshot{
		Addresses:   []string{"192.0.2.5:8080"},
		Fingerprint: "AA:BB:CC",
	}, false)
	text := output.String()
	for _, expected := range []string{"coordinator online address=192.0.2.5:8080", "coordinator fingerprint=AA:BB:CC"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("coordinator startup missing %q: %s", expected, text)
		}
	}
}
