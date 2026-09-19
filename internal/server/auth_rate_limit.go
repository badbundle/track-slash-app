package server

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAuthIPAttempts         = 30
	defaultAuthIPWindow           = time.Minute
	defaultAuthIdentifierAttempts = 10
	defaultAuthIdentifierWindow   = 5 * time.Minute
	defaultAuthLimitEntries       = 10_000
)

type AuthRateLimitOptions struct {
	IPAttempts         int
	IPWindow           time.Duration
	IdentifierAttempts int
	IdentifierWindow   time.Duration
}

type authRateLimiter struct {
	byIP         *fixedWindowLimiter
	byIdentifier *fixedWindowLimiter
}

type fixedWindowLimiter struct {
	mu         sync.Mutex
	limit      int
	window     time.Duration
	maxEntries int
	now        func() time.Time
	entries    map[string]fixedWindowEntry
}

type fixedWindowEntry struct {
	count int
	reset time.Time
}

func newAuthRateLimiter(opts AuthRateLimitOptions) *authRateLimiter {
	if opts.IPAttempts <= 0 {
		opts.IPAttempts = defaultAuthIPAttempts
	}
	if opts.IPWindow <= 0 {
		opts.IPWindow = defaultAuthIPWindow
	}
	if opts.IdentifierAttempts <= 0 {
		opts.IdentifierAttempts = defaultAuthIdentifierAttempts
	}
	if opts.IdentifierWindow <= 0 {
		opts.IdentifierWindow = defaultAuthIdentifierWindow
	}
	return &authRateLimiter{
		byIP:         newFixedWindowLimiter(opts.IPAttempts, opts.IPWindow, defaultAuthLimitEntries, time.Now),
		byIdentifier: newFixedWindowLimiter(opts.IdentifierAttempts, opts.IdentifierWindow, defaultAuthLimitEntries, time.Now),
	}
}

func newFixedWindowLimiter(limit int, window time.Duration, maxEntries int, now func() time.Time) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit:      limit,
		window:     window,
		maxEntries: maxEntries,
		now:        now,
		entries:    make(map[string]fixedWindowEntry),
	}
}

func (l *fixedWindowLimiter) allow(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[key]
	if exists && !now.Before(entry.reset) {
		delete(l.entries, key)
		exists = false
	}
	if exists {
		if entry.count >= l.limit {
			return false, entry.reset.Sub(now)
		}
		entry.count++
		l.entries[key] = entry
		return true, 0
	}

	if len(l.entries) >= l.maxEntries {
		for candidate, candidateEntry := range l.entries {
			if !now.Before(candidateEntry.reset) {
				delete(l.entries, candidate)
			}
		}
		if len(l.entries) >= l.maxEntries {
			return false, l.window
		}
	}
	l.entries[key] = fixedWindowEntry{count: 1, reset: now.Add(l.window)}
	return true, 0
}

// blocked reports whether a key has already spent its budget, without spending
// any of it.
//
// allow both tests and increments, which is what an endpoint wants when every
// request should count. An endpoint that counts only failures has to ask the
// question on every request but answer for very few, and calling allow there
// would quietly charge the successes too.
func (l *fixedWindowLimiter) blocked(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, exists := l.entries[key]
	if !exists || !now.Before(entry.reset) {
		return false, 0
	}
	if entry.count >= l.limit {
		return true, entry.reset.Sub(now)
	}
	return false, 0
}

func (s *Server) authIPRateLimited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := s.authLimiter.byIP.allow(clientIP(r, s.trustedProxyCIDRs))
		if !allowed {
			writeAuthRateLimit(w, retryAfter)
			return
		}
		next(w, r)
	}
}

func (s *Server) authAccountRateLimited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowAuthIdentifier(w, currentUser(r).ID.String()) {
			return
		}
		next(w, r)
	}
}

func (s *Server) allowAuthIdentifier(w http.ResponseWriter, identifier string) bool {
	key := strings.ToLower(strings.TrimSpace(identifier))
	if key == "" {
		key = "unknown"
	}
	allowed, retryAfter := s.authLimiter.byIdentifier.allow(key)
	if !allowed {
		writeAuthRateLimit(w, retryAfter)
		return false
	}
	return true
}

func clientIP(r *http.Request, trustedProxyCIDRs []net.IPNet) string {
	remote := strings.TrimSpace(r.RemoteAddr)
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	peerIP := net.ParseIP(host)
	if peerIP == nil {
		if remote == "" {
			return "unknown"
		}
		return remote
	}
	peer := peerIP.String()
	if !ipInNetworks(peerIP, trustedProxyCIDRs) {
		return peer
	}

	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate := net.ParseIP(strings.TrimSpace(forwarded[i]))
		if candidate == nil {
			return peer
		}
		if !ipInNetworks(candidate, trustedProxyCIDRs) {
			return candidate.String()
		}
	}
	return peer
}

func ipInNetworks(ip net.IP, networks []net.IPNet) bool {
	for i := range networks {
		if networks[i].Contains(ip) {
			return true
		}
	}
	return false
}

func writeAuthRateLimit(w http.ResponseWriter, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retryAfter)))
	writeError(w, http.StatusTooManyRequests, "too many authentication attempts")
}

func retryAfterSeconds(duration time.Duration) int {
	seconds := int(math.Ceil(duration.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
