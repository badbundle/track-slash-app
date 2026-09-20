package server_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
)

const (
	oauthTestRedirectURI = "https://claude.ai/api/mcp/auth_callback"
	oauthTestVerifier    = "a-code-verifier-long-enough-to-be-realistic-0123456789"
	// Same well-formed length as the real one, so a rejection can only come
	// from the comparison and never from the length check.
	oauthTestWrongVerifier = "a-code-verifier-long-enough-to-be-realistic-9876543210"
)

func oauthTestChallenge() string {
	sum := sha256.Sum256([]byte(oauthTestVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type oauthHTTPEnv struct {
	*httpEnv
	user         model.User
	sessionToken string
	client       store.CreatedOAuthClient
}

func newOAuthHTTPEnv(t *testing.T) *oauthHTTPEnv {
	t.Helper()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "oauth")
	created, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       user.ID,
		Name:         "Claude",
		RedirectURIs: []string{oauthTestRedirectURI},
	})
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}
	return &oauthHTTPEnv{httpEnv: e, user: user, sessionToken: token, client: created}
}

func (e *oauthHTTPEnv) authorizeQuery(overrides map[string]string) string {
	params := url.Values{
		"client_id":             {e.client.Client.ClientID},
		"redirect_uri":          {oauthTestRedirectURI},
		"response_type":         {"code"},
		"state":                 {"opaque-state"},
		"scope":                 {model.OAuthScopeMCP},
		"code_challenge":        {oauthTestChallenge()},
		"code_challenge_method": {"S256"},
	}
	for key, value := range overrides {
		if value == "" {
			params.Del(key)
			continue
		}
		params.Set(key, value)
	}
	return "/oauth/authorize?" + params.Encode()
}

// approve walks the consent screen the way a browser would and returns the
// authorization code handed back through the redirect.
func (e *oauthHTTPEnv) approve(t *testing.T, overrides map[string]string) string {
	t.Helper()
	query := e.authorizeQuery(overrides)
	page := e.uiGet(t, query, e.sessionToken)
	if !strings.Contains(page, "Connect Claude") {
		t.Fatalf("consent page missing: %s", page)
	}

	form := url.Values{
		"client_id":             {e.client.Client.ClientID},
		"redirect_uri":          {oauthTestRedirectURI},
		"response_type":         {"code"},
		"state":                 {"opaque-state"},
		"scope":                 {model.OAuthScopeMCP},
		"code_challenge":        {oauthTestChallenge()},
		"code_challenge_method": {"S256"},
		"approve":               {"1"},
		"csrf_token":            {uiCSRFTokenForTest("session", e.sessionToken)},
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth/authorize", e.sessionToken,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if location.Query().Get("state") != "opaque-state" {
		t.Fatalf("state was not round-tripped: %s", location)
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in %s", location)
	}
	return code
}

func (e *oauthHTTPEnv) postToken(t *testing.T, form url.Values, basic bool) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic {
		req.SetBasicAuth(url.QueryEscape(e.client.Client.ClientID), url.QueryEscape(e.client.RawSecret))
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /oauth/token: %v", err)
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		body = map[string]any{}
	}
	res.Body.Close()
	return res, body
}

func (e *oauthHTTPEnv) exchange(t *testing.T, code string) map[string]any {
	t.Helper()
	res, body := e.postToken(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthTestRedirectURI},
		"code_verifier": {oauthTestVerifier},
		"client_id":     {e.client.Client.ClientID},
		"client_secret": {e.client.RawSecret},
	}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("exchange code = %d body = %v", res.StatusCode, body)
	}
	return body
}

func TestOAuthAuthorizationCodeFlowEndToEnd(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	code := e.approve(t, nil)
	tokens := e.exchange(t, code)

	if tokens["token_type"] != "Bearer" || tokens["scope"] != model.OAuthScopeMCP {
		t.Fatalf("token response = %v", tokens)
	}
	if tokens["expires_in"] == nil || tokens["refresh_token"] == "" {
		t.Fatalf("token response missing expiry or refresh token: %v", tokens)
	}
	accessToken, _ := tokens["access_token"].(string)
	if accessToken == "" {
		t.Fatalf("no access token: %v", tokens)
	}

	// The whole point: the issued token reaches MCP as the approving user.
	if got := e.mcpUsername(t, accessToken); got != e.user.Username {
		t.Fatalf("MCP identity = %q, want %q", got, e.user.Username)
	}

	// A second authorization skips the consent screen, which is what makes
	// reconnecting painless.
	repeat := e.uiDoNoRedirect(t, http.MethodGet, e.authorizeQuery(nil), e.sessionToken, nil)
	defer repeat.Body.Close()
	if repeat.StatusCode != http.StatusSeeOther {
		t.Fatalf("remembered consent should redirect, got %d: %s", repeat.StatusCode, readBody(t, repeat))
	}
	// Unless the client explicitly asks to be asked again.
	forced := e.uiGet(t, e.authorizeQuery(map[string]string{"prompt": "consent"}), e.sessionToken)
	if !strings.Contains(forced, "Connect Claude") {
		t.Fatalf("prompt=consent should re-ask: %s", forced)
	}
}

