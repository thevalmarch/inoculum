// Command coordinator starts the Inoculum coordinator daemon.
//
// The coordinator manages workers, distributes tasks, and collects results.
//
// Usage:
//
//	coordinator [flags]
//
// Flags:
//
//	-port        HTTP port to listen on (default: 8080)
//	-strategy    Scheduling strategy: "round-robin" or "least-busy" (default: "round-robin")
//	-discovery   Enable UDP broadcast discovery (default: true)
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/inoculum/internal/audit"
	"github.com/inoculum/internal/coordinator"
	"github.com/inoculum/internal/crypto"
	"github.com/inoculum/internal/discovery"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port for the coordinator")
	strategy := flag.String("strategy", "round-robin", "Scheduling strategy: round-robin, least-busy")
	enableDiscovery := flag.Bool("discovery", true, "Enable UDP broadcast discovery for workers")
	tokenFlag := flag.String("token", "", "Shared secret token for authentication")
	auditLog := flag.String("audit-log", "inoculum-audit.log", "Path to audit log file (JSON)")
	flag.Parse()

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("INOCULUM_TOKEN")
	}
	if token == "" {
		log.Fatalf("[coordinator] Fatal: -token flag or INOCULUM_TOKEN env var is required")
	}

	if err := audit.InitLogger(*auditLog); err != nil {
		log.Fatalf("Failed to initialize audit logger: %v", err)
	}
	log.Printf("[coordinator] Audit logging to %s", *auditLog)

	cert, fingerprint, err := crypto.GetOrGenerateCert(".inoculum-coordinator-cert.pem", ".inoculum-coordinator-key.pem")
	if err != nil {
		log.Fatalf("[coordinator] Fatal: failed to get or generate cert: %v", err)
	}
	log.Printf("[coordinator] Loaded TLS certificate. Fingerprint: %s", fingerprint)

	// Determine scheduling strategy
	var sched coordinator.ScheduleStrategy
	switch *strategy {
	case "least-busy":
		sched = coordinator.LeastBusy
		log.Printf("[coordinator] Using least-busy scheduling strategy")
	default:
		sched = coordinator.RoundRobin
		log.Printf("[coordinator] Using round-robin scheduling strategy")
	}

	// Start UDP discovery listener
	stop := make(chan struct{})
	if *enableDiscovery {
		discovery.StartListener(*port, stop)
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		log.Printf("[coordinator] Shutting down...")
		close(stop)
		os.Exit(0)
	}()

	// Start the coordinator server
	server := coordinator.NewServer(*port, sched, token, cert)
	if err := server.Start(); err != nil {
		log.Fatalf("[coordinator] Fatal: %v", err)
	}
}
