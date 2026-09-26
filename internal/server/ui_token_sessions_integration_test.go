package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// Web sessions are numerous and their names carry no information, so a row each
// buried the API tokens people actually manage.
func TestTokensPageGroupsWebSessionsBehindOneAction(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustSessionToken(t, "session-grouping")
	e.mustNamedToken(t, user.ID, model.AuthTokenKindSession, "chrome-on-laptop")
	e.mustNamedToken(t, user.ID, model.AuthTokenKindAPI, "deploy bot")

	body := e.uiGet(t, "/tokens", session)

	for _, want := range []string{
		"API tokens",
		"Web sessions",
		"deploy bot",
		"2 active web sessions",
		`action="/token-sessions/revoke"`,
		"Revoke all web sessions",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("tokens page missing %q: %s", want, body)
		}
	}
	// The session rows themselves must be gone, along with their per-row action.
	if strings.Contains(body, "chrome-on-laptop") {
		t.Fatalf("tokens page still lists individual sessions: %s", body)
	}
}

// A revoked API token can never be used or restored, so the page drops it as
// soon as it is revoked rather than keeping a "revoked" row around for good.
func TestTokensPageDropsRevokedAPITokens(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustSessionToken(t, "revoked-api-tokens")
	e.mustNamedToken(t, user.ID, model.AuthTokenKindAPI, "deploy bot")
	stale := e.mustNamedToken(t, user.ID, model.AuthTokenKindAPI, "stale bot")

	revoke := func(id uuid.UUID) {
		t.Helper()
		form := url.Values{"csrf_token": {uiCSRFTokenForTest("session", session)}}
		res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/tokens/"+id.String()+"/revoke", session, strings.NewReader(form.Encode()), map[string]string{
			"Origin":       e.ts.URL,
			"X-CSRF-Token": "",
		})
		defer res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/tokens" {
			t.Fatalf("revoke code = %d location = %q body = %s", res.StatusCode, res.Header.Get("Location"), readBody(t, res))
		}
	}

	revoke(stale.ID)
	body := e.uiGet(t, "/tokens", session)
	if !strings.Contains(body, "deploy bot") || !strings.Contains(body, `action="/tokens/`) {
		t.Fatalf("tokens page lost the live API token: %s", body)
	}
	for _, notWant := range []string{"stale bot", `action="/tokens/` + stale.ID.String() + `/revoke"`, ">revoked<"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("tokens page still shows the revoked API token (%q): %s", notWant, body)
		}
	}
	if strings.Contains(body, "No active API tokens.") {
		t.Fatalf("tokens page shows the empty state beside a live token: %s", body)
	}

	// The row is hidden, not deleted.
	tokens, err := e.store.ListAuthTokens(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAuthTokens: %v", err)
	}
	var kept bool
	for _, token := range tokens {
		if token.ID == stale.ID {
			kept = token.RevokedAt != nil
		}
	}
	if !kept {
		t.Fatalf("revoked API token should stay in auth_tokens, revoked: %+v", tokens)
	}

	for _, token := range tokens {
		if token.Kind == model.AuthTokenKindAPI && token.RevokedAt == nil {
			revoke(token.ID)
		}
	}
	if body := e.uiGet(t, "/tokens", session); !strings.Contains(body, "No active API tokens.") || strings.Contains(body, "deploy bot") {
		t.Fatalf("tokens page should show the empty state once every API token is revoked: %s", body)
	}
}

func TestTokensPageCountsOnlyLiveSessions(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustSessionToken(t, "session-count")
	revoked := e.mustNamedToken(t, user.ID, model.AuthTokenKindSession, "old session")
	if err := e.store.RevokeAuthTokenForUser(e.ctx, user.ID, revoked.ID); err != nil {
		t.Fatalf("RevokeAuthTokenForUser: %v", err)
	}

	body := e.uiGet(t, "/tokens", session)
	if !strings.Contains(body, "1 active web session<") {
		t.Fatalf("expected a singular live-session count: %s", body)
	}
}

