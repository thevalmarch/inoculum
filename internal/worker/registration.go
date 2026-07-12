package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/inoculum/internal/types"
)

const (
	// HeartbeatInterval is how often the worker sends a heartbeat to the coordinator.
	HeartbeatInterval = 10 * time.Second
)

// Registration manages the worker's connection to the coordinator.
type Registration struct {
	workerID         string
	workerAddress    string
	coordinatorAddr  string
	hostname         string
}

// NewRegistration creates a registration manager.
func NewRegistration(workerID, workerAddress, coordinatorAddr string) *Registration {
	hostname, _ := os.Hostname()
	return &Registration{
		workerID:        workerID,
		workerAddress:   workerAddress,
		coordinatorAddr: coordinatorAddr,
		hostname:        hostname,
	}
}

// Register sends the initial registration request to the coordinator.
func (reg *Registration) Register() error {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	req := types.RegisterRequest{
		ID:       reg.workerID,
		Address:  reg.workerAddress,
		Hostname: reg.hostname,
		CPUCores: runtime.NumCPU(),
		RAMBytes: memStats.Sys,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	resp, err := http.Post(
		fmt.Sprintf("http://%s/register", reg.coordinatorAddr),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	defer resp.Body.Close()

	var regResp types.RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	if !regResp.OK {
		return fmt.Errorf("registration rejected: %s", regResp.Message)
	}

	log.Printf("[worker] Registered with coordinator at %s: %s", reg.coordinatorAddr, regResp.Message)
	return nil
}

// StartHeartbeat sends periodic heartbeat signals to the coordinator.
func (reg *Registration) StartHeartbeat(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				reg.sendHeartbeat()
			case <-stop:
				return
			}
		}
	}()
}

// sendHeartbeat sends a single heartbeat to the coordinator.
func (reg *Registration) sendHeartbeat() {
	req := types.HeartbeatRequest{ID: reg.workerID}
	body, err := json.Marshal(req)
	if err != nil {
		log.Printf("[worker] Heartbeat marshal error: %v", err)
		return
	}

	resp, err := http.Post(
		fmt.Sprintf("http://%s/heartbeat", reg.coordinatorAddr),
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		log.Printf("[worker] Heartbeat failed: %v", err)
		return
	}
	defer resp.Body.Close()

	var hbResp types.HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&hbResp); err != nil {
		log.Printf("[worker] Heartbeat decode error: %v", err)
		return
	}

	if !hbResp.OK {
		log.Printf("[worker] Heartbeat rejected — re-registering...")
		if err := reg.Register(); err != nil {
			log.Printf("[worker] Re-registration failed: %v", err)
		}
	}
}
