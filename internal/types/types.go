package types

import "time"

// Status represents the current state of a task.
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

// WorkerInfo describes a worker node's identity and state.
type WorkerInfo struct {
	ID            string    `json:"id"`
	Address       string    `json:"address"` // host:port
	Hostname      string    `json:"hostname"`
	CPUCores      int       `json:"cpu_cores"`
	RAMBytes      uint64    `json:"ram_bytes"`
	GPUInfo       string    `json:"gpu_info,omitempty"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Busy          bool      `json:"busy"`
	ActiveTasks   int       `json:"active_tasks"`
}

// Task is the smallest unit of work assigned to a single worker.
type Task struct {
	ID       string `json:"id"`
	JobID    string `json:"job_id"`
	Type     string `json:"type"`     // e.g. "dummy", "file_analyze", "http_fetch"
	Input    string `json:"input"`    // task-specific input data
	Status   Status `json:"status"`
	WorkerID string `json:"worker_id,omitempty"`
}

// Job is a high-level request submitted by the user, broken into tasks.
type Job struct {
	ID          string    `json:"id"`
	Tasks       []Task    `json:"tasks"`
	Status      Status    `json:"status"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// Result contains the output produced by a worker for a single task.
type Result struct {
	TaskID      string        `json:"task_id"`
	Output      string        `json:"output"`
	Duration    time.Duration `json:"duration_ns"` // processing duration in nanoseconds
	DurationStr string        `json:"duration"`    // human-readable duration
	Error       string        `json:"error,omitempty"`
}

// --- Request / Response types for HTTP endpoints ---

// RegisterRequest is sent by a worker to POST /register.
type RegisterRequest struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Hostname string `json:"hostname"`
	CPUCores int    `json:"cpu_cores"`
	RAMBytes uint64 `json:"ram_bytes"`
	GPUInfo  string `json:"gpu_info,omitempty"`
}

// RegisterResponse is returned by the coordinator after registration.
type RegisterResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// HeartbeatRequest is sent by a worker to POST /heartbeat.
type HeartbeatRequest struct {
	ID string `json:"id"`
}

// HeartbeatResponse is returned by the coordinator.
type HeartbeatResponse struct {
	OK bool `json:"ok"`
}

// SubmitJobRequest is sent by a user to POST /submit-job.
type SubmitJobRequest struct {
	TaskType string   `json:"task_type"`  // type of tasks to create
	Inputs   []string `json:"inputs"`     // one input per task
}

// SubmitJobResponse is returned after a job is processed.
type SubmitJobResponse struct {
	JobID          string        `json:"job_id"`
	Results        []Result      `json:"results"`
	TotalDuration  time.Duration `json:"total_duration_ns"`
	TotalDurationS string        `json:"total_duration"`
	RoundTrips     []RoundTrip   `json:"round_trips"`
}

// RoundTrip captures latency metrics for a single task dispatch.
type RoundTrip struct {
	TaskID   string        `json:"task_id"`
	WorkerID string        `json:"worker_id"`
	Latency  time.Duration `json:"latency_ns"`
	LatencyS string        `json:"latency"`
}

// ExecuteRequest is sent by the coordinator to a worker's POST /execute.
type ExecuteRequest struct {
	Task Task `json:"task"`
}

// ExecuteResponse is returned by the worker after executing a task.
type ExecuteResponse struct {
	Result Result `json:"result"`
}

// StatusResponse is returned by GET /status.
type StatusResponse struct {
	ActiveWorkers  int    `json:"active_workers"`
	TotalWorkers   int    `json:"total_workers"`
	PendingTasks   int    `json:"pending_tasks"`
	RunningTasks   int    `json:"running_tasks"`
	CompletedTasks int    `json:"completed_tasks"`
	FailedTasks    int    `json:"failed_tasks"`
	TotalJobs      int    `json:"total_jobs"`
	Uptime         string `json:"uptime"`
}
