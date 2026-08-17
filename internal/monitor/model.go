// Package monitor defines presentation-neutral, immutable views of Inoculum's
// live runtime state. Runtime packages may produce these values, but they never
// depend on a terminal renderer.
package monitor

import "time"

type ConnectionState string

const (
	ConnectionStarting    ConnectionState = "starting"
	ConnectionConnected   ConnectionState = "connected"
	ConnectionUnavailable ConnectionState = "unavailable"
	ConnectionAuthFailed  ConnectionState = "authentication_failed"
	ConnectionUntrusted   ConnectionState = "no_trusted_identity"
	ConnectionIdentity    ConnectionState = "identity_mismatch"
	ConnectionStopping    ConnectionState = "stopping"
)

type TaskCounts struct {
	Queued    int
	Running   int
	Completed int
	Failed    int
	Total     int
}

type TaskProgress struct {
	TaskID    string
	Key       string
	State     string
	WorkerID  string
	Attempt   int
	StartedAt time.Time
	Duration  time.Duration
	Output    string
	Error     string
}

type WorkerSummary struct {
	WorkerID     string
	LastActivity time.Time
	Active       int
}

type WorkerContribution struct {
	WorkerID  string
	Active    int
	Completed int
	Failed    int
}

type JobProgress struct {
	JobID     string
	State     string
	Total     int
	Queued    int
	Running   int
	Completed int
	Failed    int
	StartedAt time.Time
	Elapsed   time.Duration
	Tasks     []TaskProgress
	Workers   []WorkerContribution
}

type CoordinatorSnapshot struct {
	ObservedAt  time.Time
	Online      bool
	Addresses   []string
	Fingerprint string
	Uptime      time.Duration
	Tasks       TaskCounts
	Jobs        int
	Workers     []WorkerSummary
	CurrentJob  *JobProgress
	Events      []SystemEvent
}

type WorkerSnapshot struct {
	ObservedAt       time.Time
	WorkerID         string
	Coordinator      string
	Connection       ConnectionState
	ConnectedAt      time.Time
	UnavailableSince time.Time
	LastError        string
	RetryAttempt     int
	RetryAt          time.Time
	Concurrency      int
	ActiveTasks      []TaskProgress
	Completed        int
	Failed           int
	Events           []SystemEvent
}

type SubmitSnapshot struct {
	ObservedAt time.Time
	Submitted  bool
	Done       bool
	TimedOut   bool
	Job        JobProgress
	Error      string
}

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type SystemEvent struct {
	Time      time.Time
	Severity  Severity
	Component string
	Kind      string
	Message   string
	Fields    map[string]string
}
