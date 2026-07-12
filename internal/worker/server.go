package worker

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/inoculum/internal/types"
)

// Server is the worker's HTTP server that receives tasks from the coordinator.
type Server struct {
	executor    *Executor
	port        int
	concurrency int
	sem         chan struct{}
}

// NewServer creates a new worker HTTP server.
func NewServer(port int, concurrency int) *Server {
	return &Server{
		executor:    NewExecutor(),
		port:        port,
		concurrency: concurrency,
		sem:         make(chan struct{}, concurrency),
	}
}

// Start begins listening for task execution requests.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/execute", s.handleExecute)

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("[worker] Listening on %s", addr)
	log.Printf("[worker] Endpoints:")
	log.Printf("[worker]   POST /execute — execute a task")
	return http.ListenAndServe(addr, mux)
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
	} else {
		log.Printf("[worker] Task %s completed (took %s)", task.ID, duration)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(types.ExecuteResponse{Result: result}); err != nil {
		log.Printf("[worker] Error writing response: %v", err)
	}
}
