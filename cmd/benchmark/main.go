// Command benchmark measures Inoculum's distributed performance vs sequential execution.
//
// It submits N tasks to the coordinator, measures wall-clock time, then runs the same
// tasks sequentially on the local machine, and prints a comparison table with speedup ratio.
//
// Usage:
//
//	benchmark [flags]
//
// Flags:
//
//	-coordinator   Coordinator address (default: localhost:8080)
//	-tasks         Number of tasks to submit (default: 10)
//	-type          Task type to benchmark (default: "dummy")
//	-input         Input for each task (default: "benchmark-payload")
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/inoculum/internal/auth"
	"github.com/inoculum/internal/crypto"
	"github.com/inoculum/internal/types"
	"github.com/inoculum/internal/worker"
)

func main() {
	coordAddr := flag.String("coordinator", "localhost:8080", "Coordinator address (host:port)")
	numTasks := flag.Int("tasks", 10, "Number of tasks to submit")
	taskType := flag.String("type", "dummy", "Task type to benchmark")
	input := flag.String("input", "benchmark-payload", "Input for each task")
	tokenFlag := flag.String("token", "", "Shared secret token for authentication")
	fingerprintFlag := flag.String("coordinator-fingerprint", "", "Optional: Pinned SHA-256 fingerprint of the coordinator's certificate")
	flag.Parse()

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("INOCULUM_TOKEN")
	}
	if token == "" {
		log.Fatalf("Fatal: -token flag or INOCULUM_TOKEN env var is required")
	}

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║           Inoculum Benchmark — Phase 4                  ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	// --- Distributed Execution ---
	fmt.Printf("▶ Submitting %d tasks (type: %s) to coordinator at %s...\n\n", *numTasks, *taskType, *coordAddr)

	inputs := make([]string, *numTasks)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("%s-%d", *input, i)
	}

	distStart := time.Now()
	distResp, err := submitJob(*coordAddr, *taskType, inputs, token, *fingerprintFlag)
	distDuration := time.Since(distStart)

	if err != nil {
		log.Fatalf("Distributed execution failed: %v", err)
	}

	fmt.Printf("  Distributed execution completed in %s\n", distDuration)
	fmt.Println()

	// Print per-task round-trip latencies
	fmt.Println("  Round-trip latencies:")
	var totalLatency time.Duration
	for _, rt := range distResp.RoundTrips {
		fmt.Printf("    Task %s → Worker %s: %s\n", rt.TaskID, rt.WorkerID, rt.LatencyS)
		totalLatency += rt.Latency
	}
	avgLatency := totalLatency / time.Duration(len(distResp.RoundTrips))
	fmt.Printf("    Average round-trip: %s\n\n", avgLatency)

	// Print per-task processing times
	fmt.Println("  Task processing times:")
	var totalProcessing time.Duration
	for _, r := range distResp.Results {
		fmt.Printf("    Task %s: %s", r.TaskID, r.DurationStr)
		if r.Error != "" {
			fmt.Printf(" (ERROR: %s)", r.Error)
		}
		fmt.Println()
		totalProcessing += r.Duration
	}
	fmt.Println()

	// --- Sequential Execution ---
	fmt.Printf("▶ Running %d tasks sequentially on local machine...\n\n", *numTasks)

	executor := worker.NewExecutor([]string{"."})
	seqStart := time.Now()
	for i, inp := range inputs {
		output, dur, err := executor.Execute(*taskType, inp)
		_ = output
		fmt.Printf("    Task %d: %s", i, dur)
		if err != nil {
			fmt.Printf(" (ERROR: %s)", err)
		}
		fmt.Println()
	}
	seqDuration := time.Since(seqStart)
	fmt.Printf("\n  Sequential execution completed in %s\n\n", seqDuration)

	// --- Comparison ---
	speedup := float64(seqDuration) / float64(distDuration)
	coordOverhead := distDuration - time.Duration(float64(totalProcessing)/float64(len(inputs)))

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║                   BENCHMARK RESULTS                     ║")
	fmt.Println("╠══════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Sequential time:      %-33s║\n", seqDuration)
	fmt.Printf("║  Distributed time:     %-33s║\n", distDuration)
	fmt.Printf("║  Speedup ratio:        %-33s║\n", fmt.Sprintf("%.2fx", speedup))
	fmt.Printf("║  Avg round-trip:       %-33s║\n", avgLatency)
	fmt.Printf("║  Coordinator overhead: %-33s║\n", coordOverhead)
	fmt.Printf("║  Tasks:                %-33s║\n", fmt.Sprintf("%d", *numTasks))
	fmt.Printf("║  Workers used:         %-33s║\n", fmt.Sprintf("%d", countUniqueWorkers(distResp.RoundTrips)))
	fmt.Println("╚══════════════════════════════════════════════════════════╝")

	if speedup > 1.0 {
		fmt.Printf("\n✅ Distributed execution was %.2fx FASTER than sequential.\n", speedup)
	} else {
		fmt.Printf("\n⚠️  Distributed execution was %.2fx SLOWER than sequential.\n", 1.0/speedup)
		fmt.Println("   This is expected with very fast dummy tasks due to network overhead.")
		fmt.Println("   Try with real workloads (type=file_analyze) for meaningful speedup.")
	}
}

// submitJob sends a job to the coordinator and returns the response.
func submitJob(coordAddr, taskType string, inputs []string, token, fingerprint string) (*types.SubmitJobResponse, error) {
	req := types.SubmitJobRequest{
		TaskType: taskType,
		Inputs:   inputs,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %w", err)
	}

	tlsConfig := crypto.NewTOFUClientConfig(coordAddr, fingerprint, ".inoculum-client-known-hosts")
	
	// Clone DefaultTransport to preserve HTTP/2 multiplexing and connection reuse,
	// avoiding massive TLS overhead on concurrent requests.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	transport.MaxIdleConnsPerHost = 100

	client := &http.Client{
		Timeout:   5 * time.Minute,
		Transport: transport,
	}

	httpReq, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://%s/submit-job", coordAddr), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Inoculum-Token", token)
	httpReq.Header.Set("X-Inoculum-Nonce", auth.GenerateNonce())
	httpReq.Header.Set("X-Inoculum-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coordinator returned status %d", resp.StatusCode)
	}

	var jobResp types.SubmitJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobResp); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}

	return &jobResp, nil
}

// countUniqueWorkers counts distinct workers used in round-trips.
func countUniqueWorkers(trips []types.RoundTrip) int {
	seen := make(map[string]bool)
	for _, rt := range trips {
		seen[rt.WorkerID] = true
	}
	return len(seen)
}
