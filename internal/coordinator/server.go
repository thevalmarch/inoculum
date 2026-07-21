package coordinator

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/inoculum/internal/audit"
	"github.com/inoculum/internal/auth"
	"github.com/inoculum/internal/crypto"
	"github.com/inoculum/internal/types"
)

// Server is the coordinator's HTTP server.
type Server struct {
	registry  *Registry
	scheduler *Scheduler
	startTime time.Time
	port      int
	token         string
	cert          tls.Certificate
	nonceCache    *auth.NonceCache
	rateLimiter   *RateLimiter
	workerClients map[string]*http.Client
	wcMu          sync.Mutex

	// Job tracking
	mu   sync.RWMutex
	jobs map[string]*types.Job

	// Task counters
	pendingTasks   int
	runningTasks   int
	completedTasks int
	failedTasks    int

	// Max retries for task reassignment (Phase 5)
	maxRetries int
}

// NewServer creates a new coordinator server.
func NewServer(port int, strategy ScheduleStrategy, token string, cert tls.Certificate) *Server {
	return &Server{
		registry:      NewRegistry(),
		scheduler:     NewScheduler(strategy),
		startTime:     time.Now(),
		port:          port,
		token:         token,
		cert:          cert,
		nonceCache:    auth.NewNonceCache(auth.ReplayWindow),
		rateLimiter:   NewRateLimiter(1.0, 60.0), // 1 job/sec, burst of 60
		workerClients: make(map[string]*http.Client),
		jobs:          make(map[string]*types.Job),
		maxRetries:    2,
	}
}

// Start begins listening for HTTP requests and starts the registry cleanup.
func (s *Server) Start() error {
	stop := make(chan struct{})
	s.registry.StartCleanup(stop)
	defer s.nonceCache.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/register", auth.WithTokenAuth(s.token, s.nonceCache, s.handleRegister))
	mux.HandleFunc("/heartbeat", auth.WithTokenAuth(s.token, s.nonceCache, s.handleHeartbeat))
	mux.HandleFunc("/submit-job", auth.WithTokenAuth(s.token, s.nonceCache, s.handleSubmitJob))
	mux.HandleFunc("/status", auth.WithTokenAuth(s.token, s.nonceCache, s.handleStatus))

	addr := fmt.Sprintf(":%d", s.port)
	
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
	}
	listener, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to create TLS listener: %w", err)
	}

	log.Printf("[coordinator] Starting HTTPS on %s", addr)
	log.Printf("[coordinator] Endpoints:")
	log.Printf("[coordinator]   POST /register    — worker registration")
	log.Printf("[coordinator]   POST /heartbeat   — worker alive signal")
	log.Printf("[coordinator]   POST /submit-job  — submit a new job")
	log.Printf("[coordinator]   GET  /status      — system status")
	return http.Serve(listener, mux)
}

// SetSchedulerStrategy updates the scheduling strategy.
func (s *Server) SetSchedulerStrategy(strategy ScheduleStrategy) {
	s.scheduler.SetStrategy(strategy)
}

// handleRegister processes POST /register
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	s.registry.Register(req)
	audit.LogEvent("worker_registration", r.RemoteAddr, "success", "Worker registered", map[string]any{"worker_id": req.ID})
	writeJSON(w, types.RegisterResponse{
		OK:      true,
		Message: fmt.Sprintf("Worker %s registered successfully", req.ID),
	})
}

// handleHeartbeat processes POST /heartbeat
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ok := s.registry.Heartbeat(req.ID)
	writeJSON(w, types.HeartbeatResponse{OK: ok})
}

// handleSubmitJob processes POST /submit-job
func (s *Server) handleSubmitJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}

	if !s.rateLimiter.Allow(ip) {
		audit.LogEvent("job_submission", ip, "429", "Rate limit exceeded", map[string]any{"path": r.URL.Path})
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}

	var req types.SubmitJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Inputs) == 0 {
		http.Error(w, "no inputs provided", http.StatusBadRequest)
		return
	}

	// Get available workers
	workers := s.registry.GetAvailable()
	if len(workers) == 0 {
		http.Error(w, "no workers available", http.StatusServiceUnavailable)
		return
	}

	// Create job and tasks
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	tasks := make([]types.Task, len(req.Inputs))
	for i, input := range req.Inputs {
		tasks[i] = types.Task{
			ID:     fmt.Sprintf("%s-task-%d", jobID, i),
			JobID:  jobID,
			Type:   req.TaskType,
			Input:  input,
			Status: types.StatusPending,
		}
	}

	job := &types.Job{
		ID:          jobID,
		Tasks:       tasks,
		Status:      types.StatusProcessing,
		SubmittedAt: time.Now(),
	}

	s.mu.Lock()
	s.jobs[jobID] = job
	s.mu.Unlock()

	log.Printf("[coordinator] Job %s created with %d tasks (type: %s)", jobID, len(tasks), req.TaskType)

	// Dispatch tasks to workers in parallel
	jobStart := time.Now()
	results, roundTrips := s.dispatchTasks(tasks, workers)
	totalDuration := time.Since(jobStart)

	// Update job status
	s.mu.Lock()
	job.Status = types.StatusCompleted
	for _, res := range results {
		if res.Error != "" {
			job.Status = types.StatusFailed
			break
		}
	}
	s.mu.Unlock()

	resp := types.SubmitJobResponse{
		JobID:          jobID,
		Results:        results,
		TotalDuration:  totalDuration,
		TotalDurationS: totalDuration.String(),
		RoundTrips:     roundTrips,
	}

	log.Printf("[coordinator] Job %s completed in %s", jobID, totalDuration)
	for _, rt := range roundTrips {
		log.Printf("[coordinator]   Task %s → Worker %s: round-trip %s", rt.TaskID, rt.WorkerID, rt.LatencyS)
	}

	audit.LogEvent("job_submission", r.RemoteAddr, "success", "Job completed", map[string]any{
		"job_id":     jobID,
		"tasks":      len(tasks),
		"task_type":  req.TaskType,
		"duration_s": totalDuration.String(),
	})

	writeJSON(w, resp)
}

