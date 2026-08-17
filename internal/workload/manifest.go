// Package workload defines the deliberately small V1 batch workload surface.
package workload

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ManifestVersion  = 1
	HTTPProbeType    = "http_probe"
	MaxManifestBytes = 5 * 1024 * 1024
	MaxTasks         = 1000
	MaxKeyBytes      = 128
	MaxInputBytes    = 4096
)

type Manifest struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Tasks   []Task `json:"tasks"`
}

type Task struct {
	Key   string `json:"key"`
	Input string `json:"input"`
}

// LoadManifest reads and validates one bounded JSON manifest. Unknown fields
// are rejected so misspelled workload configuration cannot be ignored.
func LoadManifest(path string) (Manifest, error) {
	if strings.TrimSpace(path) == "" {
		return Manifest{}, fmt.Errorf("manifest path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(file, MaxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	if len(contents) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("manifest exceeds the %d-byte limit", MaxManifestBytes)
	}

	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("invalid manifest JSON: %w", err)
	}
	if err := ensureEndOfJSON(decoder); err != nil {
		return Manifest{}, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("unsupported manifest version %d; supported version is %d", manifest.Version, ManifestVersion)
	}
	if manifest.Type == "" {
		return fmt.Errorf("manifest type is required")
	}
	if manifest.Type != HTTPProbeType {
		return fmt.Errorf("unsupported manifest task type %q; supported type is %q", manifest.Type, HTTPProbeType)
	}
	return ValidateTasks(manifest.Tasks)
}

func ValidateTasks(tasks []Task) error {
	if len(tasks) == 0 {
		return fmt.Errorf("manifest must contain at least one task")
	}
	if len(tasks) > MaxTasks {
		return fmt.Errorf("manifest contains %d tasks; maximum is %d", len(tasks), MaxTasks)
	}

	keys := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if strings.TrimSpace(task.Key) == "" {
			return fmt.Errorf("manifest task %d has an empty key", index)
		}
		if len(task.Key) > MaxKeyBytes {
			return fmt.Errorf("manifest task %q key exceeds the %d-byte limit", task.Key, MaxKeyBytes)
		}
		if _, exists := keys[task.Key]; exists {
			return fmt.Errorf("manifest contains duplicate task key %q", task.Key)
		}
		keys[task.Key] = struct{}{}
		if len(task.Input) == 0 {
			return fmt.Errorf("manifest task %q has an empty input", task.Key)
		}
		if len(task.Input) > MaxInputBytes {
			return fmt.Errorf("manifest task %q input exceeds the %d-byte limit", task.Key, MaxInputBytes)
		}
	}
	return nil
}

func ensureEndOfJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("invalid manifest JSON: %w", err)
	}
	return fmt.Errorf("invalid manifest JSON: multiple JSON values are not allowed")
}
