package types

import "time"

// Result contains the output produced by a worker for a single task.
type Result struct {
	TaskID      string        `json:"task_id"`
	Output      string        `json:"output"`
	Duration    time.Duration `json:"duration_ns"` // processing duration in nanoseconds
	DurationStr string        `json:"duration"`    // human-readable duration
	Error       string        `json:"error,omitempty"`
}

// HTTPProbeOutput is encoded into Result.Output by the fixed http_probe
// executor. Keeping it typed makes manifest result export deterministic.
type HTTPProbeOutput struct {
	StatusCode            int    `json:"status_code,omitempty"`
	FinalURL              string `json:"final_url,omitempty"`
	ElapsedMilliseconds   int64  `json:"elapsed_ms"`
	DeclaredContentLength *int64 `json:"declared_content_length,omitempty"`
	TLSCertificateExpiry  string `json:"tls_certificate_expiry,omitempty"`
	ErrorCategory         string `json:"error_category,omitempty"`
	ErrorMessage          string `json:"error_message,omitempty"`
}

// PullStatus is an explicit protocol outcome for the pull-based execution path.
type PullStatus string

const (
	PullTaskAvailable PullStatus = "task_available"
	PullNoTask        PullStatus = "no_task_available"
	PullLeaseRenewed  PullStatus = "lease_renewed"
	PullTaskCompleted PullStatus = "task_completed"
	PullTaskRequeued  PullStatus = "task_requeued"
	PullTaskFailed    PullStatus = "task_failed"
	PullStaleLease    PullStatus = "stale_lease"
	PullRejected      PullStatus = "rejected"
)

type PullTask struct {
	TaskID         string    `json:"task_id"`
	JobID          string    `json:"job_id"`
	Type           string    `json:"type"`
	Input          string    `json:"input"`
	LeaseID        string    `json:"lease_id"`
	Attempt        int       `json:"attempt"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}

type PullClaimRequest struct {
	WorkerID string `json:"worker_id"`
}

type PullClaimResponse struct {
	Status PullStatus `json:"status"`
	Task   *PullTask  `json:"task,omitempty"`
}

type PullRenewRequest struct {
	WorkerID string `json:"worker_id"`
	TaskID   string `json:"task_id"`
	LeaseID  string `json:"lease_id"`
}

type PullRenewResponse struct {
	Status         PullStatus `json:"status"`
	LeaseExpiresAt time.Time  `json:"lease_expires_at,omitempty"`
	Message        string     `json:"message,omitempty"`
}

type PullResultRequest struct {
	WorkerID string `json:"worker_id"`
	TaskID   string `json:"task_id"`
	LeaseID  string `json:"lease_id"`
	Result   Result `json:"result"`
}

type PullResultResponse struct {
	Status  PullStatus `json:"status"`
	Message string     `json:"message,omitempty"`
}

type PullSubmitRequest struct {
	TaskType string           `json:"task_type"`
	Inputs   []string         `json:"inputs,omitempty"`
	Tasks    []PullSubmitTask `json:"tasks,omitempty"`
}

// PullSubmitTask carries a user correlation key only as far as the
// coordinator. Workers continue to receive the existing type + input payload.
type PullSubmitTask struct {
	Key   string `json:"key"`
	Input string `json:"input"`
}

type PullSubmitResponse struct {
	JobID   string   `json:"job_id"`
	TaskIDs []string `json:"task_ids"`
}

type PullJobState string

const (
	PullJobQueued    PullJobState = "queued"
	PullJobRunning   PullJobState = "running"
	PullJobCompleted PullJobState = "completed"
	PullJobFailed    PullJobState = "failed"
)

type PullJobTask struct {
	TaskID   string  `json:"task_id"`
	Key      string  `json:"key,omitempty"`
	State    string  `json:"state"`
	Attempts int     `json:"attempts"`
	WorkerID string  `json:"worker_id,omitempty"`
	Result   *Result `json:"result,omitempty"`
}

type PullJobResponse struct {
	JobID string        `json:"job_id"`
	State PullJobState  `json:"state"`
	Tasks []PullJobTask `json:"tasks"`
}

type PullWorkerStatus struct {
	WorkerID     string    `json:"worker_id"`
	LastActivity time.Time `json:"last_activity"`
	ActiveLeases int       `json:"active_leases"`
}

type CoordinatorStatusResponse struct {
	QueuedTasks    int                `json:"queued_tasks"`
	LeasedTasks    int                `json:"leased_tasks"`
	CompletedTasks int                `json:"completed_tasks"`
	FailedTasks    int                `json:"failed_tasks"`
	TotalTasks     int                `json:"total_tasks"`
	TotalJobs      int                `json:"total_jobs"`
	RecentWorkers  []PullWorkerStatus `json:"recent_workers"`
	Uptime         string             `json:"uptime"`
}
