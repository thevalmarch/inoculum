package worker

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/inoculum/internal/audit"
	"github.com/inoculum/internal/auth"
	"github.com/inoculum/internal/types"
)

// Server is the worker's HTTP server that receives tasks from the coordinator.
type Server struct {
	executor    *Executor
	port        int
	concurrency int
	sem         chan struct{}
	token       string
	cert        tls.Certificate
	nonceCache  *auth.NonceCache
}

// NewServer creates a new worker HTTP server.
func NewServer(port int, concurrency int, allowedPaths []string, token string, cert tls.Certificate) *Server {
	return &Server{
		executor:    NewExecutor(allowedPaths),
		port:        port,
		concurrency: concurrency,
		sem:         make(chan struct{}, concurrency),
		token:       token,
		cert:        cert,
		nonceCache:  auth.NewNonceCache(auth.ReplayWindow),
	}
}

// Start begins listening for task execution requests.
func (s *Server) Start() error {
	defer s.nonceCache.Stop()
	
	mux := http.NewServeMux()
	mux.HandleFunc("/execute", auth.WithTokenAuth(s.token, s.nonceCache, s.handleExecute))

	addr := fmt.Sprintf(":%d", s.port)
	
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{s.cert},
	}
	listener, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to create TLS listener: %w", err)
	}

	log.Printf("[worker] Listening on %s (HTTPS)", addr)
	log.Printf("[worker] Endpoints:")
	log.Printf("[worker]   POST /execute — execute a task")
	return http.Serve(listener, mux)
}

// handleExecute processes POST /execute
func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req types.ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	task := req.Task
	log.Printf("[worker] Received task %s (type: %s), waiting for execution slot...", task.ID, task.Type)

	// Acquire semaphore
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	log.Printf("[worker] Executing task %s (type: %s)", task.ID, task.Type)

	output, duration, err := s.executor.Execute(task.Type, task.Input)

	result := types.Result{
		TaskID:      task.ID,
		Output:      output,
		Duration:    duration,
		DurationStr: duration.String(),
	}

	if err != nil {
		result.Error = err.Error()
		log.Printf("[worker] Task %s failed: %v (took %s)", task.ID, err, duration)
		
		status := "failed"
		if strings.Contains(err.Error(), "path traversal attempt blocked") {
			status = "path_traversal_blocked"
		}
		
		audit.LogEvent("task_execution", r.RemoteAddr, status, "Task execution failed", map[string]any{
			"task_id":   task.ID,
			"task_type": task.Type,
			"error":     err.Error(),
			"duration":  duration.String(),
		})
	} else {
		log.Printf("[worker] Task %s completed (took %s)", task.ID, duration)
		audit.LogEvent("task_execution", r.RemoteAddr, "success", "Task executed successfully", map[string]any{
			"task_id":   task.ID,
			"task_type": task.Type,
			"duration":  duration.String(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(types.ExecuteResponse{Result: result}); err != nil {
		log.Printf("[worker] Error writing response: %v", err)
	}
}