// An API token was the only way in before OAuth existed and has to keep working.
func TestOAuthDoesNotDisturbAPITokenAuth(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	if got := e.mcpUsername(t, e.sessionToken); got != e.user.Username {
		t.Fatalf("API token identity = %q, want %q", got, e.user.Username)
	}
}

func TestOAuthAccessTokenDiesWithItsConnector(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	tokens := e.exchange(t, e.approve(t, nil))
	accessToken, _ := tokens["access_token"].(string)
	if got := e.mcpUsername(t, accessToken); got != e.user.Username {
		t.Fatalf("MCP identity = %q, want %q", got, e.user.Username)
	}

	form := url.Values{"csrf_token": {uiCSRFTokenForTest("session", e.sessionToken)}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost,
		"/oauth-clients/"+e.client.Client.ID.String()+"/revoke", e.sessionToken,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke code = %d", res.StatusCode)
	}

	if status := e.mcpStatus(t, accessToken); status != http.StatusUnauthorized {
		t.Fatalf("revoked connector token status = %d, want 401", status)
	}
}

// Nothing may be sent to an address the client did not register: an error
// delivered there would be an open redirector carrying the victim's state.
func TestOAuthAuthorizeRefusesToRedirectBeforeTheClientIsSettled(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	for _, tt := range []struct {
		name      string
		overrides map[string]string
		wantBody  string
	}{
		{name: "missing client", overrides: map[string]string{"client_id": ""}, wantBody: "Missing client"},
		{name: "unknown client", overrides: map[string]string{"client_id": "nope"}, wantBody: "Unknown client"},
		{name: "missing redirect", overrides: map[string]string{"redirect_uri": ""}, wantBody: "Redirect address not registered"},
		{name: "unregistered redirect", overrides: map[string]string{"redirect_uri": "https://evil.example.com/cb"}, wantBody: "Redirect address not registered"},
		// Exact matching means even a trailing character is a different address.
		{name: "near-miss redirect", overrides: map[string]string{"redirect_uri": oauthTestRedirectURI + "/"}, wantBody: "Redirect address not registered"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Deliberately not parallel: these share the env created above, whose
			// context has a deadline, and a parked subtest can outlive it.
			res := e.uiDoNoRedirect(t, http.MethodGet, e.authorizeQuery(tt.overrides), e.sessionToken, nil)
			defer res.Body.Close()
			body := readBody(t, res)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", res.StatusCode, body)
			}
			if location := res.Header.Get("Location"); location != "" {
				t.Fatalf("must not redirect, got Location %q", location)
			}
			if !strings.Contains(body, tt.wantBody) {
				t.Fatalf("body missing %q: %s", tt.wantBody, body)
			}
		})
	}
}

func TestOAuthAuthorizeReportsRecoverableErrorsToTheClient(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	for _, tt := range []struct {
		name      string
		overrides map[string]string
		wantError string
	}{
		{name: "implicit flow", overrides: map[string]string{"response_type": "token"}, wantError: "unsupported_response_type"},
		{name: "no pkce", overrides: map[string]string{"code_challenge": ""}, wantError: "invalid_request"},
		{name: "plain pkce", overrides: map[string]string{"code_challenge_method": "plain"}, wantError: "invalid_request"},
		{name: "absent pkce method", overrides: map[string]string{"code_challenge_method": ""}, wantError: "invalid_request"},
		{name: "unknown scope", overrides: map[string]string{"scope": "admin"}, wantError: "invalid_scope"},
		{name: "foreign resource", overrides: map[string]string{"resource": "https://evil.example.com/mcp"}, wantError: "invalid_target"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Deliberately not parallel: these share the env created above, whose
			// context has a deadline, and a parked subtest can outlive it.
			res := e.uiDoNoRedirect(t, http.MethodGet, e.authorizeQuery(tt.overrides), e.sessionToken, nil)
			defer res.Body.Close()
			if res.StatusCode != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303: %s", res.StatusCode, readBody(t, res))
			}
			location, err := url.Parse(res.Header.Get("Location"))
			if err != nil {
				t.Fatalf("parse Location: %v", err)
			}
			if !strings.HasPrefix(res.Header.Get("Location"), oauthTestRedirectURI) {
				t.Fatalf("errors must go to the registered address: %s", location)
			}
			if got := location.Query().Get("error"); got != tt.wantError {
				t.Fatalf("error = %q, want %q", got, tt.wantError)
			}
			// state must survive so the client can match the response to its
			// own request.
			if got := location.Query().Get("state"); got != "opaque-state" {
				t.Fatalf("state = %q, want it preserved", got)
			}
		})
	}
}

