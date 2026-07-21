package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ReplayWindow is the maximum age of a timestamp to be accepted.
const ReplayWindow = 30 * time.Second

// NonceCache stores seen nonces to prevent replay attacks.
type NonceCache struct {
	mu     sync.Mutex
	nonces map[string]int64 // map[nonce]expiryTime
	stop   chan struct{}
}

// NewNonceCache creates a new NonceCache and starts its cleanup goroutine.
func NewNonceCache(cleanupInterval time.Duration) *NonceCache {
	c := &NonceCache{
		nonces: make(map[string]int64),
		stop:   make(chan struct{}),
	}
	go c.cleanupLoop(cleanupInterval)
	return c
}

// Stop halts the background cleanup goroutine.
func (c *NonceCache) Stop() {
	close(c.stop)
}

// CheckAndAdd checks if a nonce has been seen. If not, it adds it and returns true.
// If it has been seen or the timestamp is outside the valid window, it returns false.
func (c *NonceCache) CheckAndAdd(nonce string, timestamp int64) bool {
	t := time.Unix(timestamp, 0)
	now := time.Now()

	// Reject if timestamp is too far in the past or future
	if now.Sub(t) > ReplayWindow || t.Sub(now) > ReplayWindow {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.nonces[nonce]; exists {
		return false // Replay detected
	}

	// Store nonce with an expiry time slightly larger than the replay window
	c.nonces[nonce] = now.Add(ReplayWindow * 2).Unix()
	return true
}

func (c *NonceCache) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			now := time.Now().Unix()
			c.mu.Lock()
			for nonce, expiry := range c.nonces {
				if now > expiry {
					delete(c.nonces, nonce)
				}
			}
			c.mu.Unlock()
		}
	}
}

// GenerateNonce creates a secure random nonce.
func GenerateNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
