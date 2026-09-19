package server

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	contentSecurityPolicyFormAction = "form-action 'self'"
	contentSecurityPolicy           = "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; " + contentSecurityPolicyFormAction + "; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; media-src 'self'; frame-src 'none'; worker-src 'self'; manifest-src 'self'"
	permissionsPolicy               = "camera=(), geolocation=(), microphone=(), payment=(), usb=()"
	strictTransportPolicy           = "max-age=31536000"
)

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", contentSecurityPolicy)
		headers.Set("Permissions-Policy", permissionsPolicy)
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		if s.httpsDeployment {
			headers.Set("Strict-Transport-Security", strictTransportPolicy)
		}
		next.ServeHTTP(w, r)
	})
}

// allowOAuthFormAction widens form-action for a single response, to the single
// origin a registered OAuth client asked to be sent back to.
//
// The consent screen is the one page in trackslash whose form is meant to end up
// somewhere else. Chromium and WebKit apply form-action to every hop of a form
// submission's redirect chain, so under the global `form-action 'self'` the
// browser blocks the hand-off to the client and the connection silently fails.
//
// redirectURI has already been matched exactly against the client's registered
// list by the time this is called, so the origin added here is one a signed-in
// user of this instance chose, never raw request input. Only the form-action
// directive changes; everything else in the policy is left exactly as the
// middleware set it. It must be called before the response is written.
func allowOAuthFormAction(w http.ResponseWriter, redirectURI string) {
	origin := oauthRedirectOrigin(redirectURI)
	if origin == "" {
		return
	}
	current := w.Header().Get("Content-Security-Policy")
	if current == "" {
		return
	}
	w.Header().Set("Content-Security-Policy", strings.Replace(
		current,
		contentSecurityPolicyFormAction,
		contentSecurityPolicyFormAction+" "+origin,
		1,
	))
}

func oauthRedirectOrigin(redirectURI string) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
