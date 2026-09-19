package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

const (
	uiTokenRevealCookieName       = "track_slash_token_reveal"
	uiOAuthSecretRevealCookieName = "track_slash_oauth_reveal"
	uiTokenRevealPath             = "/tokens"
	uiTokenRevealMaxAge           = time.Minute
)

// A newly created API token is shown once and cannot be recovered from the
// database, which is the whole reason POST /tokens used to answer 200 with the
// page instead of redirecting. This carries the raw token across the redirect in
// a cookie that is HttpOnly, scoped to /tokens, expires in a minute, and is
// cleared the moment it is read.
//
// The value is bound to the session so a same-site attacker cannot plant a
// cookie that makes the page display a token of their choosing. DEPLOYMENT.md
// treats sibling subdomains as untrusted, and SameSite alone does not.
func (s *Server) setUITokenRevealCookie(w http.ResponseWriter, r *http.Request, rawToken string) {
	s.setUIRevealCookie(w, r, uiTokenRevealCookieName, rawToken)
}

// takeUITokenRevealCookie returns the raw token a preceding create stashed, and
// clears the cookie whether or not the value was usable.
func (s *Server) takeUITokenRevealCookie(w http.ResponseWriter, r *http.Request) string {
	return s.takeUIRevealCookie(w, r, uiTokenRevealCookieName)
}

// A client secret is subject to exactly the same constraint as an API token: it
// is hashed on the way in and can never be read back. The client ID rides along
// so the page can show the pair together, which is how it gets pasted into a
// connector's setup form. Raw values are base64url, so ":" separates them
// unambiguously.
func (s *Server) setUIOAuthSecretRevealCookie(w http.ResponseWriter, r *http.Request, clientID, secret string) {
	if clientID == "" || secret == "" {
		return
	}
	s.setUIRevealCookie(w, r, uiOAuthSecretRevealCookieName, clientID+":"+secret)
}

func (s *Server) takeUIOAuthSecretRevealCookie(w http.ResponseWriter, r *http.Request) (string, string) {
	value := s.takeUIRevealCookie(w, r, uiOAuthSecretRevealCookieName)
	clientID, secret, ok := strings.Cut(value, ":")
	if !ok || clientID == "" || secret == "" {
		return "", ""
	}
	return clientID, secret
}

func (s *Server) setUIRevealCookie(w http.ResponseWriter, r *http.Request, name, value string) {
	if value == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value + "." + uiRevealSignature(r, name, value),
		Path:     uiTokenRevealPath,
		MaxAge:   int(uiTokenRevealMaxAge.Seconds()),
		Expires:  time.Now().Add(uiTokenRevealMaxAge),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.secureCookies || r.TLS != nil,
	})
}

func (s *Server) takeUIRevealCookie(w http.ResponseWriter, r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil || cookie.Value == "" {
		return ""
	}
	s.clearUIRevealCookie(w, r, name)
	value, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || value == "" {
		return ""
	}
	if subtle.ConstantTimeCompare([]byte(signature), []byte(uiRevealSignature(r, name, value))) != 1 {
		return ""
	}
	return value
}

func (s *Server) clearUITokenRevealCookie(w http.ResponseWriter, r *http.Request) {
	s.clearUIRevealCookie(w, r, uiTokenRevealCookieName)
}

func (s *Server) clearUIRevealCookie(w http.ResponseWriter, r *http.Request, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     uiTokenRevealPath,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.secureCookies || r.TLS != nil,
	})
}

// uiRevealSignature binds a stashed value to the session that created it. The
// cookie name is part of the derivation so a value lifted from one reveal cookie
// cannot be replayed into the other.
func uiRevealSignature(r *http.Request, name, value string) string {
	cookie, err := r.Cookie(uiAuthCookieName)
	if err != nil {
		return ""
	}
	return uiDerivedCSRFToken(name+":"+value, cookie.Value)
}