func TestOAuthAuthorizeDenyTellsTheClient(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	form := url.Values{
		"client_id":             {e.client.Client.ClientID},
		"redirect_uri":          {oauthTestRedirectURI},
		"response_type":         {"code"},
		"state":                 {"opaque-state"},
		"code_challenge":        {oauthTestChallenge()},
		"code_challenge_method": {"S256"},
		"approve":               {"0"},
		"csrf_token":            {uiCSRFTokenForTest("session", e.sessionToken)},
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth/authorize", e.sessionToken,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("deny code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	location, _ := url.Parse(res.Header.Get("Location"))
	if location.Query().Get("error") != "access_denied" {
		t.Fatalf("deny error = %q", location.Query().Get("error"))
	}
	// Denying must not leave a remembered approval behind.
	consented, err := e.store.OAuthClientConsented(e.ctx, e.client.Client.ID, e.user.ID, model.OAuthScopeMCP)
	if err != nil {
		t.Fatalf("OAuthClientConsented: %v", err)
	}
	if consented {
		t.Fatal("a denied request must not record consent")
	}
}

// A signed-out user is the common case: the connector sends them here first.
func TestOAuthAuthorizeSendsSignedOutUsersThroughLogin(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	query := e.authorizeQuery(nil)
	res := e.uiDoNoRedirect(t, http.MethodGet, query, "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", res.StatusCode, readBody(t, res))
	}
	location := res.Header.Get("Location")
	if !strings.HasPrefix(location, "/login?next=") {
		t.Fatalf("Location = %q, want a login redirect", location)
	}
	next, err := url.QueryUnescape(strings.TrimPrefix(location, "/login?next="))
	if err != nil {
		t.Fatalf("unescape next: %v", err)
	}
	// The whole request has to survive the round trip, or the connection fails
	// after sign-in with nothing to explain why.
	if next != query {
		t.Fatalf("next = %q, want %q", next, query)
	}
}

func TestOAuthConsentPageWidensFormActionForItsClient(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	res := e.uiDoNoRedirect(t, http.MethodGet, e.authorizeQuery(nil), e.sessionToken, nil)
	defer res.Body.Close()
	policy := res.Header.Get("Content-Security-Policy")
	if !strings.Contains(policy, "form-action 'self' https://claude.ai;") {
		t.Fatalf("consent page policy = %q", policy)
	}

	// Every other page keeps the strict policy.
	other := e.uiDoNoRedirect(t, http.MethodGet, "/tokens", e.sessionToken, nil)
	defer other.Body.Close()
	if got := other.Header.Get("Content-Security-Policy"); !strings.Contains(got, "form-action 'self';") {
		t.Fatalf("tokens page policy = %q", got)
	}
}

func TestOAuthAuthorizeRequiresCSRF(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	form := url.Values{
		"client_id":             {e.client.Client.ClientID},
		"redirect_uri":          {oauthTestRedirectURI},
		"response_type":         {"code"},
		"code_challenge":        {oauthTestChallenge()},
		"code_challenge_method": {"S256"},
		"approve":               {"1"},
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth/authorize", e.sessionToken,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", res.StatusCode, readBody(t, res))
	}
}

func TestOAuthTokenEndpointClientAuthentication(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	// client_secret_basic, the method most hosted clients prefer.
	code := e.approve(t, nil)
	res, body := e.postToken(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthTestRedirectURI},
		"code_verifier": {oauthTestVerifier},
	}, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("basic auth exchange = %d body = %v", res.StatusCode, body)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("token responses must not be cached, got %q", res.Header.Get("Cache-Control"))
	}

	// Presenting both methods at once is ambiguous and refused.
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/oauth/token",
		strings.NewReader(url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {"whatever"},
			"client_id":     {e.client.Client.ClientID},
			"client_secret": {e.client.RawSecret},
		}.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(e.client.Client.ClientID, e.client.RawSecret)
	both, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	both.Body.Close()
	if both.StatusCode != http.StatusBadRequest {
		t.Fatalf("dual client auth = %d, want 400", both.StatusCode)
	}
}

