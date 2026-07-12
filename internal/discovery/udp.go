package discovery

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

const (
	// DiscoveryPort is the well-known UDP port for discovery broadcasts.
	DiscoveryPort = 9999
	// DiscoveryMessage is the magic string workers broadcast to find coordinators.
	DiscoveryMessage = "INOCULUM_DISCOVER"
	// DiscoveryResponsePrefix is the prefix of the coordinator's reply.
	DiscoveryResponsePrefix = "INOCULUM_COORDINATOR:"
	// DiscoveryTimeout is how long a worker waits for a discovery response.
	DiscoveryTimeout = 5 * time.Second
)

// StartListener starts the coordinator's UDP discovery listener.
// It responds to broadcast messages with the coordinator's HTTP address.
func StartListener(coordinatorPort int, stop <-chan struct{}) {
	addr := &net.UDPAddr{
		Port: DiscoveryPort,
		IP:   net.IPv4zero,
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		log.Printf("[discovery] Failed to start listener on port %d: %v", DiscoveryPort, err)
		return
	}

	log.Printf("[discovery] Listening for worker broadcasts on UDP port %d", DiscoveryPort)

	go func() {
		defer conn.Close()
		buf := make([]byte, 1024)

		for {
			select {
			case <-stop:
				return
			default:
			}

			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				log.Printf("[discovery] Read error: %v", err)
				continue
			}

			msg := strings.TrimSpace(string(buf[:n]))
			if msg == DiscoveryMessage {
				// Get the coordinator's LAN IP
				localIP := getOutboundIP()
				response := fmt.Sprintf("%s%s:%d", DiscoveryResponsePrefix, localIP, coordinatorPort)
				_, err := conn.WriteToUDP([]byte(response), remoteAddr)
				if err != nil {
					log.Printf("[discovery] Failed to respond to %s: %v", remoteAddr, err)
				} else {
					log.Printf("[discovery] Responded to worker at %s with address %s:%d", remoteAddr, localIP, coordinatorPort)
				}
			}
		}
	}()
}

// DiscoverCoordinator broadcasts on the LAN to find the coordinator.
// Returns the coordinator's HTTP address (host:port) or an error.
func DiscoverCoordinator() (string, error) {
	log.Printf("[discovery] Broadcasting to find coordinator on UDP port %d...", DiscoveryPort)

	// Create a UDP socket for broadcasting
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{
		IP:   net.IPv4bcast,
		Port: DiscoveryPort,
	})
	if err != nil {
		return "", fmt.Errorf("dial error: %w", err)
	}
	defer conn.Close()

	// Send discovery message
	_, err = conn.Write([]byte(DiscoveryMessage))
	if err != nil {
		return "", fmt.Errorf("write error: %w", err)
	}

	// Wait for response
	conn.SetReadDeadline(time.Now().Add(DiscoveryTimeout))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return "", fmt.Errorf("no coordinator found (timeout): %w", err)
	}

	response := strings.TrimSpace(string(buf[:n]))
	if !strings.HasPrefix(response, DiscoveryResponsePrefix) {
		return "", fmt.Errorf("invalid discovery response: %s", response)
	}

	coordAddr := strings.TrimPrefix(response, DiscoveryResponsePrefix)
	log.Printf("[discovery] Found coordinator at %s", coordAddr)
	return coordAddr, nil
}

// getOutboundIP returns the preferred outbound IP of this machine.
func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
