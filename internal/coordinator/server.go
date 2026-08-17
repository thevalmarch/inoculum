package coordinator

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/inoculum/internal/auth"
	"github.com/inoculum/internal/leasequeue"
	"github.com/inoculum/internal/monitor"
)

const (
	DefaultLeaseDuration = 6 * time.Second
	DefaultMaxAttempts   = 3
)

// Config contains coordinator-owned runtime configuration. Lease and retry
// policy are global for the in-memory queue in V1.
type Config struct {
	Port          int
	Token         string
	Certificate   tls.Certificate
	LeaseDuration time.Duration
	MaxAttempts   int
}

// Server owns the task queue and exposes the coordinator's single HTTPS API.
// Workers only make outbound requests to this server.
type Server struct {
	startTime time.Time
	port      int
	token     string
	cert      tls.Certificate
	pullQueue *leasequeue.Queue
	events    *monitor.Recorder

	leaseDuration time.Duration
	maxAttempts   int

	workerMu       sync.Mutex
	workerActivity map[string]time.Time
}

func NewServer(config Config) (*Server, error) {
	pullQueue, err := leasequeue.New(leasequeue.Config{
		LeaseDuration: config.LeaseDuration,
		MaxAttempts:   config.MaxAttempts,
	})
	if err != nil {
		return nil, fmt.Errorf("configure pull queue: %w", err)
	}

	return &Server{
		startTime:      time.Now(),
		port:           config.Port,
		token:          config.Token,
		cert:           config.Certificate,
		pullQueue:      pullQueue,
		events:         monitor.NewRecorder(100),
		leaseDuration:  config.LeaseDuration,
		maxAttempts:    config.MaxAttempts,
		workerActivity: make(map[string]time.Time),
	}, nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/worker/claim", auth.WithBearerAuth(s.token, s.handlePullClaim))
	mux.HandleFunc("/worker/renew", auth.WithBearerAuth(s.token, s.handlePullRenew))
	mux.HandleFunc("/worker/result", auth.WithBearerAuth(s.token, s.handlePullResult))
	mux.HandleFunc("/pull/submit", auth.WithBearerAuth(s.token, s.handlePullSubmit))
	mux.HandleFunc("/pull/job", auth.WithBearerAuth(s.token, s.handlePullJob))
	mux.HandleFunc("/status", auth.WithBearerAuth(s.token, s.handlePullStatus))
	return mux
}

func (s *Server) Start() error {
	listener, err := tls.Listen("tcp", fmt.Sprintf(":%d", s.port), &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("failed to create TLS listener: %w", err)
	}

	log.Printf("[coordinator] Starting HTTPS on :%d", s.port)
	log.Printf("[coordinator] Lease policy: duration=%s max_attempts=%d", s.leaseDuration, s.maxAttempts)
	log.Printf("[coordinator] Worker API: POST /worker/claim, /worker/renew, /worker/result")
	log.Printf("[coordinator] Client API: POST /pull/submit, GET /pull/job?id=..., GET /status")
	return http.Serve(listener, s.routes())
}

func (s *Server) recordWorkerActivity(workerID string) {
	if workerID == "" {
		return
	}
	s.workerMu.Lock()
	s.workerActivity[workerID] = time.Now()
	s.workerMu.Unlock()
}

func (s *Server) recordEvent(severity monitor.Severity, kind, message string, fields map[string]string) {
	s.events.Record(monitor.SystemEvent{
		Severity:  severity,
		Component: "coordinator",
		Kind:      kind,
		Message:   message,
		Fields:    fields,
	})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("[coordinator] Error writing response: %v", err)
	}
}
