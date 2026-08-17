package workload

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifestTestFile(t *testing.T, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validManifest() Manifest {
	return Manifest{Version: 1, Type: HTTPProbeType, Tasks: []Task{{Key: "homepage", Input: "https://example.com/"}}}
}

func TestLoadValidManifestPreservesOrder(t *testing.T) {
	manifest := validManifest()
	manifest.Tasks = []Task{
		{Key: "third", Input: "https://example.com/3"},
		{Key: "first", Input: "https://example.com/1"},
		{Key: "second", Input: "https://example.com/2"},
	}
	loaded, err := LoadManifest(writeManifestTestFile(t, manifest))
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"third", "first", "second"} {
		if loaded.Tasks[index].Key != want {
			t.Fatalf("task %d key = %q, want %q", index, loaded.Tasks[index].Key, want)
		}
	}
}

func TestManifestValidationFailures(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		want     string
	}{
		{name: "unsupported version", manifest: Manifest{Version: 2, Type: HTTPProbeType, Tasks: validManifest().Tasks}, want: "unsupported manifest version"},
		{name: "missing type", manifest: Manifest{Version: 1, Tasks: validManifest().Tasks}, want: "manifest type is required"},
		{name: "unsupported type", manifest: Manifest{Version: 1, Type: "command", Tasks: validManifest().Tasks}, want: "unsupported manifest task type"},
		{name: "zero tasks", manifest: Manifest{Version: 1, Type: HTTPProbeType}, want: "at least one task"},
		{name: "empty key", manifest: Manifest{Version: 1, Type: HTTPProbeType, Tasks: []Task{{Input: "https://example.com"}}}, want: "empty key"},
		{name: "duplicate key", manifest: Manifest{Version: 1, Type: HTTPProbeType, Tasks: []Task{{Key: "same", Input: "https://example.com/1"}, {Key: "same", Input: "https://example.com/2"}}}, want: "duplicate task key"},
		{name: "empty input", manifest: Manifest{Version: 1, Type: HTTPProbeType, Tasks: []Task{{Key: "empty"}}}, want: "empty input"},
		{name: "oversized input", manifest: Manifest{Version: 1, Type: HTTPProbeType, Tasks: []Task{{Key: "large", Input: strings.Repeat("x", MaxInputBytes+1)}}}, want: "input exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateManifest(test.manifest); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateManifest() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestManifestTaskLimit(t *testing.T) {
	manifest := Manifest{Version: 1, Type: HTTPProbeType, Tasks: make([]Task, MaxTasks)}
	for index := range manifest.Tasks {
		manifest.Tasks[index] = Task{Key: fmt.Sprintf("task-%04d", index), Input: "https://example.com"}
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("1000-task manifest rejected: %v", err)
	}
	manifest.Tasks = append(manifest.Tasks, Task{Key: "too-many", Input: "https://example.com"})
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "maximum is 1000") {
		t.Fatalf("oversized manifest error = %v", err)
	}
}

func TestLoadManifestRejectsUnknownFieldsAndInvalidJSON(t *testing.T) {
	directory := t.TempDir()
	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{name: "unknown top-level", contents: `{"version":1,"type":"http_probe","tasks":[{"key":"a","input":"https://example.com"}],"extra":true}`, want: "unknown field"},
		{name: "unknown task field", contents: `{"version":1,"type":"http_probe","tasks":[{"key":"a","input":"https://example.com","extra":true}]}`, want: "unknown field"},
		{name: "invalid JSON", contents: `{"version":`, want: "invalid manifest JSON"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(directory, fmt.Sprintf("manifest-%d.json", index))
			if err := os.WriteFile(path, []byte(test.contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadManifest() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadManifestRejectsOversizedDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", MaxManifestBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("LoadManifest() error = %v", err)
	}
}
