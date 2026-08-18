package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thevalmarch/inoculum/internal/monitor"
	"github.com/thevalmarch/inoculum/internal/presentation/plain"
)

func TestManifestResultsPreserveOrderAndStructuredFields(t *testing.T) {
	job := monitor.JobProgress{
		JobID: "pull-job-1", State: "failed", Total: 2, Completed: 1, Failed: 1,
		Tasks: []monitor.TaskProgress{
			{TaskID: "internal-9", Key: "homepage", State: "completed", Attempt: 1, WorkerID: "mac-worker", Output: `{"status_code":200,"final_url":"https://example.com/","elapsed_ms":12}`},
			{TaskID: "internal-2", Key: "docs", State: "failed", Attempt: 3, WorkerID: "linux-worker", Output: `{"elapsed_ms":10000,"error_category":"timeout","error_message":"request timed out"}`, Error: "request timed out"},
		},
	}
	document, err := buildManifestResults(job)
	if err != nil {
		t.Fatal(err)
	}
	if document.JobID != job.JobID || document.State != "failed" || len(document.Tasks) != 2 {
		t.Fatalf("document = %#v", document)
	}
	if document.Tasks[0].Key != "homepage" || document.Tasks[1].Key != "docs" {
		t.Fatalf("task ordering = %#v", document.Tasks)
	}
	if document.Tasks[0].Attempts != 1 || document.Tasks[0].Worker != "mac-worker" || document.Tasks[0].Output == nil || document.Tasks[0].Output.StatusCode != 200 {
		t.Fatalf("success result = %#v", document.Tasks[0])
	}
	if document.Tasks[1].Attempts != 3 || document.Tasks[1].Worker != "linux-worker" || document.Tasks[1].Error == nil || document.Tasks[1].Error.Category != "timeout" {
		t.Fatalf("failure result = %#v", document.Tasks[1])
	}
}

func TestManifestResultJSONIsDeterministicAndContainsNoSecrets(t *testing.T) {
	job := monitor.JobProgress{
		JobID: "pull-job-2", State: "completed", Total: 1, Completed: 1,
		Tasks: []monitor.TaskProgress{{Key: "one", State: "completed", Attempt: 2, WorkerID: "worker-a", Output: `{"status_code":204,"elapsed_ms":4}`}},
	}
	first := filepath.Join(t.TempDir(), "first.json")
	second := filepath.Join(t.TempDir(), "second.json")
	if err := writeManifestResults(first, job); err != nil {
		t.Fatal(err)
	}
	if err := writeManifestResults(second, job); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("result JSON differs:\n%s\n%s", a, b)
	}
	if strings.Contains(strings.ToLower(string(a)), "token") || strings.Contains(strings.ToLower(string(a)), "authorization") {
		t.Fatalf("result JSON contains a secret-bearing field: %s", a)
	}
	var decoded map[string]any
	if err := json.Unmarshal(a, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestManifestResultOutputPathErrors(t *testing.T) {
	directory := t.TempDir()
	if err := validateResultOutputPath(directory); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("directory error = %v", err)
	}
	missingParent := filepath.Join(t.TempDir(), "missing", "results.json")
	if err := validateResultOutputPath(missingParent); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing parent error = %v", err)
	}
	if err := writeManifestResults(directory, monitor.JobProgress{}); err == nil {
		t.Fatal("writeManifestResults accepted a directory")
	}
}

func TestManifestResultsRejectMalformedStructuredWorkerOutput(t *testing.T) {
	_, err := buildManifestResults(monitor.JobProgress{Tasks: []monitor.TaskProgress{{Key: "bad", Output: "not-json"}}})
	if err == nil || !strings.Contains(err.Error(), "decode structured output") {
		t.Fatalf("error = %v", err)
	}
}

func TestManifestSubmitSummaryDoesNotFloodTerminalWithTaskFailures(t *testing.T) {
	job := monitor.JobProgress{JobID: "job-many", State: "failed", Total: 500, Failed: 500}
	for index := 0; index < 500; index++ {
		job.Tasks = append(job.Tasks, monitor.TaskProgress{TaskID: "task", State: "failed", Attempt: 3, Error: "failed"})
	}
	var output bytes.Buffer
	plain.ManifestSubmitSummary(&output, job, false)
	if strings.Contains(output.String(), "attempts exhausted") || strings.Count(output.String(), "task") > 1 {
		t.Fatalf("manifest summary flooded task details: %q", output.String())
	}
}
