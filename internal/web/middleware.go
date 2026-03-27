package web

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter tracks requests per IP.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]int
}

// NewRateLimiter initializes the limiter and starts the cleanup goroutine.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]int),
	}

	// Background Goroutine: Reset map every minute
	go rl.cleanupLoop()

	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	// We want this to run forever as long as the app runs
	for range ticker.C {
		rl.mu.Lock()
		// Recreate map to reset counts and prevent memory leaks
		rl.visitors = make(map[string]int)
		rl.mu.Unlock()
	}
}

// LimitMiddleware wraps an http.HandlerFunc to enforce IP rate limits.
func (rl *RateLimiter) LimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			// Fallback if IP is malformed
			ip = r.RemoteAddr
		}

		rl.mu.Lock()
		rl.visitors[ip]++
		count := rl.visitors[ip]
		rl.mu.Unlock()

		// Allow 50 requests per minute
		if count > 50 {
			log.Printf("[RateLimiter] BLOCKED IP: %s (Count: %d)", ip, count)
			http.Error(w, `{"error":"Rate limit exceeded. Try again later."}`, http.StatusTooManyRequests)
			return
		}

		// Proceed to handler
		next(w, r)
	}
}
