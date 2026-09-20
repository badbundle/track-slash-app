package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

const oauthCodeChallengeMethodS256 = "S256"

// oauthAuthorizeRequest is an authorization request that has passed the checks
// which must happen before anything may be sent to the client's redirect URI.
type oauthAuthorizeRequest struct {
	Client        model.OAuthClient
	RedirectURI   string
	State         string
	Scope         string
	Resource      string
	CodeChallenge string
}

// uiOAuthAuthorize renders the consent screen, or skips it when this user has
// already approved this client.
func (s *Server) uiOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	req, ok := s.oauthResolveAuthorizeRequest(w, r, r.URL.Query())
	if !ok {
		return
	}
	user := currentUser(r)
	// prompt=consent lets a client deliberately re-ask, which is the only way
	// back to this screen once an approval is remembered.
	if r.URL.Query().Get("prompt") != "consent" {
		consented, err := s.store.OAuthClientConsented(r.Context(), req.Client.ID, user.ID, req.Scope)
		if err != nil {
			writeUIInternalError(w, "oauth authorize consent lookup", err)
			return
		}
		if consented {
			s.oauthIssueCodeAndRedirect(w, r, req)
			return
		}
	}
	s.renderOAuthConsent(w, r, req)
}

// uiOAuthAuthorizeDecision handles Allow and Deny.
//
// Every parameter is re-read from the form and re-validated rather than trusted
// because it made a round trip through the page: the hidden fields are as much
// user input on the way back as the query string was on the way in.
func (s *Server) uiOAuthAuthorizeDecision(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderOAuthError(w, r, http.StatusBadRequest, "Invalid request", "The authorization request could not be read.")
		return
	}
	req, ok := s.oauthResolveAuthorizeRequest(w, r, r.PostForm)
	if !ok {
		return
	}
	if r.PostForm.Get("approve") != "1" {
		oauthRedirectError(w, r, req.RedirectURI, req.State, "access_denied", "the user denied the request")
		return
	}
	s.oauthIssueCodeAndRedirect(w, r, req)
}

// oauthResolveAuthorizeRequest validates an authorization request in the order
// the spec demands.
//
// The client and redirect URI are settled first and, if either is wrong,
// nothing is sent to the redirect URI at all: an attacker who could get an
// error delivered to an address of their choosing would have an open redirector
// with the victim's state attached. Only once the destination is known to be one
// the client registered may a problem be reported by redirecting to it.
func (s *Server) oauthResolveAuthorizeRequest(w http.ResponseWriter, r *http.Request, params url.Values) (oauthAuthorizeRequest, bool) {
	clientID := strings.TrimSpace(params.Get("client_id"))
	if clientID == "" {
		s.renderOAuthError(w, r, http.StatusBadRequest, "Missing client",
			"This request did not say which application is asking for access.")
		return oauthAuthorizeRequest{}, false
	}
	client, err := s.store.GetOAuthClientByClientID(r.Context(), clientID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderOAuthError(w, r, http.StatusBadRequest, "Unknown client",
				"No application is registered with this client ID, or its access has been revoked.")
			return oauthAuthorizeRequest{}, false
		}
		writeUIInternalError(w, "oauth authorize client lookup", err)
		return oauthAuthorizeRequest{}, false
	}

	redirectURI := params.Get("redirect_uri")
	if redirectURI == "" || !client.AllowsRedirectURI(redirectURI) {
		s.renderOAuthError(w, r, http.StatusBadRequest, "Redirect address not registered",
			"This application asked to be sent back to an address it has not registered. "+
				"Add it to the application's redirect URIs, exactly as written, and try again.")
		return oauthAuthorizeRequest{}, false
	}

	state := params.Get("state")
	if responseType := params.Get("response_type"); responseType != "code" {
		oauthRedirectError(w, r, redirectURI, state, "unsupported_response_type",
			"only the authorization code response type is supported")
		return oauthAuthorizeRequest{}, false
	}
	challenge := strings.TrimSpace(params.Get("code_challenge"))
	if challenge == "" {
		oauthRedirectError(w, r, redirectURI, state, "invalid_request", "code_challenge is required")
		return oauthAuthorizeRequest{}, false
	}
	// Checked here as well as by the column constraint, so a malformed challenge
	// is reported to the client as a bad request instead of failing the insert
	// and surfacing as an internal error.
	if !oauthValidPKCELength(challenge) {
		oauthRedirectError(w, r, redirectURI, state, "invalid_request",
			"code_challenge must be 43 to 128 characters")
		return oauthAuthorizeRequest{}, false
	}
	// PKCE is mandatory and only S256 is accepted. "plain" offers no protection
	// against an intercepted code, which is the whole point of the exchange.
	if method := params.Get("code_challenge_method"); method != oauthCodeChallengeMethodS256 {
		oauthRedirectError(w, r, redirectURI, state, "invalid_request",
			"code_challenge_method must be S256")
		return oauthAuthorizeRequest{}, false
	}
	scope := strings.TrimSpace(params.Get("scope"))
	if scope == "" {
		scope = model.OAuthScopeMCP
	}
	if scope != model.OAuthScopeMCP {
		oauthRedirectError(w, r, redirectURI, state, "invalid_scope",
			"the only supported scope is "+model.OAuthScopeMCP)
		return oauthAuthorizeRequest{}, false
	}
	// RFC 8707. A client that names a resource must name this one, so a token
	// minted here can never be aimed somewhere else.
	resource := strings.TrimSpace(params.Get("resource"))
	if resource != "" && resource != s.uiRequestOrigin(r)+mcpPath {
		oauthRedirectError(w, r, redirectURI, state, "invalid_target",
			"the only supported resource is "+s.uiRequestOrigin(r)+mcpPath)
		return oauthAuthorizeRequest{}, false
	}

	return oauthAuthorizeRequest{
		Client:        client,
		RedirectURI:   redirectURI,
		State:         state,
		Scope:         scope,
		Resource:      resource,
		CodeChallenge: challenge,
	}, true
}