func TestOAuthTokenEndpointRejectsBadClientCredentials(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	for _, tt := range []struct {
		name          string
		clientID      string
		secret        string
		wantChallenge bool
		basic         bool
	}{
		{name: "wrong secret", clientID: e.client.Client.ClientID, secret: "wrong"},
		{name: "unknown client", clientID: "nope", secret: e.client.RawSecret},
		{name: "no credentials"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Deliberately not parallel: these share the env created above, whose
			// context has a deadline, and a parked subtest can outlive it.
			form := url.Values{"grant_type": {"authorization_code"}}
			if tt.clientID != "" {
				form.Set("client_id", tt.clientID)
				form.Set("client_secret", tt.secret)
			}
			res, body := e.postToken(t, form, false)
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 body = %v", res.StatusCode, body)
			}
			if body["error"] != "invalid_client" {
				t.Fatalf("error = %v, want invalid_client", body["error"])
			}
			// The challenge only belongs in the response when the client
			// actually tried that scheme.
			if got := res.Header.Get("WWW-Authenticate"); got != "" {
				t.Fatalf("form-auth failure should carry no Basic challenge, got %q", got)
			}
		})
	}
}

func TestOAuthTokenEndpointRejectsBadGrants(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		mutate    func(t *testing.T, e *oauthHTTPEnv, code string) url.Values
		wantError string
	}{
		{
			name: "pkce verifier mismatch",
			mutate: func(_ *testing.T, _ *oauthHTTPEnv, code string) url.Values {
				return url.Values{"grant_type": {"authorization_code"}, "code": {code},
					"redirect_uri": {oauthTestRedirectURI}, "code_verifier": {oauthTestWrongVerifier}}
			},
			wantError: "invalid_grant",
		},
		{
			name: "redirect uri mismatch",
			mutate: func(_ *testing.T, _ *oauthHTTPEnv, code string) url.Values {
				return url.Values{"grant_type": {"authorization_code"}, "code": {code},
					"redirect_uri": {"https://evil.example.com/cb"}, "code_verifier": {oauthTestVerifier}}
			},
			wantError: "invalid_grant",
		},
		{
			name: "unknown code",
			mutate: func(_ *testing.T, _ *oauthHTTPEnv, _ string) url.Values {
				return url.Values{"grant_type": {"authorization_code"}, "code": {"never-issued"},
					"redirect_uri": {oauthTestRedirectURI}, "code_verifier": {oauthTestVerifier}}
			},
			wantError: "invalid_grant",
		},
		{
			name: "missing parameters",
			mutate: func(_ *testing.T, _ *oauthHTTPEnv, _ string) url.Values {
				return url.Values{"grant_type": {"authorization_code"}}
			},
			wantError: "invalid_request",
		},
		{
			name: "missing refresh token",
			mutate: func(_ *testing.T, _ *oauthHTTPEnv, _ string) url.Values {
				return url.Values{"grant_type": {"refresh_token"}}
			},
			wantError: "invalid_request",
		},
		{
			name: "unknown refresh token",
			mutate: func(_ *testing.T, _ *oauthHTTPEnv, _ string) url.Values {
				return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"never-issued"}}
			},
			wantError: "invalid_grant",
		},
		{
			// The grant trackslash deliberately does not implement: it would
			// mean a token with no user behind it.
			name: "client credentials",
			mutate: func(_ *testing.T, _ *oauthHTTPEnv, _ string) url.Values {
				return url.Values{"grant_type": {"client_credentials"}}
			},
			wantError: "unsupported_grant_type",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newOAuthHTTPEnv(t)
			code := e.approve(t, nil)
			form := tt.mutate(t, e, code)
			form.Set("client_id", e.client.Client.ClientID)
			form.Set("client_secret", e.client.RawSecret)

			res, body := e.postToken(t, form, false)
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body = %v", res.StatusCode, body)
			}
			if body["error"] != tt.wantError {
				t.Fatalf("error = %v, want %q", body["error"], tt.wantError)
			}
		})
	}
}

