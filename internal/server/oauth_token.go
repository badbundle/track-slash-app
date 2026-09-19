package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type oauthErrorBody struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// writeOAuthError answers in the shape RFC 6749 section 5.2 defines. The
// trackslash error body ({"error":"not found"}) means something different to an
// OAuth client, so writeStoreError is deliberately not used on these routes.
func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeOAuthCORS(w)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, oauthErrorBody{Error: code, Description: description})
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return
	}
	client, _, ok := s.oauthAuthenticateClient(w, r)
	if !ok {
		return
	}

	switch grantType := r.PostForm.Get("grant_type"); grantType {
	case "authorization_code":
		s.oauthExchangeAuthorizationCode(w, r, client)
	case "refresh_token":
		s.oauthExchangeRefreshToken(w, r, client)
	default:
		s.oauthNoteAuthFailure(r, client.ClientID)
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"supported grant types are authorization_code and refresh_token")
	}
}

func (s *Server) oauthExchangeAuthorizationCode(w http.ResponseWriter, r *http.Request, client model.OAuthClient) {
	code := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")
	verifier := r.PostForm.Get("code_verifier")
	if code == "" || redirectURI == "" || verifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"code, redirect_uri and code_verifier are required")
		return
	}
	consumed, err := s.store.ConsumeOAuthAuthorizationCode(r.Context(), code)
	if err != nil {
		s.oauthNoteAuthFailure(r, client.ClientID)
		switch {
		case errors.Is(err, store.ErrOAuthReplay):
			// The store has already revoked everything this pair held.
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"this authorization code has already been used; all access for this application has been revoked")
		case errors.Is(err, store.ErrUnauthorized):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"the authorization code is invalid or has expired")
		default:
			writeInternalError(w, "oauth consume authorization code", err)
		}
		return
	}
	// A code is bound to the client it was issued to and the address it was
	// issued for. Both are re-checked here because the code alone must not be
	// enough to obtain a token.
	if consumed.ClientID != client.ID || consumed.RedirectURI != redirectURI {
		s.oauthNoteAuthFailure(r, client.ClientID)
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
			"the authorization code was not issued for this client and redirect URI")
		return
	}
	if !oauthVerifyPKCE(consumed.CodeChallenge, verifier) {
		s.oauthNoteAuthFailure(r, client.ClientID)
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "the code verifier does not match")
		return
	}

	issued, err := s.store.IssueOAuthTokens(r.Context(), store.IssueOAuthTokensParams{
		ClientID:   client.ID,
		ClientName: client.Name,
		UserID:     consumed.UserID,
		Scope:      consumed.Scope,
	})
	if err != nil {
		writeInternalError(w, "oauth issue tokens", err)
		return
	}
	writeOAuthTokens(w, issued, consumed.Scope)
}

func (s *Server) oauthExchangeRefreshToken(w http.ResponseWriter, r *http.Request, client model.OAuthClient) {
	refresh := r.PostForm.Get("refresh_token")
	if refresh == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	issued, err := s.store.RotateOAuthRefreshToken(r.Context(), refresh, client.ID)
	if err != nil {
		s.oauthNoteAuthFailure(r, client.ClientID)
		switch {
		case errors.Is(err, store.ErrOAuthReplay):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"this refresh token has already been used; all access for this application has been revoked")
		case errors.Is(err, store.ErrUnauthorized):
			writeOAuthError(w, http.StatusBadRequest, "invalid_grant",
				"the refresh token is invalid or has expired")
		default:
			writeInternalError(w, "oauth rotate refresh token", err)
		}
		return
	}
	writeOAuthTokens(w, issued, model.OAuthScopeMCP)
}

func writeOAuthTokens(w http.ResponseWriter, issued store.IssuedOAuthTokens, scope string) {
	writeOAuthCORS(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, oauthTokenResponse{
		AccessToken:  issued.AccessToken,
		TokenType:    "Bearer",
		ExpiresIn:    issued.ExpiresIn,
		RefreshToken: issued.RefreshToken,
		Scope:        scope,
	})
}

