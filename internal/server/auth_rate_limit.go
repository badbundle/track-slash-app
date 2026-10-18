package server

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultAuthIPAttempts         = 30
	defaultAuthIPWindow           = time.Minute
	defaultAuthIdentifierAttempts = 10
	defaultAuthIdentifierWindow   = 5 * time.Minute
	// defaultAuthLimitEntries bounds each limiter's table. When it is full the
	// entry closest to expiry makes room, so a flood of new keys cannot turn
	// the limiter into a refusal for everyone else.
	defaultAuthLimitEntries = 100_000
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

// limiterKey stores a fixed-size digest of the caller's key. Keys come from
// request input such as usernames and client IDs, and keeping them verbatim
// would let a caller park megabytes in the table for a whole window.
func limiterKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

func (l *fixedWindowLimiter) allow(key string) (bool, time.Duration) {
	key = limiterKey(key)
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
		oldest, oldestReset := "", time.Time{}
		for candidate, candidateEntry := range l.entries {
			if !now.Before(candidateEntry.reset) {
				delete(l.entries, candidate)
				continue
			}
			if oldest == "" || candidateEntry.reset.Before(oldestReset) {
				oldest, oldestReset = candidate, candidateEntry.reset
			}
		}
		if len(l.entries) >= l.maxEntries {
			delete(l.entries, oldest)
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
	key = limiterKey(key)
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

// allowAuthIP spends one attempt from the request's IP budget, answering 429
// when it is gone, for handlers that rate-limit only some of their requests.
func (s *Server) allowAuthIP(w http.ResponseWriter, r *http.Request) bool {
	allowed, retryAfter := s.authLimiter.byIP.allow(clientIP(r, s.trustedProxyCIDRs))
	if !allowed {
		writeAuthRateLimit(w, retryAfter)
		return false
	}
	return true
}

// authIdentifierAllowed spends one attempt from an identifier's budget, for
// callers that answer without an http.ResponseWriter.
func (s *Server) authIdentifierAllowed(identifier string) bool {
	allowed, _ := s.authLimiter.byIdentifier.allow(strings.ToLower(strings.TrimSpace(identifier)))
	return allowed
}

func (s *Server) authAccountRateLimited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowAuthIdentifier(w, accountKey(currentUser(r).ID)) {
			return
		}
		next(w, r)
	}
}

// allowAuthIdentifier spends one attempt from the budget key names. Every
// identifier budget shares one table, so each key starts with its purpose's
// prefix (see accountKey and signupKey): some are built from request input,
// and an unprefixed one could spell another purpose's key and spend that
// budget instead.
func (s *Server) allowAuthIdentifier(w http.ResponseWriter, key string) bool {
	allowed, retryAfter := s.authLimiter.byIdentifier.allow(authIdentifierKey(key))
	if !allowed {
		writeAuthRateLimit(w, retryAfter)
		return false
	}
	return true
}

func authIdentifierKey(identifier string) string {
	key := strings.ToLower(strings.TrimSpace(identifier))
	if key == "" {
		key = "unknown"
	}
	return key
}

// accountKey is the budget for checks of a signed-in account's own
// credentials: reauthentication and password changes.
func accountKey(userID uuid.UUID) string {
	return "account:" + userID.String()
}

// signupKey is the budget for sign-ups under one username, which must already
// have passed store.NormalizeUsername.
func signupKey(username string) string {
	return "signup:" + username
}

// passwordLoginKey is the budget for password sign-ins to one username. It is
// spent only by failures (see passwordLoginBlocked and notePasswordLoginFailure),
// so an account's own successful sign-ins never use it up.
func passwordLoginKey(username string) string {
	return "password-login:" + authIdentifierKey(username)
}

// passwordLoginBlocked answers 429 when a username has used up its failed
// sign-in budget, without spending any of it.
func (s *Server) passwordLoginBlocked(w http.ResponseWriter, username string) bool {
	if stopped, retryAfter := s.authLimiter.byIdentifier.blocked(passwordLoginKey(username)); stopped {
		writeAuthRateLimit(w, retryAfter)
		return true
	}
	return false
}

func (s *Server) notePasswordLoginFailure(username string) {
	s.authLimiter.byIdentifier.allow(passwordLoginKey(username))
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
	peer := rateLimitAddress(peerIP)
	if !ipInNetworks(peerIP, trustedProxyCIDRs) {
		return peer
	}

	// A proxy may append its own X-Forwarded-For line rather than extend the
	// client's, so every line counts, in order.
	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate := net.ParseIP(strings.TrimSpace(forwarded[i]))
		if candidate == nil {
			return peer
		}
		if !ipInNetworks(candidate, trustedProxyCIDRs) {
			return rateLimitAddress(candidate)
		}
	}
	return peer
}

// rateLimitAddress is the unit an address is budgeted in: the address itself
// for IPv4, and its /64 for IPv6, since a single host is routinely handed a
// whole /64 and could otherwise use a fresh address for every attempt.
func rateLimitAddress(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String()
	}
	return (&net.IPNet{IP: ip.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
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