// Replaying a code means a copy leaked, so everything it produced is revoked
// rather than the second attempt merely being refused.
func TestOAuthReplayedAuthorizationCodeRevokesTheGrant(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	code := e.approve(t, nil)
	tokens := e.exchange(t, code)
	accessToken, _ := tokens["access_token"].(string)
	if status := e.mcpStatus(t, accessToken); status != http.StatusOK {
		t.Fatalf("issued token status = %d, want 200", status)
	}

	res, body := e.postToken(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthTestRedirectURI},
		"code_verifier": {oauthTestVerifier},
		"client_id":     {e.client.Client.ClientID},
		"client_secret": {e.client.RawSecret},
	}, false)
	if res.StatusCode != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("replay = %d body = %v", res.StatusCode, body)
	}
	if status := e.mcpStatus(t, accessToken); status != http.StatusUnauthorized {
		t.Fatalf("token issued from a replayed code status = %d, want 401", status)
	}
}

func TestOAuthRefreshRotatesAndDetectsReuse(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	tokens := e.exchange(t, e.approve(t, nil))
	refresh, _ := tokens["refresh_token"].(string)

	res, rotated := e.postToken(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {e.client.Client.ClientID},
		"client_secret": {e.client.RawSecret},
	}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("refresh = %d body = %v", res.StatusCode, rotated)
	}
	rotatedAccess, _ := rotated["access_token"].(string)
	if rotated["refresh_token"] == refresh {
		t.Fatal("refresh tokens must rotate")
	}
	if status := e.mcpStatus(t, rotatedAccess); status != http.StatusOK {
		t.Fatalf("rotated access token status = %d, want 200", status)
	}

	// Using the retired token is how a leak shows itself.
	res, body := e.postToken(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {e.client.Client.ClientID},
		"client_secret": {e.client.RawSecret},
	}, false)
	if res.StatusCode != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("refresh reuse = %d body = %v", res.StatusCode, body)
	}
	if status := e.mcpStatus(t, rotatedAccess); status != http.StatusUnauthorized {
		t.Fatalf("access token after refresh reuse = %d, want 401", status)
	}
}

func TestOAuthRevocationEndpoint(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	tokens := e.exchange(t, e.approve(t, nil))
	accessToken, _ := tokens["access_token"].(string)

	revoke := func(t *testing.T, token string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/oauth/revoke",
			strings.NewReader(url.Values{
				"token":         {token},
				"client_id":     {e.client.Client.ClientID},
				"client_secret": {e.client.RawSecret},
			}.Encode()))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := e.ts.Client().Do(req)
		if err != nil {
			t.Fatalf("POST /oauth/revoke: %v", err)
		}
		res.Body.Close()
		return res
	}

	// RFC 7009: an unknown token is a success, so a client cannot probe which
	// tokens exist.
	if res := revoke(t, "never-issued"); res.StatusCode != http.StatusOK {
		t.Fatalf("unknown token revoke = %d, want 200", res.StatusCode)
	}
	if res := revoke(t, accessToken); res.StatusCode != http.StatusOK {
		t.Fatalf("revoke = %d, want 200", res.StatusCode)
	}
	if status := e.mcpStatus(t, accessToken); status != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", status)
	}
}

func TestOAuthRevocationRequiresAToken(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/oauth/revoke",
		strings.NewReader(url.Values{
			"client_id":     {e.client.Client.ClientID},
			"client_secret": {e.client.RawSecret},
		}.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /oauth/revoke: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

// mcpStatus reports what /mcp makes of a bearer token.
func (e *oauthHTTPEnv) mcpStatus(t *testing.T, token string) int {
	t.Helper()
	res := e.mcpCall(t, token)
	defer res.Body.Close()
	return res.StatusCode
}

func (e *oauthHTTPEnv) mcpUsername(t *testing.T, token string) string {
	t.Helper()
	res := e.mcpCall(t, token)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("MCP call status = %d body = %s", res.StatusCode, readBody(t, res))
	}
	body := readBody(t, res)
	var envelope struct {
		Result struct {
			StructuredContent struct {
				User struct {
					Username string `json:"username"`
				} `json:"user"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode MCP response: %v (%s)", err, body)
	}
	return envelope.Result.StructuredContent.User.Username
}

func (e *oauthHTTPEnv) mcpCall(t *testing.T, token string) *http.Response {
	t.Helper()
	return e.mcpTool(t, token, "track_get_me", "{}")
}

func (e *oauthHTTPEnv) mcpTool(t *testing.T, token, tool, arguments string) *http.Response {
	t.Helper()
	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/mcp", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	return res
}

// The 401 has to say where discovery starts, or a client that could complete
// the flow on its own has nothing to go on.
func TestMCPChallengePointsAtTheResourceMetadata(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	res := e.mcpCall(t, "not-a-token")
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
	challenge := res.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, `resource_metadata="`+e.ts.URL+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("challenge = %q", challenge)
	}
}

// The token endpoint budgets failures rather than traffic. Hosted clients
// refresh from a small pool of shared egress addresses, so a per-IP budget spent
// by successful refreshes would throttle exactly the traffic that is working.
func TestOAuthTokenEndpointBudgetsFailuresNotSuccesses(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, sessionToken := e.mustProjectMemberToken(t, "oauth-limit")
	created, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       user.ID,
		Name:         "Claude",
		RedirectURIs: []string{oauthTestRedirectURI},
	})
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}
	limited := &oauthHTTPEnv{httpEnv: e, user: user, sessionToken: sessionToken, client: created}

	// Far more successful exchanges than the identifier budget allows. If
	// success spent budget, the very next failure would already be throttled
	// instead of answered on its merits.
	for range 12 {
		limited.exchange(t, limited.approve(t, map[string]string{"prompt": "consent"}))
	}
	res, body := limited.postToken(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {"never-issued"},
		"client_id":     {created.Client.ClientID},
		"client_secret": {created.RawSecret},
	}, false)
	if res.StatusCode != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("successful exchanges must not spend the failure budget: %d %v", res.StatusCode, body)
	}

	// Now spend the identifier budget on failures alone.
	var sawRateLimit bool
	for range 12 {
		res, body := limited.postToken(t, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {"never-issued"},
			"client_id":     {created.Client.ClientID},
			"client_secret": {created.RawSecret},
		}, false)
		if res.StatusCode == http.StatusTooManyRequests {
			sawRateLimit = true
			if res.Header.Get("Retry-After") == "" {
				t.Fatalf("a throttled response must say when to retry: %v", body)
			}
			break
		}
	}
	if !sawRateLimit {
		t.Fatal("repeated failures should eventually be throttled")
	}
}

