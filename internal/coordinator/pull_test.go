package coordinator

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thevalmarch/inoculum/internal/leasequeue"
	"github.com/thevalmarch/inoculum/internal/types"
	"github.com/thevalmarch/inoculum/internal/workload"
)

func newPullTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := NewServer(Config{LeaseDuration: time.Minute, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("NewServer() error: %v", err)
	}
	return server
}

func TestServerUsesConfiguredLeaseDuration(t *testing.T) {
	const leaseDuration = 17 * time.Second
	server, err := NewServer(Config{LeaseDuration: leaseDuration, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-1", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	assignment, err := server.pullQueue.Claim("worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if got := assignment.Lease.ExpiresAt.Sub(assignment.Lease.IssuedAt); got != leaseDuration {
		t.Fatalf("lease duration = %s, want %s", got, leaseDuration)
	}
}

func TestServerUsesConfiguredMaxAttempts(t *testing.T) {
	server, err := NewServer(Config{LeaseDuration: time.Minute, MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-1", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}

	first, err := server.pullQueue.Claim("worker-a")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := server.pullQueue.Complete(first.Task.ID, first.Lease.ID, "worker-a", leasequeue.Result{Error: "first failure"})
	if err != nil || outcome != leasequeue.OutcomeRequeued {
		t.Fatalf("first failure = %q, %v", outcome, err)
	}

	second, err := server.pullQueue.Claim("worker-b")
	if err != nil {
		t.Fatal(err)
	}
	if second.Lease.Attempt != 2 {
		t.Fatalf("second attempt = %d, want 2", second.Lease.Attempt)
	}
	outcome, err = server.pullQueue.Complete(second.Task.ID, second.Lease.ID, "worker-b", leasequeue.Result{Error: "second failure"})
	if err != nil || outcome != leasequeue.OutcomeFailed {
		t.Fatalf("second failure = %q, %v", outcome, err)
	}
	task, ok := server.pullQueue.Get("task-1")
	if !ok || task.State != leasequeue.StateFailed || task.Attempts != 2 {
		t.Fatalf("failed task = %#v", task)
	}
}

func TestSuccessfulTaskIsUnaffectedByCustomPolicy(t *testing.T) {
	server, err := NewServer(Config{LeaseDuration: 25 * time.Second, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-1", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	assignment, err := server.pullQueue.Claim("worker-a")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := server.pullQueue.Complete(assignment.Task.ID, assignment.Lease.ID, "worker-a", leasequeue.Result{Output: "ok"})
	if err != nil || outcome != leasequeue.OutcomeCompleted {
		t.Fatalf("completion = %q, %v", outcome, err)
	}
}

func TestServerRejectsInvalidQueuePolicy(t *testing.T) {
	for _, config := range []Config{
		{LeaseDuration: 0, MaxAttempts: 3},
		{LeaseDuration: -time.Second, MaxAttempts: 3},
		{LeaseDuration: time.Second, MaxAttempts: 0},
		{LeaseDuration: time.Second, MaxAttempts: -1},
	} {
		if _, err := NewServer(config); err == nil {
			t.Fatalf("NewServer(%#v) succeeded", config)
		}
	}
}

func requestJSON(t *testing.T, handler http.HandlerFunc, method, target string, request, response any) int {
	t.Helper()
	var body bytes.Buffer
	if request != nil {
		if err := json.NewEncoder(&body).Encode(request); err != nil {
			t.Fatalf("encode request: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, &body)
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	if response != nil {
		if err := json.NewDecoder(recorder.Body).Decode(response); err != nil {
			t.Fatalf("decode response (HTTP %d, body %q): %v", recorder.Code, recorder.Body.String(), err)
		}
	}
	return recorder.Code
}

func requestBody(handler http.HandlerFunc, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler(recorder, req)
	return recorder
}

func TestHTTPServerUsesConservativeTimeouts(t *testing.T) {
	server := newPullTestServer(t).httpServer()
	if server.ReadHeaderTimeout != 5*time.Second || server.WriteTimeout != 30*time.Second || server.IdleTimeout != 60*time.Second || server.MaxHeaderBytes != 32*1024 {
		t.Fatalf("http server configuration = %#v", server)
	}
	if server.ReadTimeout != 0 {
		t.Fatalf("ReadTimeout = %s, want per-route body limits instead", server.ReadTimeout)
	}
}

func TestWorkerEndpointsRejectUnknownTrailingAndOversizedJSON(t *testing.T) {
	server := newPullTestServer(t)
	endpoints := []struct {
		path    string
		handler http.HandlerFunc
		valid   string
	}{
		{path: "/worker/claim", handler: server.handlePullClaim, valid: `{"worker_id":"worker-a"}`},
		{path: "/worker/renew", handler: server.handlePullRenew, valid: `{"worker_id":"worker-a","task_id":"task-1","lease_id":"lease-1"}`},
		{path: "/worker/result", handler: server.handlePullResult, valid: `{"worker_id":"worker-a","task_id":"task-1","lease_id":"lease-1","result":{}}`},
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint.path, func(t *testing.T) {
			for _, body := range []string{
				strings.TrimSuffix(endpoint.valid, "}") + `,"unknown":true}`,
				endpoint.valid + ` {}`,
				`{"padding":"` + strings.Repeat("x", MaxWorkerRequestBytes) + `"}`,
			} {
				if got := requestBody(endpoint.handler, endpoint.path, body).Code; got != http.StatusBadRequest {
					t.Fatalf("body of %d bytes returned HTTP %d, want 400", len(body), got)
				}
			}
		})
	}
}

func TestWorkerRequestFieldLimits(t *testing.T) {
	server := newPullTestServer(t)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		body    string
		want    int
	}{
		{name: "worker ID", handler: server.handlePullClaim, path: "/worker/claim", body: `{"worker_id":"` + strings.Repeat("w", types.MaxWorkerIDBytes+1) + `"}`, want: http.StatusBadRequest},
		{name: "worker control", handler: server.handlePullClaim, path: "/worker/claim", body: `{"worker_id":"worker\\nforged"}`, want: http.StatusBadRequest},
		{name: "task ID", handler: server.handlePullRenew, path: "/worker/renew", body: `{"worker_id":"worker-a","task_id":"` + strings.Repeat("t", types.MaxTaskIDBytes+1) + `","lease_id":"lease-1"}`, want: http.StatusBadRequest},
		{name: "lease ID", handler: server.handlePullRenew, path: "/worker/renew", body: `{"worker_id":"worker-a","task_id":"task-1","lease_id":"` + strings.Repeat("l", types.MaxLeaseIDBytes+1) + `"}`, want: http.StatusBadRequest},
		{name: "result output", handler: server.handlePullResult, path: "/worker/result", body: `{"worker_id":"worker-a","task_id":"task-1","lease_id":"lease-1","result":{"output":"` + strings.Repeat("o", types.MaxResultOutputBytes+1) + `"}}`, want: http.StatusRequestEntityTooLarge},
		{name: "result error", handler: server.handlePullResult, path: "/worker/result", body: `{"worker_id":"worker-a","task_id":"task-1","lease_id":"lease-1","result":{"error":"` + strings.Repeat("e", types.MaxResultErrorBytes+1) + `"}}`, want: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := requestBody(test.handler, test.path, test.body).Code; got != test.want {
				t.Fatalf("HTTP status = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSimpleSubmissionLimitsPreventQueueAmplification(t *testing.T) {
	server := newPullTestServer(t)
	inputs := make([]string, workload.MaxTasks)
	for index := range inputs {
		inputs[index] = "1ms"
	}
	var accepted types.PullSubmitResponse
	if status := requestJSON(t, server.handlePullSubmit, http.MethodPost, "/pull/submit", types.PullSubmitRequest{TaskType: "diagnostic_sleep", Inputs: inputs}, &accepted); status != http.StatusOK {
		t.Fatalf("maximum simple submission HTTP status = %d", status)
	}
	if len(accepted.TaskIDs) != workload.MaxTasks {
		t.Fatalf("task IDs = %d, want %d", len(accepted.TaskIDs), workload.MaxTasks)
	}
	before := server.pullQueue.Stats().Total
	for _, request := range []types.PullSubmitRequest{
		{TaskType: "diagnostic_sleep", Inputs: append(inputs, "1ms")},
		{TaskType: "diagnostic_sleep", Inputs: []string{strings.Repeat("x", workload.MaxInputBytes+1)}},
		{TaskType: strings.Repeat("x", workload.MaxTaskTypeBytes+1), Inputs: []string{"1ms"}},
	} {
		var response map[string]string
		if status := requestJSON(t, server.handlePullSubmit, http.MethodPost, "/pull/submit", request, &response); status != http.StatusBadRequest {
			t.Fatalf("oversized submission HTTP status = %d", status)
		}
	}
	if after := server.pullQueue.Stats().Total; after != before {
		t.Fatalf("rejected submissions changed queue size from %d to %d", before, after)
	}
}

func TestSubmitRejectsUnknownFieldsTrailingJSONAndOversizedBody(t *testing.T) {
	server := newPullTestServer(t)
	for _, body := range []string{
		`{"task_type":"diagnostic_sleep","inputs":["1ms"],"unknown":true}`,
		`{"task_type":"diagnostic_sleep","inputs":["1ms"]} {}`,
		`{"task_type":"diagnostic_sleep","inputs":["` + strings.Repeat("x", workload.MaxManifestBytes) + `"]}`,
	} {
		if got := requestBody(server.handlePullSubmit, "/pull/submit", body).Code; got != http.StatusBadRequest {
			t.Fatalf("body of %d bytes returned HTTP %d, want 400", len(body), got)
		}
	}
}

func TestPullProtocolHappyPath(t *testing.T) {
	server := newPullTestServer(t)

	var submitted types.PullSubmitResponse
	if status := requestJSON(t, server.handlePullSubmit, http.MethodPost, "/pull/submit", types.PullSubmitRequest{
		TaskType: "diagnostic_sleep",
		Inputs:   []string{"1ms"},
	}, &submitted); status != http.StatusOK {
		t.Fatalf("submit HTTP status = %d", status)
	}
	if submitted.JobID == "" || len(submitted.TaskIDs) != 1 {
		t.Fatalf("submit response = %#v", submitted)
	}

	var claim types.PullClaimResponse
	if status := requestJSON(t, server.handlePullClaim, http.MethodPost, "/worker/claim", types.PullClaimRequest{WorkerID: "worker-a"}, &claim); status != http.StatusOK {
		t.Fatalf("claim HTTP status = %d", status)
	}
	if claim.Status != types.PullTaskAvailable || claim.Task == nil || claim.Task.TaskID != submitted.TaskIDs[0] {
		t.Fatalf("claim response = %#v", claim)
	}

	var renewed types.PullRenewResponse
	if status := requestJSON(t, server.handlePullRenew, http.MethodPost, "/worker/renew", types.PullRenewRequest{
		WorkerID: "worker-a", TaskID: claim.Task.TaskID, LeaseID: claim.Task.LeaseID,
	}, &renewed); status != http.StatusOK || renewed.Status != types.PullLeaseRenewed {
		t.Fatalf("renew = HTTP %d, %#v", status, renewed)
	}

	var result types.PullResultResponse
	if status := requestJSON(t, server.handlePullResult, http.MethodPost, "/worker/result", types.PullResultRequest{
		WorkerID: "worker-a",
		TaskID:   claim.Task.TaskID,
		LeaseID:  claim.Task.LeaseID,
		Result:   types.Result{TaskID: claim.Task.TaskID, Output: "ok", Duration: time.Millisecond},
	}, &result); status != http.StatusOK || result.Status != types.PullTaskCompleted {
		t.Fatalf("result = HTTP %d, %#v", status, result)
	}

	var job types.PullJobResponse
	if status := requestJSON(t, server.handlePullJob, http.MethodGet, "/pull/job?id="+submitted.JobID, nil, &job); status != http.StatusOK {
		t.Fatalf("job HTTP status = %d", status)
	}
	if job.State != types.PullJobCompleted || len(job.Tasks) != 1 || job.Tasks[0].WorkerID != "worker-a" {
		t.Fatalf("job response = %#v", job)
	}
}

func TestManifestKeySurvivesRetryWithoutChangingInternalTaskID(t *testing.T) {
	server := newPullTestServer(t)
	var submitted types.PullSubmitResponse
	request := types.PullSubmitRequest{
		TaskType: "http_probe",
		Tasks:    []types.PullSubmitTask{{Key: "homepage", Input: "https://example.com"}},
	}
	if status := requestJSON(t, server.handlePullSubmit, http.MethodPost, "/pull/submit", request, &submitted); status != http.StatusOK {
		t.Fatalf("submit HTTP status = %d", status)
	}
	if len(submitted.TaskIDs) != 1 || submitted.TaskIDs[0] == "homepage" {
		t.Fatalf("internal task IDs = %#v", submitted.TaskIDs)
	}

	var first types.PullClaimResponse
	requestJSON(t, server.handlePullClaim, http.MethodPost, "/worker/claim", types.PullClaimRequest{WorkerID: "worker-a"}, &first)
	var failed types.PullResultResponse
	requestJSON(t, server.handlePullResult, http.MethodPost, "/worker/result", types.PullResultRequest{
		WorkerID: "worker-a", TaskID: first.Task.TaskID, LeaseID: first.Task.LeaseID,
		Result: types.Result{TaskID: first.Task.TaskID, Error: "temporary probe failure"},
	}, &failed)
	if failed.Status != types.PullTaskRequeued {
		t.Fatalf("first result = %#v", failed)
	}

	var second types.PullClaimResponse
	requestJSON(t, server.handlePullClaim, http.MethodPost, "/worker/claim", types.PullClaimRequest{WorkerID: "worker-b"}, &second)
	if second.Task.TaskID != first.Task.TaskID || second.Task.Attempt != 2 {
		t.Fatalf("reassigned task = %#v", second.Task)
	}
	var completed types.PullResultResponse
	requestJSON(t, server.handlePullResult, http.MethodPost, "/worker/result", types.PullResultRequest{
		WorkerID: "worker-b", TaskID: second.Task.TaskID, LeaseID: second.Task.LeaseID,
		Result: types.Result{TaskID: second.Task.TaskID, Output: `{"status_code":200}`},
	}, &completed)

	var job types.PullJobResponse
	requestJSON(t, server.handlePullJob, http.MethodGet, "/pull/job?id="+submitted.JobID, nil, &job)
	if len(job.Tasks) != 1 || job.Tasks[0].Key != "homepage" || job.Tasks[0].TaskID != submitted.TaskIDs[0] || job.Tasks[0].Attempts != 2 || job.Tasks[0].WorkerID != "worker-b" {
		t.Fatalf("job tasks = %#v", job.Tasks)
	}
}

func TestPullProtocolNoTaskAndStaleResult(t *testing.T) {
	server := newPullTestServer(t)
	var empty types.PullClaimResponse
	if status := requestJSON(t, server.handlePullClaim, http.MethodPost, "/worker/claim", types.PullClaimRequest{WorkerID: "worker-a"}, &empty); status != http.StatusOK || empty.Status != types.PullNoTask {
		t.Fatalf("empty claim = HTTP %d, %#v", status, empty)
	}

	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-1", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	assignment, err := server.pullQueue.Claim("worker-a")
	if err != nil {
		t.Fatal(err)
	}
	var response types.PullResultResponse
	status := requestJSON(t, server.handlePullResult, http.MethodPost, "/worker/result", types.PullResultRequest{
		WorkerID: "worker-a", TaskID: assignment.Task.ID, LeaseID: "wrong-lease", Result: types.Result{Output: "late"},
	}, &response)
	if status != http.StatusConflict || response.Status != types.PullStaleLease {
		t.Fatalf("stale result = HTTP %d, %#v", status, response)
	}
}

func TestPullProtocolDuplicateResultIsExplicit(t *testing.T) {
	server := newPullTestServer(t)
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-1", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	assignment, _ := server.pullQueue.Claim("worker-a")
	request := types.PullResultRequest{
		WorkerID: "worker-a", TaskID: assignment.Task.ID, LeaseID: assignment.Lease.ID, Result: types.Result{Output: "ok"},
	}
	var first types.PullResultResponse
	if status := requestJSON(t, server.handlePullResult, http.MethodPost, "/worker/result", request, &first); status != http.StatusOK {
		t.Fatalf("first result HTTP status = %d", status)
	}

	var duplicate types.PullResultResponse
	status := requestJSON(t, server.handlePullResult, http.MethodPost, "/worker/result", request, &duplicate)
	if status != http.StatusConflict || duplicate.Status != types.PullTaskCompleted {
		t.Fatalf("duplicate result = HTTP %d, %#v", status, duplicate)
	}
}

func TestPullStatusUsesQueueAndRecentActivity(t *testing.T) {
	server := newPullTestServer(t)
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-1", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-2", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.pullQueue.Claim("worker-b"); err != nil {
		t.Fatal(err)
	}
	server.recordWorkerActivity("worker-b")

	var status types.CoordinatorStatusResponse
	if code := requestJSON(t, server.handlePullStatus, http.MethodGet, "/status", nil, &status); code != http.StatusOK {
		t.Fatalf("status HTTP code = %d", code)
	}
	if status.QueuedTasks != 1 || status.LeasedTasks != 1 || status.TotalTasks != 2 || status.TotalJobs != 1 {
		t.Fatalf("status response = %#v", status)
	}
	if len(status.RecentWorkers) != 1 || status.RecentWorkers[0].WorkerID != "worker-b" || status.RecentWorkers[0].ActiveLeases != 1 {
		t.Fatalf("worker status = %#v", status.RecentWorkers)
	}
}

func TestMonitorSnapshotUsesQueueWithoutSeparateRegistry(t *testing.T) {
	server := newPullTestServer(t)
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-1", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	if err := server.pullQueue.Enqueue(leasequeue.TaskSpec{ID: "task-2", JobID: "job-1", Type: "diagnostic_sleep"}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.pullQueue.Claim("worker-a"); err != nil {
		t.Fatal(err)
	}
	server.recordWorkerActivity("worker-a")

	snapshot := server.MonitorSnapshot([]string{"192.0.2.5:8080"}, "fingerprint")
	if snapshot.Tasks.Queued != 1 || snapshot.Tasks.Running != 1 || snapshot.Tasks.Total != 2 || snapshot.Jobs != 1 {
		t.Fatalf("monitor snapshot = %#v", snapshot)
	}
	if len(snapshot.Workers) != 1 || snapshot.Workers[0].WorkerID != "worker-a" || snapshot.Workers[0].Active != 1 {
		t.Fatalf("monitor workers = %#v", snapshot.Workers)
	}
	if snapshot.CurrentJob == nil || snapshot.CurrentJob.JobID != "job-1" || snapshot.CurrentJob.Running != 1 {
		t.Fatalf("current job = %#v", snapshot.CurrentJob)
	}
}

func TestLegacyRoutesAreGone(t *testing.T) {
	server := newPullTestServer(t)
	for _, path := range []string{"/register", "/heartbeat", "/submit-job", "/execute"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		recorder := httptest.NewRecorder()
		server.routes().ServeHTTP(recorder, req)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s returned HTTP %d, want 404", path, recorder.Code)
		}
	}
}

func TestCoordinatorRoutesRequireBearerTokenWithoutReplayHeaders(t *testing.T) {
	server := newPullTestServer(t)
	server.token = "correct-secret"

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{name: "correct", authorization: "Bearer correct-secret", wantStatus: http.StatusOK},
		{name: "wrong", authorization: "Bearer wrong-secret", wantStatus: http.StatusUnauthorized},
		{name: "missing", wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/status", nil)
			request.Header.Set("Authorization", test.authorization)
			// No timestamp or nonce is supplied. Authentication is deliberately
			// independent of clocks and generic replay state.
			recorder := httptest.NewRecorder()
			server.routes().ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}
