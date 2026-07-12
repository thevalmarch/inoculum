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

	"github.com/inoculum/internal/coordinator"
	"github.com/inoculum/internal/discovery"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port for the coordinator")
	strategy := flag.String("strategy", "round-robin", "Scheduling strategy: round-robin, least-busy")
	enableDiscovery := flag.Bool("discovery", true, "Enable UDP broadcast discovery for workers")
	flag.Parse()

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
	server := coordinator.NewServer(*port, sched)
	if err := server.Start(); err != nil {
		log.Fatalf("[coordinator] Fatal: %v", err)
	}
}