// Sessions expire long before the sweep revokes them, so an unrevoked session is
// not necessarily one you can still sign in with.
func TestTokensPageIgnoresExpiredSessions(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustSessionToken(t, "session-expired")
	e.mustExpiringSessionToken(t, user.ID, "expired session", time.Now().Add(-time.Hour))

	body := e.uiGet(t, "/tokens", session)
	if !strings.Contains(body, "1 active web session<") {
		t.Fatalf("expired session counted as active: %s", body)
	}
}

func TestTokensPageWithoutWebSessions(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, apiToken := e.mustUserToken(t, "session-empty")

	body := e.uiGet(t, "/tokens", apiToken)
	if !strings.Contains(body, "No active web sessions") {
		t.Fatalf("tokens page missing the empty session state: %s", body)
	}
	if strings.Contains(body, "Revoke all web sessions") {
		t.Fatalf("tokens page offers a bulk revoke with nothing to revoke: %s", body)
	}
}

// The action does what its label says, so it ends the caller's own session too.
func TestRevokingAllWebSessionsSignsTheCallerOut(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustSessionToken(t, "session-revoke")
	e.mustNamedToken(t, user.ID, model.AuthTokenKindSession, "another browser")
	apiToken := e.mustNamedToken(t, user.ID, model.AuthTokenKindAPI, "keep me")

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/token-sessions/revoke", session,
		strings.NewReader(url.Values{}.Encode()), map[string]string{"Origin": e.ts.URL})
	body := readBody(t, res)
	res.Body.Close()

	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Fatalf("code = %d location = %q body = %s", res.StatusCode, res.Header.Get("Location"), body)
	}

	tokens, err := e.store.ListAuthTokens(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAuthTokens: %v", err)
	}
	for _, token := range tokens {
		switch {
		case token.Kind == model.AuthTokenKindSession && token.RevokedAt == nil:
			t.Fatalf("session %s survived: %+v", token.Name, token)
		case token.ID == apiToken.ID && token.RevokedAt != nil:
			t.Fatalf("API token was revoked: %+v", token)
		}
	}

	// The now-revoked session cannot be used again.
	after := e.uiDoNoRedirect(t, http.MethodGet, "/tokens", session, nil)
	after.Body.Close()
	if after.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoked session still works: code = %d", after.StatusCode)
	}
}

// htmx posts the form too, and following a 303 there would swap the login page
// into the panel.
func TestRevokingAllWebSessionsUsesHXRedirect(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, session := e.mustSessionToken(t, "session-revoke-htmx")

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/token-sessions/revoke", session,
		strings.NewReader(url.Values{}.Encode()), map[string]string{"Origin": e.ts.URL, "HX-Request": "true"})
	res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", res.StatusCode)
	}
	if got := res.Header.Get("HX-Redirect"); got != "/login" {
		t.Fatalf("HX-Redirect = %q, want /login", got)
	}
}

func (e *httpEnv) mustSessionToken(t *testing.T, label string) (model.User, string) {
	t.Helper()
	user, _ := e.mustUserToken(t, label)
	created, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID: user.ID,
		Kind:   model.AuthTokenKindSession,
		Name:   "web session",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	return user, created.RawToken
}

func (e *httpEnv) mustExpiringSessionToken(t *testing.T, userID uuid.UUID, name string, expiresAt time.Time) model.AuthToken {
	t.Helper()
	created, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID:    userID,
		Kind:      model.AuthTokenKindSession,
		Name:      name,
		ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	return created.Token
}

func (e *httpEnv) mustNamedToken(t *testing.T, userID uuid.UUID, kind model.AuthTokenKind, name string) model.AuthToken {
	t.Helper()
	created, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID: userID,
		Kind:   kind,
		Name:   name,
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	return created.Token
}