// RFC 6749 section 5.2 puts the challenge in the response only when the client
// actually tried that scheme.
func TestOAuthTokenEndpointChallengesOnlyBasicAuth(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/oauth/token",
		strings.NewReader(url.Values{"grant_type": {"authorization_code"}}.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(e.client.Client.ClientID, "wrong-secret")
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /oauth/token: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
	if got := res.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("WWW-Authenticate = %q, want a Basic challenge", got)
	}
}

// The per-IP budget is the backstop when an attacker rotates client IDs rather
// than hammering one.
func TestOAuthTokenEndpointThrottlesFailuresByIP(t *testing.T) {
	t.Parallel()
	base := newHTTPEnv(t)
	user, sessionToken := base.mustProjectMemberToken(t, "oauth-ip-limit")
	created, err := base.store.CreateOAuthClient(base.ctx, store.CreateOAuthClientParams{
		UserID:       user.ID,
		Name:         "Claude",
		RedirectURIs: []string{oauthTestRedirectURI},
	})
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}
	limited := newHTTPEnvWithOptions(t, server.Options{AuthRateLimit: server.AuthRateLimitOptions{
		IPAttempts:         2,
		IPWindow:           time.Minute,
		IdentifierAttempts: 100,
		IdentifierWindow:   5 * time.Minute,
	}})
	limited.store = base.store
	e := &oauthHTTPEnv{httpEnv: limited, user: user, sessionToken: sessionToken, client: created}

	var statuses []int
	for i := range 4 {
		// A different client ID each time, so only the IP bucket can stop this.
		res, _ := e.postToken(t, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {"never-issued"},
			"client_id":     {"unknown-client-" + strconv.Itoa(i)},
			"client_secret": {"nope"},
		}, false)
		statuses = append(statuses, res.StatusCode)
	}
	if statuses[len(statuses)-1] != http.StatusTooManyRequests {
		t.Fatalf("repeated failures from one IP should be throttled, got %v", statuses)
	}
}

