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

	"github.com/inoculum/internal/auth"
	"github.com/inoculum/internal/crypto"
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
	token            string
	client           *http.Client
}

// NewRegistration creates a registration manager.
func NewRegistration(workerID, workerAddress, coordinatorAddr, token, coordFingerprint string) *Registration {
	hostname, _ := os.Hostname()
	
	tlsConfig := crypto.NewTOFUClientConfig(coordinatorAddr, coordFingerprint, ".inoculum-worker-known-hosts")
	
	// Clone DefaultTransport to preserve connection pooling and avoid TLS overhead.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	return &Registration{
		workerID:        workerID,
		workerAddress:   workerAddress,
		coordinatorAddr: coordinatorAddr,
		hostname:        hostname,
		token:           token,
		client:          client,
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

	reqHttp, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://%s/register", reg.coordinatorAddr), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	reqHttp.Header.Set("Content-Type", "application/json")
	reqHttp.Header.Set("X-Inoculum-Token", reg.token)
	reqHttp.Header.Set("X-Inoculum-Nonce", auth.GenerateNonce())
	reqHttp.Header.Set("X-Inoculum-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	resp, err := reg.client.Do(reqHttp)
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

	reqHttp, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://%s/heartbeat", reg.coordinatorAddr), bytes.NewReader(body))
	if err != nil {
		log.Printf("[worker] Heartbeat request creation failed: %v", err)
		return
	}
	reqHttp.Header.Set("Content-Type", "application/json")
	reqHttp.Header.Set("X-Inoculum-Token", reg.token)
	reqHttp.Header.Set("X-Inoculum-Nonce", auth.GenerateNonce())
	reqHttp.Header.Set("X-Inoculum-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	resp, err := reg.client.Do(reqHttp)
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