func (s *Server) oauthIssueCodeAndRedirect(w http.ResponseWriter, r *http.Request, req oauthAuthorizeRequest) {
	code, err := s.store.CreateOAuthAuthorizationCode(r.Context(), store.CreateOAuthAuthorizationCodeParams{
		ClientID:      req.Client.ID,
		UserID:        currentUser(r).ID,
		RedirectURI:   req.RedirectURI,
		CodeChallenge: req.CodeChallenge,
		Scope:         req.Scope,
		Resource:      req.Resource,
	})
	if err != nil {
		writeUIInternalError(w, "oauth authorize create code", err)
		return
	}
	target := oauthRedirectTarget(req.RedirectURI, map[string]string{
		"code":  code,
		"state": req.State,
	})
	allowOAuthFormAction(w, req.RedirectURI)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func oauthRedirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	target := oauthRedirectTarget(redirectURI, map[string]string{
		"error":             code,
		"error_description": description,
		"state":             state,
	})
	allowOAuthFormAction(w, redirectURI)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// oauthRedirectTarget appends parameters to a redirect URI, preserving any the
// client already put there. An empty value is omitted, so a request that
// carried no state does not come back with an empty one.
func oauthRedirectTarget(redirectURI string, params map[string]string) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		// Unreachable: the URI was parsed and exact-matched during registration.
		return redirectURI
	}
	query := parsed.Query()
	for key, value := range params {
		if value != "" {
			query.Set(key, value)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (s *Server) renderOAuthConsent(w http.ResponseWriter, r *http.Request, req oauthAuthorizeRequest) {
	redirectHost := req.RedirectURI
	if parsed, err := url.Parse(req.RedirectURI); err == nil {
		redirectHost = parsed.Host
	}
	allowOAuthFormAction(w, req.RedirectURI)
	renderUITemplate(w, http.StatusOK, "oauth-consent", uiOAuthConsentData{
		CSRFToken:     uiSessionCSRFToken(r),
		User:          currentUser(r),
		ClientName:    req.Client.Name,
		ClientID:      req.Client.ClientID,
		RedirectURI:   req.RedirectURI,
		RedirectHost:  redirectHost,
		State:         req.State,
		Scope:         req.Scope,
		Resource:      req.Resource,
		CodeChallenge: req.CodeChallenge,
	})
}

func (s *Server) renderOAuthError(w http.ResponseWriter, _ *http.Request, status int, title, message string) {
	renderUITemplate(w, status, "oauth-error", uiOAuthErrorData{Title: title, Message: message})
}