// The hidden fields are as much user input on the way back as the query string
// was on the way in, so the decision handler re-validates rather than trusting
// what the page returned.
func TestOAuthAuthorizeDecisionRevalidatesItsForm(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	for _, tt := range []struct {
		name     string
		mutate   func(url.Values)
		wantCode int
		wantBody string
	}{
		{
			name:     "redirect swapped for an unregistered address",
			mutate:   func(f url.Values) { f.Set("redirect_uri", "https://evil.example.com/cb") },
			wantCode: http.StatusBadRequest,
			wantBody: "Redirect address not registered",
		},
		{
			name:     "client swapped",
			mutate:   func(f url.Values) { f.Set("client_id", "nope") },
			wantCode: http.StatusBadRequest,
			wantBody: "Unknown client",
		},
		{
			name:     "pkce stripped on the way back",
			mutate:   func(f url.Values) { f.Del("code_challenge") },
			wantCode: http.StatusSeeOther,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Deliberately not parallel: these share the env created above, whose
			// context has a deadline, and a parked subtest can outlive it.
			form := url.Values{
				"client_id":             {e.client.Client.ClientID},
				"redirect_uri":          {oauthTestRedirectURI},
				"response_type":         {"code"},
				"state":                 {"opaque-state"},
				"code_challenge":        {oauthTestChallenge()},
				"code_challenge_method": {"S256"},
				"approve":               {"1"},
				"csrf_token":            {uiCSRFTokenForTest("session", e.sessionToken)},
			}
			tt.mutate(form)
			res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth/authorize", e.sessionToken,
				strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
			defer res.Body.Close()
			body := readBody(t, res)
			if res.StatusCode != tt.wantCode {
				t.Fatalf("status = %d, want %d: %s", res.StatusCode, tt.wantCode, body)
			}
			if tt.wantBody != "" {
				if !strings.Contains(body, tt.wantBody) {
					t.Fatalf("body missing %q: %s", tt.wantBody, body)
				}
				if res.Header.Get("Location") != "" {
					t.Fatalf("must not redirect: %q", res.Header.Get("Location"))
				}
				return
			}
			// A recoverable problem still reports back to the registered
			// address, and never hands out a code.
			location, _ := url.Parse(res.Header.Get("Location"))
			if location.Query().Get("code") != "" {
				t.Fatalf("a tampered request must not yield a code: %s", location)
			}
			if location.Query().Get("error") != "invalid_request" {
				t.Fatalf("error = %q", location.Query().Get("error"))
			}
		})
	}
}

func TestOAuthRevocationRequiresClientAuthentication(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	tokens := e.exchange(t, e.approve(t, nil))
	accessToken, _ := tokens["access_token"].(string)

	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/oauth/revoke",
		strings.NewReader(url.Values{"token": {accessToken}}.Encode()))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /oauth/revoke: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
	// An unauthenticated request must not have revoked anything.
	if status := e.mcpStatus(t, accessToken); status != http.StatusOK {
		t.Fatalf("token status after a refused revoke = %d, want 200", status)
	}
}

// A connector acts for the user, but only while the user lets it. Minting an
// API token would escape that: the new token records no client, so revoking the
// connector could never reach it.
func TestConnectorTokenCannotManageCredentials(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)
	tokens := e.exchange(t, e.approve(t, nil))
	accessToken, _ := tokens["access_token"].(string)

	for _, tt := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{name: "mint an api token", method: http.MethodPost, path: "/me/tokens", body: map[string]any{"name": "persistence"}},
		{name: "list tokens", method: http.MethodGet, path: "/me/tokens"},
		{name: "mint a token for another user", method: http.MethodPost,
			path: "/users/" + e.user.ID.String() + "/tokens", body: map[string]any{"name": "persistence"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, body := e.doWithToken(t, accessToken, tt.method, tt.path, tt.body)
			if code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", code, body)
			}
		})
	}

	// The same request from the user's own API token still works, so the rule is
	// about the credential and not about the person.
	code, body := e.doWithToken(t, e.sessionToken, http.MethodPost, "/me/tokens", map[string]any{"name": "mine"})
	if code != http.StatusCreated {
		t.Fatalf("the user's own token should still mint: %d %s", code, body)
	}
}

// The CSRF token is derived from whatever sits in the session cookie, so a
// connector's access token placed there would otherwise drive the whole signed-in
// UI — including registering itself a second connector that no revocation reaches.
func TestConnectorTokenIsNotABrowserSession(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)
	tokens := e.exchange(t, e.approve(t, nil))
	accessToken, _ := tokens["access_token"].(string)

	res := e.uiDoNoRedirect(t, http.MethodGet, "/tokens", accessToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want a redirect to login: %s", res.StatusCode, readBody(t, res))
	}
	if location := res.Header.Get("Location"); !strings.HasPrefix(location, "/login") {
		t.Fatalf("Location = %q, want /login", location)
	}

	// And it cannot register a connector of its own.
	form := url.Values{
		"name":          {"second connector"},
		"redirect_uris": {oauthTestRedirectURI},
		"csrf_token":    {uiCSRFTokenForTest("session", accessToken)},
	}
	post := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients", accessToken,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL})
	post.Body.Close()
	// Bounced to the login page, exactly as the GET was — a 303 to /tokens here
	// would mean the registration went through.
	if post.StatusCode != http.StatusSeeOther || !strings.HasPrefix(post.Header.Get("Location"), "/login") {
		t.Fatalf("registration was not refused: %d %q", post.StatusCode, post.Header.Get("Location"))
	}
	clients, err := e.store.ListOAuthClientsForUser(e.ctx, e.user.ID)
	if err != nil {
		t.Fatalf("ListOAuthClientsForUser: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("connector count = %d, want the original one only", len(clients))
	}
}