// oauthVerifyPKCE checks a code verifier against the S256 challenge recorded
// when the code was issued.
func oauthVerifyPKCE(challenge, verifier string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// oauthAuthenticateClient resolves the client behind a token or revocation
// request, from either a Basic header or form fields.
//
// Presenting both is rejected rather than resolved: RFC 6749 section 2.3 allows
// a client to use one method, and accepting two lets a caller probe which one
// the server actually honours.
func (s *Server) oauthAuthenticateClient(w http.ResponseWriter, r *http.Request) (model.OAuthClient, bool, bool) {
	var clientID, secret string
	usedBasic := false

	basicID, basicSecret, hasBasic := r.BasicAuth()
	formID := r.PostForm.Get("client_id")
	formSecret := r.PostForm.Get("client_secret")

	switch {
	case hasBasic && (formID != "" || formSecret != ""):
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"present client credentials once, either in the Authorization header or the form body")
		return model.OAuthClient{}, false, false
	case hasBasic:
		// RFC 6749 section 2.3.1 form-encodes both halves before base64.
		clientID, secret, usedBasic = basicID, basicSecret, true
		if decoded, err := decodeOAuthBasicField(basicID); err == nil {
			clientID = decoded
		}
		if decoded, err := decodeOAuthBasicField(basicSecret); err == nil {
			secret = decoded
		}
	default:
		clientID, secret = formID, formSecret
	}

	if clientID == "" || secret == "" {
		s.oauthNoteAuthFailure(r, clientID)
		writeOAuthUnauthorizedClient(w, usedBasic, "client authentication is required")
		return model.OAuthClient{}, usedBasic, false
	}
	if !s.oauthAuthAllowed(w, r, clientID) {
		return model.OAuthClient{}, usedBasic, false
	}
	client, err := s.store.AuthenticateOAuthClient(r.Context(), clientID, secret)
	if err != nil {
		if errors.Is(err, store.ErrUnauthorized) {
			s.oauthNoteAuthFailure(r, clientID)
			writeOAuthUnauthorizedClient(w, usedBasic, "client authentication failed")
			return model.OAuthClient{}, usedBasic, false
		}
		writeInternalError(w, "oauth authenticate client", err)
		return model.OAuthClient{}, usedBasic, false
	}
	return client, usedBasic, true
}

func decodeOAuthBasicField(value string) (string, error) {
	return url.QueryUnescape(value)
}

func writeOAuthUnauthorizedClient(w http.ResponseWriter, usedBasic bool, description string) {
	// RFC 6749 section 5.2: the challenge belongs in the response only when the
	// client actually tried to authenticate with that scheme.
	if usedBasic {
		w.Header().Set("WWW-Authenticate", `Basic realm="trackslash"`)
	}
	writeOAuthError(w, http.StatusUnauthorized, "invalid_client", description)
}

// oauthAuthAllowed throttles repeated failures rather than all traffic.
//
// Hosted clients refresh from a small pool of egress addresses shared by every
// user of an instance, so a per-IP budget spent by successful refreshes would
// throttle exactly the traffic that is working. The buckets are consumed only
// by oauthNoteAuthFailure, which means they count failures and a healthy client
// never approaches them.
func (s *Server) oauthAuthAllowed(w http.ResponseWriter, r *http.Request, clientID string) bool {
	// blocked, not allow: testing the budget here must not spend it, or the
	// successful refreshes this design exists to protect would pay for it.
	if stopped, retryAfter := s.authLimiter.byIP.blocked(oauthFailureKey(clientIP(r, s.trustedProxyCIDRs))); stopped {
		writeAuthRateLimit(w, retryAfter)
		return false
	}
	if stopped, retryAfter := s.authLimiter.byIdentifier.blocked(oauthFailureKey("client:" + clientID)); stopped {
		writeAuthRateLimit(w, retryAfter)
		return false
	}
	return true
}

// oauthNoteAuthFailure records one failed attempt against both buckets.
//
// fixedWindowLimiter.allow both tests and increments, so calling it only after a
// failure is what turns a general rate limit into a failure budget. The return
// value is ignored: the current request has already been answered, and the point
// is the count it leaves behind for the next one.
func (s *Server) oauthNoteAuthFailure(r *http.Request, clientID string) {
	s.authLimiter.byIP.allow(oauthFailureKey(clientIP(r, s.trustedProxyCIDRs)))
	if clientID != "" {
		s.authLimiter.byIdentifier.allow(oauthFailureKey("client:" + clientID))
	}
}

func oauthFailureKey(identifier string) string {
	return "oauth-failure:" + strings.ToLower(strings.TrimSpace(identifier))
}

func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "the request body could not be read")
		return
	}
	client, _, ok := s.oauthAuthenticateClient(w, r)
	if !ok {
		return
	}
	token := r.PostForm.Get("token")
	if token == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	if err := s.store.RevokeOAuthToken(r.Context(), token, client.ID); err != nil {
		writeInternalError(w, "oauth revoke token", err)
		return
	}
	// RFC 7009 section 2.2: an unknown or already-invalid token is a success.
	// Saying otherwise would let a client probe which tokens exist.
	writeOAuthCORS(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}
