package app

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type clientRecord struct {
	count     int
	lastReset time.Time
}

// IPRateLimiter provides thread-safe in-memory rate limiting per IP address.
type IPRateLimiter struct {
	mu      sync.Mutex
	records map[string]*clientRecord
	limit   int
	window  time.Duration
}

// NewIPRateLimiter creates a new rate limiter with the specified limit and time window.
func NewIPRateLimiter(limit int, window time.Duration) *IPRateLimiter {
	limiter := &IPRateLimiter{
		records: make(map[string]*clientRecord),
		limit:   limit,
		window:  window,
	}

	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		for range ticker.C {
			limiter.cleanup()
		}
	}()

	return limiter
}

// Allow returns true if the client IP is within limits, incrementing their counter.
func (rl *IPRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rec, exists := rl.records[ip]
	if !exists || now.Sub(rec.lastReset) > rl.window {
		rl.records[ip] = &clientRecord{count: 1, lastReset: now}
		return true
	}

	if rec.count >= rl.limit {
		return false
	}

	rec.count++
	return true
}

func (rl *IPRateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, rec := range rl.records {
		if now.Sub(rec.lastReset) > rl.window*2 {
			delete(rl.records, ip)
		}
	}
}

// GetClientIP extracts the real client IP, respecting X-Forwarded-For or RemoteAddr.
func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip := strings.TrimSpace(xri)
		if ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
