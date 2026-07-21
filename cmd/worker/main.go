// Command worker starts an Inoculum worker daemon.
//
// The worker registers with the coordinator, sends heartbeats, and executes tasks.
//
// Usage:
//
//	worker [flags]
//
// Flags:
//
//	-port          HTTP port to listen on (default: 9000)
//	-coordinator   Coordinator address (host:port). If empty, uses UDP discovery.
//	-id            Worker ID (default: auto-generated from hostname)
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/inoculum/internal/audit"
	"github.com/inoculum/internal/crypto"
	"github.com/inoculum/internal/discovery"
	"github.com/inoculum/internal/worker"
)

func main() {
	port := flag.Int("port", 9000, "HTTP port for the worker")
	coordAddr := flag.String("coordinator", "", "Coordinator address (host:port). Leave empty for auto-discovery.")
	workerID := flag.String("id", "", "Worker ID (default: auto-generated)")
	concurrency := flag.Int("concurrency", 1, "Max concurrent tasks (default: 1)")
	allowedPathsStr := flag.String("allowed-paths", ".", "Comma-separated list of directories allowed for file_analyze")
	tokenFlag := flag.String("token", "", "Shared secret token for authentication")
	fingerprintFlag := flag.String("coordinator-fingerprint", "", "Optional: Pinned SHA-256 fingerprint of the coordinator's certificate")
	auditLog := flag.String("audit-log", "inoculum-audit.log", "Path to audit log file (JSON)")
	flag.Parse()

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("INOCULUM_TOKEN")
	}
	if token == "" {
		log.Fatalf("[worker] Fatal: -token flag or INOCULUM_TOKEN env var is required")
	}

	if err := audit.InitLogger(*auditLog); err != nil {
		log.Fatalf("Failed to initialize audit logger: %v", err)
	}
	log.Printf("[worker] Audit logging to %s", *auditLog)

	certFile := fmt.Sprintf(".inoculum-worker-%d-cert.pem", *port)
	keyFile := fmt.Sprintf(".inoculum-worker-%d-key.pem", *port)
	cert, fingerprint, err := crypto.GetOrGenerateCert(certFile, keyFile)
	if err != nil {
		log.Fatalf("[worker] Fatal: failed to get or generate cert: %v", err)
	}
	log.Printf("[worker] Loaded TLS certificate. Fingerprint: %s", fingerprint)

	// Generate worker ID if not provided
	if *workerID == "" {
		hostname, _ := os.Hostname()
		*workerID = fmt.Sprintf("worker-%s-%d", hostname, *port)
	}

	// Discover coordinator if address not provided
	if *coordAddr == "" {
		log.Printf("[worker] No coordinator address specified, attempting UDP discovery...")
		addr, err := discovery.DiscoverCoordinator()
		if err != nil {
			log.Fatalf("[worker] Could not find coordinator: %v\n"+
				"  Hint: either start the coordinator first, or use -coordinator=HOST:PORT", err)
		}
		*coordAddr = addr
	}

	// Determine the worker's externally-reachable address
	workerAddr := fmt.Sprintf("%s:%d", getLocalIP(), *port)

	// Register with the coordinator
	reg := worker.NewRegistration(*workerID, workerAddr, *coordAddr, token, *fingerprintFlag)
	if err := reg.Register(); err != nil {
		log.Fatalf("[worker] Registration failed: %v", err)
	}

	// Start heartbeat
	stop := make(chan struct{})
	reg.StartHeartbeat(stop)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		log.Printf("[worker] Shutting down...")
		close(stop)
		os.Exit(0)
	}()

	// Parse allowed paths
	var allowedPaths []string
	if *allowedPathsStr != "" {
		for _, p := range strings.Split(*allowedPathsStr, ",") {
			allowedPaths = append(allowedPaths, strings.TrimSpace(p))
		}
	}

	// Start the worker HTTP server
	srv := worker.NewServer(*port, *concurrency, allowedPaths, token, cert)
	if err := srv.Start(); err != nil {
		log.Fatalf("[worker] Fatal: %v", err)
	}
}

// getLocalIP returns the machine's LAN IP address.
func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