// dispatchTasks sends tasks to workers and collects results.
func (s *Server) dispatchTasks(tasks []types.Task, workers []*types.WorkerInfo) ([]types.Result, []types.RoundTrip) {
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		results    = make([]types.Result, len(tasks))
		roundTrips = make([]types.RoundTrip, len(tasks))
	)

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t types.Task) {
			defer wg.Done()

			result, rt := s.dispatchSingleTask(t, workers)

			mu.Lock()
			results[idx] = result
			roundTrips[idx] = rt
			mu.Unlock()
		}(i, task)
	}

	wg.Wait()
	return results, roundTrips
}

// dispatchSingleTask sends a single task to a worker, with retry on failure (Phase 5).
func (s *Server) dispatchSingleTask(task types.Task, workers []*types.WorkerInfo) (types.Result, types.RoundTrip) {
	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		// Pick a worker
		worker := s.scheduler.Pick(workers)
		if worker == nil {
			return types.Result{
				TaskID: task.ID,
				Error:  "no workers available",
			}, types.RoundTrip{TaskID: task.ID}
		}

		task.WorkerID = worker.ID
		task.Status = types.StatusProcessing
		s.registry.IncrementActiveTasks(worker.ID)

		s.mu.Lock()
		s.pendingTasks--
		s.runningTasks++
		s.mu.Unlock()

		// Send task to worker
		rtStart := time.Now()
		result, err := s.sendTaskToWorker(worker, task)
		rtDuration := time.Since(rtStart)

		s.registry.DecrementActiveTasks(worker.ID)

		s.mu.Lock()
		s.runningTasks--
		s.mu.Unlock()

		rt := types.RoundTrip{
			TaskID:   task.ID,
			WorkerID: worker.ID,
			Latency:  rtDuration,
			LatencyS: rtDuration.String(),
		}

		if err != nil {
			log.Printf("[coordinator] Task %s failed on worker %s (attempt %d/%d): %v",
				task.ID, worker.ID, attempt+1, s.maxRetries+1, err)

			if attempt < s.maxRetries {
				log.Printf("[coordinator] Retrying task %s on another worker...", task.ID)
				continue
			}

			s.mu.Lock()
			s.failedTasks++
			s.mu.Unlock()

			return types.Result{
				TaskID: task.ID,
				Error:  fmt.Sprintf("all attempts failed: %v", err),
			}, rt
		}

		s.mu.Lock()
		s.completedTasks++
		s.mu.Unlock()

		return result, rt
	}

	// Should not reach here
	return types.Result{TaskID: task.ID, Error: "unexpected dispatch failure"}, types.RoundTrip{TaskID: task.ID}
}

// sendTaskToWorker makes the HTTP POST to the worker's /execute endpoint.
func (s *Server) sendTaskToWorker(worker *types.WorkerInfo, task types.Task) (types.Result, error) {
	reqBody, err := json.Marshal(types.ExecuteRequest{Task: task})
	if err != nil {
		return types.Result{}, fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://%s/execute", worker.Address), bytes.NewReader(reqBody))
	if err != nil {
		return types.Result{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Inoculum-Token", s.token)
	req.Header.Set("X-Inoculum-Nonce", auth.GenerateNonce())
	req.Header.Set("X-Inoculum-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	s.wcMu.Lock()
	client, ok := s.workerClients[worker.Address]
	if !ok {
		tlsConfig := crypto.NewTOFUClientConfig(worker.Address, "", ".inoculum-coordinator-known-hosts")
		
		// Clone DefaultTransport to preserve connection pooling and HTTP/2 multiplexing,
		// otherwise a raw &http.Transport{} defaults to no connection reuse (MaxIdleConns=2) 
		// and forces expensive TLS handshakes on every concurrent task dispatch.
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = tlsConfig
		transport.MaxIdleConnsPerHost = 100 // High concurrency per worker

		client = &http.Client{
			Timeout:   5 * time.Minute,
			Transport: transport,
		}
		s.workerClients[worker.Address] = client
	}
	s.wcMu.Unlock()

	resp, err := client.Do(req)
	if err != nil {
		return types.Result{}, fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return types.Result{}, fmt.Errorf("worker returned status %d", resp.StatusCode)
	}

	var execResp types.ExecuteResponse
	if err := json.NewDecoder(resp.Body).Decode(&execResp); err != nil {
		return types.Result{}, fmt.Errorf("decode error: %w", err)
	}

	return execResp.Result, nil
}

// handleStatus processes GET /status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	resp := types.StatusResponse{
		ActiveWorkers:  s.registry.ActiveCount(),
		TotalWorkers:   s.registry.TotalCount(),
		PendingTasks:   s.pendingTasks,
		RunningTasks:   s.runningTasks,
		CompletedTasks: s.completedTasks,
		FailedTasks:    s.failedTasks,
		TotalJobs:      len(s.jobs),
		Uptime:         time.Since(s.startTime).String(),
	}
	s.mu.RUnlock()

	writeJSON(w, resp)
}

// writeJSON encodes a value as JSON and writes it to the response.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[coordinator] Error writing response: %v", err)
	}
}
