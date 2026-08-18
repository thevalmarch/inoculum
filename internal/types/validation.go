package types

import "fmt"

const (
	MaxWorkerIDBytes     = 64
	MaxTaskIDBytes       = 256
	MaxLeaseIDBytes      = 128
	MaxResultOutputBytes = 64 * 1024
	MaxResultErrorBytes  = 4 * 1024
)

// ValidateWorkerID keeps the operational worker label safe for logs and
// terminal output. Worker IDs are labels, not authentication principals.
func ValidateWorkerID(workerID string) error {
	if workerID == "" {
		return fmt.Errorf("worker ID is required")
	}
	if len(workerID) > MaxWorkerIDBytes {
		return fmt.Errorf("worker ID exceeds the %d-byte limit", MaxWorkerIDBytes)
	}
	for _, character := range workerID {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("worker ID may contain only letters, numbers, '.', '_' and '-'")
	}
	return nil
}

func ValidateTaskID(taskID string) error {
	return validateSafeIdentifier("task ID", taskID, MaxTaskIDBytes)
}

func ValidateLeaseID(leaseID string) error {
	return validateSafeIdentifier("lease ID", leaseID, MaxLeaseIDBytes)
}

func ValidateResult(result Result) error {
	if len(result.TaskID) > MaxTaskIDBytes {
		return fmt.Errorf("result task ID exceeds the %d-byte limit", MaxTaskIDBytes)
	}
	if len(result.Output) > MaxResultOutputBytes {
		return fmt.Errorf("result output exceeds the %d-byte limit", MaxResultOutputBytes)
	}
	if len(result.Error) > MaxResultErrorBytes {
		return fmt.Errorf("result error exceeds the %d-byte limit", MaxResultErrorBytes)
	}
	return nil
}

func validateSafeIdentifier(name, value string, limit int) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > limit {
		return fmt.Errorf("%s exceeds the %d-byte limit", name, limit)
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return fmt.Errorf("%s may contain only letters, numbers, '.', '_' and '-'", name)
	}
	return nil
}