// Burning another client's code would leave the rightful exchange looking like a
// replay, which revokes that client's whole grant. End to end over HTTP.
func TestOAuthForeignClientCannotBurnACode(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)
	attacker, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       e.user.ID,
		Name:         "Attacker",
		RedirectURIs: []string{oauthTestRedirectURI},
	})
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}

	code := e.approve(t, nil)
	res, body := e.postToken(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthTestRedirectURI},
		"code_verifier": {oauthTestVerifier},
		"client_id":     {attacker.Client.ClientID},
		"client_secret": {attacker.RawSecret},
	}, false)
	if res.StatusCode != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("foreign exchange = %d body = %v", res.StatusCode, body)
	}

	// The victim's code is untouched, so its own exchange still succeeds.
	tokens := e.exchange(t, code)
	accessToken, _ := tokens["access_token"].(string)
	if status := e.mcpStatus(t, accessToken); status != http.StatusOK {
		t.Fatalf("victim token status = %d, want 200", status)
	}
}

func TestOAuthRejectsMalformedPKCELengths(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	// A challenge outside RFC 7636's range is a bad request reported to the
	// client, not an internal error from the column constraint.
	res := e.uiDoNoRedirect(t, http.MethodGet, e.authorizeQuery(map[string]string{"code_challenge": "too-short"}), e.sessionToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("short challenge status = %d, want 303: %s", res.StatusCode, readBody(t, res))
	}
	location, _ := url.Parse(res.Header.Get("Location"))
	if location.Query().Get("error") != "invalid_request" {
		t.Fatalf("short challenge error = %q", location.Query().Get("error"))
	}

	// A short verifier would still hash and compare, quietly reducing PKCE to
	// decoration, so it is refused on its length.
	code := e.approve(t, nil)
	tokenRes, body := e.postToken(t, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthTestRedirectURI},
		"code_verifier": {"short"},
		"client_id":     {e.client.Client.ClientID},
		"client_secret": {e.client.RawSecret},
	}, false)
	if tokenRes.StatusCode != http.StatusBadRequest || body["error"] != "invalid_request" {
		t.Fatalf("short verifier = %d body = %v", tokenRes.StatusCode, body)
	}
}

// The refresh response reports the grant's own scope rather than assuming it.
//
// The grant is issued through the store with a scope the authorize endpoint
// would never mint, because a response that hardcodes the default scope is
// indistinguishable from a correct one whenever the grant happens to hold it.
func TestOAuthRefreshReportsTheGrantScope(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)

	const granted = "mcp:future-scope"
	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID:   e.client.Client.ID,
		ClientName: e.client.Client.Name,
		UserID:     e.user.ID,
		Scope:      granted,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}

	res, rotated := e.postToken(t, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {issued.RefreshToken},
		"client_id":     {e.client.Client.ClientID},
		"client_secret": {e.client.RawSecret},
	}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("refresh = %d body = %v", res.StatusCode, rotated)
	}
	if rotated["scope"] != granted {
		t.Fatalf("scope = %v, want %q", rotated["scope"], granted)
	}
}

// The credential boundary has to hold on the MCP surface too, not only REST.
func TestConnectorTokenCannotManageCredentialsOverMCP(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)
	tokens := e.exchange(t, e.approve(t, nil))
	accessToken, _ := tokens["access_token"].(string)

	for _, tool := range []struct {
		name      string
		arguments string
	}{
		{name: "track_create_my_token", arguments: `{"name":"persistence"}`},
		{name: "track_list_my_tokens", arguments: `{}`},
	} {
		t.Run(tool.name, func(t *testing.T) {
			res := e.mcpTool(t, accessToken, tool.name, tool.arguments)
			body := readBody(t, res)
			res.Body.Close()
			if !strings.Contains(body, "forbidden") {
				t.Fatalf("%s was not refused to a connector: %s", tool.name, body)
			}
		})
	}

	// The same tool still works for the user's own API token, so the boundary is
	// about the credential rather than the person.
	res := e.mcpTool(t, e.sessionToken, "track_create_my_token", `{"name":"mine"}`)
	body := readBody(t, res)
	res.Body.Close()
	if strings.Contains(body, "forbidden") {
		t.Fatalf("an API token should still be able to mint: %s", body)
	}
}
