package coordinator

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/thevalmarch/inoculum/internal/leasequeue"
	"github.com/thevalmarch/inoculum/internal/monitor"
	"github.com/thevalmarch/inoculum/internal/types"
	"github.com/thevalmarch/inoculum/internal/workload"
)

func (s *Server) handlePullClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.PullClaimRequest
	if err := decodeJSONRequest(w, r, &req, MaxWorkerRequestBytes); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, types.PullClaimResponse{Status: types.PullRejected, Message: "invalid request"})
		return
	}
	if err := types.ValidateWorkerID(req.WorkerID); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, types.PullClaimResponse{Status: types.PullRejected, Message: err.Error()})
		return
	}
	s.recordWorkerActivity(req.WorkerID)

	for _, taskID := range s.pullQueue.RequeueExpired() {
		s.logExpiredLease(taskID)
	}
	assignment, err := s.pullQueue.Claim(req.WorkerID)
	if err != nil {
		writeJSONStatus(w, http.StatusBadRequest, types.PullClaimResponse{Status: types.PullRejected})
		return
	}
	if assignment == nil {
		writeJSON(w, types.PullClaimResponse{Status: types.PullNoTask})
		return
	}

	log.Printf("[pull] Task %s leased to %s (attempt %d)", assignment.Task.ID, req.WorkerID, assignment.Lease.Attempt)
	s.recordEvent(monitor.SeverityInfo, "task_leased", "Task leased", map[string]string{
		"task_id": assignment.Task.ID, "worker_id": req.WorkerID, "attempt": fmt.Sprint(assignment.Lease.Attempt),
	})
	writeJSON(w, types.PullClaimResponse{
		Status: types.PullTaskAvailable,
		Task: &types.PullTask{
			TaskID:         assignment.Task.ID,
			JobID:          assignment.Task.JobID,
			Type:           assignment.Task.Type,
			Input:          assignment.Task.Input,
			LeaseID:        assignment.Lease.ID,
			Attempt:        assignment.Lease.Attempt,
			LeaseExpiresAt: assignment.Lease.ExpiresAt,
		},
	})
}

func (s *Server) handlePullRenew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.PullRenewRequest
	if err := decodeJSONRequest(w, r, &req, MaxWorkerRequestBytes); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, types.PullRenewResponse{Status: types.PullRejected, Message: "invalid request"})
		return
	}
	if err := validateLeaseRequest(req.WorkerID, req.TaskID, req.LeaseID); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, types.PullRenewResponse{Status: types.PullRejected, Message: err.Error()})
		return
	}
	s.recordWorkerActivity(req.WorkerID)

	lease, err := s.pullQueue.Renew(req.TaskID, req.LeaseID, req.WorkerID)
	if err != nil {
		status, code := pullErrorResponse(err)
		writeJSONStatus(w, code, types.PullRenewResponse{Status: status, Message: err.Error()})
		return
	}
	writeJSON(w, types.PullRenewResponse{Status: types.PullLeaseRenewed, LeaseExpiresAt: lease.ExpiresAt})
}

func (s *Server) handlePullResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.PullResultRequest
	if err := decodeJSONRequest(w, r, &req, MaxWorkerRequestBytes); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, types.PullResultResponse{Status: types.PullRejected, Message: "invalid request"})
		return
	}
	if err := validateLeaseRequest(req.WorkerID, req.TaskID, req.LeaseID); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, types.PullResultResponse{Status: types.PullRejected, Message: err.Error()})
		return
	}
	if req.Result.TaskID != "" && req.Result.TaskID != req.TaskID {
		writeJSONStatus(w, http.StatusBadRequest, types.PullResultResponse{Status: types.PullRejected, Message: "result task ID does not match request task ID"})
		return
	}
	if err := types.ValidateResult(req.Result); err != nil {
		writeJSONStatus(w, http.StatusRequestEntityTooLarge, types.PullResultResponse{Status: types.PullRejected, Message: err.Error()})
		return
	}
	s.recordWorkerActivity(req.WorkerID)

	outcome, err := s.pullQueue.Complete(req.TaskID, req.LeaseID, req.WorkerID, leasequeue.Result{
		Output:   req.Result.Output,
		Duration: req.Result.Duration,
		Error:    req.Result.Error,
	})
	if err != nil {
		status, code := pullErrorResponse(err)
		log.Printf("[pull] Rejected result for task %s from %s: %v", req.TaskID, req.WorkerID, err)
		writeJSONStatus(w, code, types.PullResultResponse{Status: status, Message: err.Error()})
		return
	}

	response := types.PullResultResponse{}
	switch outcome {
	case leasequeue.OutcomeCompleted:
		response.Status = types.PullTaskCompleted
		log.Printf("[pull] Task %s completed by %s", req.TaskID, req.WorkerID)
		s.recordEvent(monitor.SeverityInfo, "task_completed", "Task completed", map[string]string{"task_id": req.TaskID, "worker_id": req.WorkerID})
	case leasequeue.OutcomeRequeued:
		response.Status = types.PullTaskRequeued
		response.Message = "task execution failed; task returned to queue"
		log.Printf("[pull] Task %s failed on %s and was requeued", req.TaskID, req.WorkerID)
		s.recordEvent(monitor.SeverityWarning, "task_requeued", "Task failed and was requeued", map[string]string{"task_id": req.TaskID, "worker_id": req.WorkerID})
	case leasequeue.OutcomeFailed:
		response.Status = types.PullTaskFailed
		response.Message = "task execution failed and retry policy was exhausted"
		log.Printf("[pull] Task %s permanently failed after result from %s", req.TaskID, req.WorkerID)
		s.recordEvent(monitor.SeverityError, "task_failed", "Task permanently failed", map[string]string{"task_id": req.TaskID, "worker_id": req.WorkerID})
	default:
		writeJSONStatus(w, http.StatusInternalServerError, types.PullResultResponse{Status: types.PullRejected})
		return
	}
	writeJSON(w, response)
}

