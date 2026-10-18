package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// Only a request that sends no credential reads as an anonymous visitor. A
// dead or malformed one is answered 401, so its client learns to sign in again
// or refresh instead of being shown the public subset as if nothing were wrong.
func TestRESTRefusesADeadCredentialRatherThanReadingAnonymously(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	if _, err := e.store.UpdateProjectAccessMode(e.ctx, e.projectID, model.ProjectAccessPublic); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	user, live := e.mustUserToken(t, "dead-token")
	past := time.Now().Add(-time.Hour)
	expired, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: user.ID, Kind: model.AuthTokenKindAPI, Name: "expired", ExpiresAt: &past})
	if err != nil {
		t.Fatalf("CreateAuthToken expired: %v", err)
	}
	revoked, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: user.ID, Kind: model.AuthTokenKindAPI, Name: "revoked"})
	if err != nil {
		t.Fatalf("CreateAuthToken revoked: %v", err)
	}
	if err := e.store.RevokeAuthToken(e.ctx, revoked.Token.ID); err != nil {
		t.Fatalf("RevokeAuthToken: %v", err)
	}

	paths := []string{"/projects", e.projectPath(), e.projectIssuesPath()}
	for name, authorization := range map[string]string{
		"expired":      "Bearer " + expired.RawToken,
		"revoked":      "Bearer " + revoked.RawToken,
		"unknown":      "Bearer not-a-token",
		"no token":     "Bearer",
		"other scheme": "Basic dXNlcjpwYXNzd29yZA==",
	} {
		for _, path := range paths {
			req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, e.ts.URL+apiPath(path), nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", authorization)
			res, err := e.ts.Client().Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			body := readBody(t, res)
			res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") {
				t.Fatalf("%s credential on %s = %d WWW-Authenticate %q body = %s, want 401 with a Bearer challenge",
					name, path, res.StatusCode, res.Header.Get("WWW-Authenticate"), body)
			}
		}
	}

	// No credential at all is still an anonymous reader of a public project,
	// and a live token still works.
	for _, path := range paths {
		if code, body := e.doUnauth(t, http.MethodGet, path, nil); code != http.StatusOK {
			t.Fatalf("anonymous %s = %d body = %s, want 200", path, code, body)
		}
		if code, body := e.doWithToken(t, live, http.MethodGet, path, nil); code != http.StatusOK {
			t.Fatalf("live token %s = %d body = %s, want 200", path, code, body)
		}
	}
}

// MCP never had an anonymous reader, and its 401 carries the challenge that
// sends an OAuth client to refresh or start over.
func TestMCPRefusesADeadCredentialWithAChallenge(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)
	past := time.Now().Add(-time.Hour)
	expired, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: e.user.ID, Kind: model.AuthTokenKindAPI, Name: "expired", ExpiresAt: &past})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	connector := e.exchange(t, e.approve(t, nil))
	if _, err := e.pool.Exec(e.ctx, `UPDATE auth_tokens SET expires_at = now() - INTERVAL '1 minute' WHERE kind = 'oauth' AND user_id = $1`, e.user.ID); err != nil {
		t.Fatalf("expire connector token: %v", err)
	}

	for name, token := range map[string]string{"expired API token": expired.RawToken, "expired connector token": connector["access_token"].(string)} {
		res := e.mcpCall(t, token)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Header.Get("WWW-Authenticate"), "resource_metadata=") {
			t.Fatalf("%s = %d WWW-Authenticate %q, want 401 with the resource metadata challenge", name, res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
	}
}
