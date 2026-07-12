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
	"syscall"

	"github.com/inoculum/internal/discovery"
	"github.com/inoculum/internal/worker"
)

func main() {
	port := flag.Int("port", 9000, "HTTP port for the worker")
	coordAddr := flag.String("coordinator", "", "Coordinator address (host:port). Leave empty for auto-discovery.")
	workerID := flag.String("id", "", "Worker ID (default: auto-generated)")
	concurrency := flag.Int("concurrency", 1, "Max concurrent tasks (default: 1)")
	flag.Parse()

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
	reg := worker.NewRegistration(*workerID, workerAddr, *coordAddr)
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

	// Start the worker HTTP server
	srv := worker.NewServer(*port, *concurrency)
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
