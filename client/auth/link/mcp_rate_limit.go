package link

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// fixedWindowLimiter is a bounded in-memory fixed-window rate limiter used by
// the MCP link endpoints (per canonical user and per source IP). Bounded means
// bounded memory: expired windows are evicted opportunistically and the entry
// map never exceeds maxEntries.
type fixedWindowLimiter struct {
	window     time.Duration
	limit      int
	maxEntries int
	now        func() time.Time

	mu      sync.Mutex
	entries map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		window:     window,
		limit:      limit,
		maxEntries: 10000,
		now:        time.Now,
		entries:    map[string]*rateWindow{},
	}
}

// Allow reports whether one more event is admitted for key in the current
// window. An empty key is always admitted (callers rate other dimensions).
func (l *fixedWindowLimiter) Allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.start) >= l.window {
		if !ok && len(l.entries) >= l.maxEntries {
			l.evictExpiredLocked(now)
			if len(l.entries) >= l.maxEntries {
				// The table is saturated with live windows; refuse rather than
				// grow without bound. This throttles only during abuse storms.
				return false
			}
		}
		l.entries[key] = &rateWindow{start: now, count: 1}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	return true
}

func (l *fixedWindowLimiter) evictExpiredLocked(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.start) >= l.window {
			delete(l.entries, key)
		}
	}
}

// clientIPKey extracts a best-effort source-IP rate key. The connection peer
// address is authoritative; forwarded headers are untrusted for limiting.
func clientIPKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
