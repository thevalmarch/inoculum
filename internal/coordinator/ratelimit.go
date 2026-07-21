package coordinator

import (
	"sync"
	"time"
)

// RateLimiter implements a simple token bucket rate limiter per IP.
type RateLimiter struct {
	mu       sync.Mutex
	limits   map[string]*tokenBucket
	rate     float64 // tokens to add per second
	capacity float64 // maximum burst size
}

type tokenBucket struct {
	tokens     float64
	lastUpdate time.Time
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(rate, capacity float64) *RateLimiter {
	return &RateLimiter{
		limits:   make(map[string]*tokenBucket),
		rate:     rate,
		capacity: capacity,
	}
}

// Allow checks if a request from the given IP is allowed.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	bucket, exists := rl.limits[ip]
	if !exists {
		rl.limits[ip] = &tokenBucket{
			tokens:     rl.capacity - 1, // Consume 1 token immediately
			lastUpdate: now,
		}
		return true
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(bucket.lastUpdate).Seconds()
	bucket.tokens += elapsed * rl.rate
	if bucket.tokens > rl.capacity {
		bucket.tokens = rl.capacity
	}
	bucket.lastUpdate = now

	// Check if there's at least 1 token
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}

	return false
}
