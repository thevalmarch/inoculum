package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thevalmarch/inoculum/internal/monitor"
	"github.com/thevalmarch/inoculum/internal/types"
)

type manifestResults struct {
	JobID string               `json:"job_id"`
	State string               `json:"state"`
	Tasks []manifestTaskResult `json:"tasks"`
}

type manifestTaskResult struct {
	Key      string                 `json:"key"`
	State    string                 `json:"state"`
	Attempts int                    `json:"attempts"`
	Worker   string                 `json:"worker,omitempty"`
	Output   *types.HTTPProbeOutput `json:"output,omitempty"`
	Error    *manifestTaskError     `json:"error,omitempty"`
}

type manifestTaskError struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

func validateResultOutputPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("path is required")
	}
	cleaned := filepath.Clean(path)
	if info, err := os.Stat(cleaned); err == nil && info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(cleaned)
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("output directory %s is unavailable: %w", parent, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("output parent %s is not a directory", parent)
	}
	return nil
}

func buildManifestResults(job monitor.JobProgress) (manifestResults, error) {
	document := manifestResults{JobID: job.JobID, State: job.State, Tasks: make([]manifestTaskResult, 0, len(job.Tasks))}
	for _, task := range job.Tasks {
		result := manifestTaskResult{
			Key: task.Key, State: task.State, Attempts: task.Attempt, Worker: task.WorkerID,
		}
		if task.Output != "" {
			var probe types.HTTPProbeOutput
			if err := json.Unmarshal([]byte(task.Output), &probe); err != nil {
				return manifestResults{}, fmt.Errorf("decode structured output for task %q: %w", task.Key, err)
			}
			if probe.ErrorCategory != "" || probe.ErrorMessage != "" {
				result.Error = &manifestTaskError{Category: probe.ErrorCategory, Message: probe.ErrorMessage}
				probe.ErrorCategory = ""
				probe.ErrorMessage = ""
			}
			result.Output = &probe
		}
		if task.Error != "" && result.Error == nil {
			result.Error = &manifestTaskError{Category: "execution", Message: task.Error}
		}
		document.Tasks = append(document.Tasks, result)
	}
	return document, nil
}

func writeManifestResults(path string, job monitor.JobProgress) error {
	if err := validateResultOutputPath(path); err != nil {
		return err
	}
	document, err := buildManifestResults(job)
	if err != nil {
		return err
	}
	contents, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode results: %w", err)
	}
	contents = append(contents, '\n')

	cleaned := filepath.Clean(path)
	temporary, err := os.CreateTemp(filepath.Dir(cleaned), "."+filepath.Base(cleaned)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary results file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary results file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary results file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary results file: %w", err)
	}
	if err := os.Rename(temporaryPath, cleaned); err != nil {
		return fmt.Errorf("replace results file: %w", err)
	}
	return nil
}
