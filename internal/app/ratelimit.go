package app

import (
	"net"
	"net/http"
	"os"
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

// GetClientIP trusts forwarding headers only from explicitly configured proxies.
// Walk X-Forwarded-For from the nearest hop to avoid trusting a spoofed prefix.
func GetClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	trusted := func(ip net.IP) bool {
		for _, cidr := range strings.Split(os.Getenv("TRUSTED_PROXY_CIDRS"), ",") {
			_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
			if err == nil && network.Contains(ip) {
				return true
			}
		}
		return false
	}
	peer := net.ParseIP(host)
	if peer == nil || !trusted(peer) {
		return host
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(parts[i]))
			if ip == nil {
				return host
			}
			if !trusted(ip) || i == 0 {
				return ip.String()
			}
		}
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return host
}
