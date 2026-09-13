package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	uiCSRFHeaderName        = "X-CSRF-Token"
	uiCSRFFormName          = "csrf_token"
	uiPreAuthCSRFCookieName = "track_slash_login_csrf"
	uiPreAuthCSRFMaxAge     = time.Hour
	// uiOpaqueOrigin is how a browser serializes an origin it will not disclose.
	// It says nothing about where the request came from, so it is not a foreign
	// origin — see uiCSRFSourceAllowed.
	uiOpaqueOrigin = "null"
)

var errUICSRFRandom = errors.New("generate CSRF token")

type uiCSRFContextKey struct{}

func uiDerivedCSRFToken(purpose, secret string) string {
	digest := sha256.Sum256([]byte("track-slash csrf " + purpose + ":" + secret))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func uiSessionCSRFToken(r *http.Request) string {
	if token, ok := r.Context().Value(uiCSRFContextKey{}).(string); ok && token != "" {
		return token
	}
	cookie, err := r.Cookie(uiAuthCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return ""
	}
	return uiDerivedCSRFToken("session", cookie.Value)
}

func (s *Server) ensureUIPreAuthCSRFToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if token, ok := r.Context().Value(uiCSRFContextKey{}).(string); ok && token != "" {
		return token, nil
	}
	if cookie, err := r.Cookie(uiPreAuthCSRFCookieName); err == nil && validUIPreAuthCSRFSecret(cookie.Value) {
		return uiDerivedCSRFToken("pre-auth", cookie.Value), nil
	}
	secretBytes := make([]byte, 32)
	random := s.csrfRandom
	if random == nil {
		random = rand.Reader
	}
	if _, err := io.ReadFull(random, secretBytes); err != nil {
		return "", errors.Join(errUICSRFRandom, err)
	}
	secret := base64.RawURLEncoding.EncodeToString(secretBytes)
	http.SetCookie(w, &http.Cookie{
		Name:     uiPreAuthCSRFCookieName,
		Value:    secret,
		Path:     "/",
		MaxAge:   int(uiPreAuthCSRFMaxAge.Seconds()),
		Expires:  time.Now().Add(uiPreAuthCSRFMaxAge),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.secureCookies || r.TLS != nil,
	})
	return uiDerivedCSRFToken("pre-auth", secret), nil
}

func validUIPreAuthCSRFSecret(secret string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	return err == nil && len(decoded) == 32
}

func (s *Server) clearUIPreAuthCSRFCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     uiPreAuthCSRFCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.secureCookies || r.TLS != nil,
	})
}

func (s *Server) uiPreAuthCSRFMiddleware(next http.Handler) http.Handler {
	return s.uiCSRFMiddleware(func(r *http.Request) string {
		cookie, err := r.Cookie(uiPreAuthCSRFCookieName)
		if err != nil || !validUIPreAuthCSRFSecret(cookie.Value) {
			return ""
		}
		return uiDerivedCSRFToken("pre-auth", cookie.Value)
	})(next)
}

func (s *Server) uiSessionCSRFMiddleware(next http.Handler) http.Handler {
	return s.uiCSRFMiddleware(uiSessionCSRFToken)(next)
}

// uiLogoutCSRFMiddleware enforces the session CSRF check only while there is a
// session to protect.
//
// Signing out is the one unsafe route reachable without the auth middleware, so
// it is the only one a browser can post to after its session cookie is gone.
// uiSessionCSRFToken then derives nothing, the check fails on the expected side
// rather than the supplied side, and the visitor is told "CSRF validation
// failed." for the harmless act of signing out when already signed out. A tab
// that outlives its cookie and a second click on Sign out both land here.
//
// With no cookie there is no session to destroy and nothing for an attacker to
// gain, so the request proceeds and the handler sends the browser to /login.
// A request that does carry a session is still checked: forcing a signed-in
// user to sign out is a real, if minor, cross-site nuisance.
func (s *Server) uiLogoutCSRFMiddleware(next http.Handler) http.Handler {
	guarded := s.uiSessionCSRFMiddleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uiSessionCSRFToken(r) == "" {
			next.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

func (s *Server) uiCSRFMiddleware(expectedToken func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !uiUnsafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			expected := expectedToken(r)
			provided := uiRequestCSRFToken(r)
			if expected == "" || provided == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 || !s.uiCSRFSourceAllowed(r) {
				http.Error(w, "CSRF validation failed.", http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), uiCSRFContextKey{}, expected)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func uiUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func uiRequestCSRFToken(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get(uiCSRFHeaderName)); token != "" {
		return token
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return ""
	}
	if err := r.ParseForm(); err != nil {
		return ""
	}
	return strings.TrimSpace(r.Form.Get(uiCSRFFormName))
}

func (s *Server) uiCSRFSourceAllowed(r *http.Request) bool {
	expected := s.publicOrigin
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	// Every page is served with Referrer-Policy: no-referrer, and that policy
	// makes a browser serialize the origin of a plain form submission as the
	// opaque origin and send no Referer either. fetch and XHR keep the real
	// origin, so only the forms that post without htmx — sign out, password
	// sign-in, sign-up, the settings forms — arrive this way. Treating the
	// opaque origin as a mismatch rejected all of them; treating it as
	// unstated hands the decision to Sec-Fetch-Site, which still reports
	// cross-site for the post this check exists to stop.
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && origin != uiOpaqueOrigin {
		return uiSameOrigin(origin, expected)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return uiSameOrigin(referer, expected)
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "", "same-origin", "none":
		return true
	default:
		return false
	}
}

func uiSameOrigin(candidate, expected string) bool {
	candidateURL, err := url.Parse(candidate)
	if err != nil || candidateURL.User != nil || candidateURL.Scheme == "" || candidateURL.Host == "" {
		return false
	}
	expectedURL, err := url.Parse(expected)
	if err != nil || expectedURL.User != nil || expectedURL.Scheme == "" || expectedURL.Host == "" {
		return false
	}
	return strings.EqualFold(candidateURL.Scheme, expectedURL.Scheme) && strings.EqualFold(candidateURL.Host, expectedURL.Host)
}