func (s *Server) handlePullSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.PullSubmitRequest
	if err := decodeJSONRequest(w, r, &req, workload.MaxManifestBytes); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "task_type and valid task inputs are required"})
		return
	}
	if err := workload.ValidateTaskType(req.TaskType); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(req.Inputs) > 0 && len(req.Tasks) > 0 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "inputs and keyed tasks cannot be combined"})
		return
	}

	tasks := make([]workload.Task, 0, max(len(req.Inputs), len(req.Tasks)))
	if len(req.Tasks) > 0 {
		if req.TaskType != workload.HTTPProbeType {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("keyed manifest tasks require task type %q", workload.HTTPProbeType)})
			return
		}
		for _, task := range req.Tasks {
			tasks = append(tasks, workload.Task{Key: task.Key, Input: task.Input})
		}
		if err := workload.ValidateTasks(tasks); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	} else {
		if err := workload.ValidateSimpleInputs(req.Inputs); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		for _, input := range req.Inputs {
			tasks = append(tasks, workload.Task{Input: input})
		}
	}

	jobID := fmt.Sprintf("pull-job-%d", time.Now().UnixNano())
	taskIDs := make([]string, len(tasks))
	for i, task := range tasks {
		taskID := fmt.Sprintf("%s-task-%d", jobID, i)
		if err := s.pullQueue.Enqueue(leasequeue.TaskSpec{ID: taskID, JobID: jobID, Key: task.Key, Type: req.TaskType, Input: task.Input}); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		taskIDs[i] = taskID
	}

	log.Printf("[pull] Job %s queued with %d tasks", jobID, len(taskIDs))
	s.recordEvent(monitor.SeverityInfo, "job_queued", "Job queued", map[string]string{"job_id": jobID, "tasks": fmt.Sprint(len(taskIDs))})
	writeJSON(w, types.PullSubmitResponse{JobID: jobID, TaskIDs: taskIDs})
}

func validateLeaseRequest(workerID, taskID, leaseID string) error {
	if err := types.ValidateWorkerID(workerID); err != nil {
		return err
	}
	if err := types.ValidateTaskID(taskID); err != nil {
		return err
	}
	return types.ValidateLeaseID(leaseID)
}

func (s *Server) handlePullJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jobID := r.URL.Query().Get("id")
	if jobID == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "job id is required"})
		return
	}

	for _, taskID := range s.pullQueue.RequeueExpired() {
		s.logExpiredLease(taskID)
	}
	tasks := s.pullQueue.JobTasks(jobID)
	if len(tasks) == 0 {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}

	response := types.PullJobResponse{JobID: jobID, State: types.PullJobQueued}
	allTerminal := true
	anyFailed := false
	started := false
	for _, task := range tasks {
		if task.State != leasequeue.StateCompleted && task.State != leasequeue.StateFailed {
			allTerminal = false
		}
		if task.State == leasequeue.StateFailed {
			anyFailed = true
		}
		if task.Attempts > 0 {
			started = true
		}

		jobTask := types.PullJobTask{
			TaskID:   task.ID,
			Key:      task.Key,
			State:    string(task.State),
			Attempts: task.Attempts,
			WorkerID: task.WorkerID,
		}
		if task.Result != nil {
			jobTask.Result = &types.Result{
				TaskID:      task.ID,
				Output:      task.Result.Output,
				Duration:    task.Result.Duration,
				DurationStr: task.Result.Duration.String(),
				Error:       task.Result.Error,
			}
		}
		response.Tasks = append(response.Tasks, jobTask)
	}

	if allTerminal {
		if anyFailed {
			response.State = types.PullJobFailed
		} else {
			response.State = types.PullJobCompleted
		}
	} else if started {
		response.State = types.PullJobRunning
	}
	writeJSON(w, response)
}

func (s *Server) logExpiredLease(taskID string) {
	log.Printf("[pull] Lease expired for task %s; applying retry policy", taskID)
	s.recordEvent(monitor.SeverityWarning, "lease_expired", "Task lease expired; applying retry policy", map[string]string{"task_id": taskID})
}

func pullErrorResponse(err error) (types.PullStatus, int) {
	switch {
	case errors.Is(err, leasequeue.ErrAlreadyCompleted):
		return types.PullTaskCompleted, http.StatusConflict
	case errors.Is(err, leasequeue.ErrStaleLease):
		return types.PullStaleLease, http.StatusConflict
	case errors.Is(err, leasequeue.ErrTaskFailed):
		return types.PullTaskFailed, http.StatusConflict
	case errors.Is(err, leasequeue.ErrTaskNotFound):
		return types.PullRejected, http.StatusNotFound
	case errors.Is(err, leasequeue.ErrWrongWorker):
		return types.PullRejected, http.StatusConflict
	default:
		return types.PullRejected, http.StatusBadRequest
	}
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("[coordinator] Error writing response: %v", err)
	}
}
