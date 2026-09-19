package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/store"
)

const uiOAuthRevealCookieNameForTest = "track_slash_oauth_reveal"

var uiOAuthSecretPattern = regexp.MustCompile(`<div class="mt-3 text-xs font-medium">Client secret</div>\s*<code[^>]*>([^<]+)</code>`)

func TestUITokensPageRegistersAndRevokesAConnector(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-oauth")
	csrfToken := uiCSRFTokenForTest("session", token)

	body := e.uiGet(t, "/tokens", token)
	for _, want := range []string{"Connectors", `data-modal-open="oauth-client-create"`, "Register a connector", "No connectors registered yet."} {
		if !strings.Contains(body, want) {
			t.Fatalf("tokens page missing %q: %s", want, body)
		}
	}

	// A malformed redirect URI is caught at registration, where it can be
	// explained, rather than at authorization time where it reads as a bare
	// "not registered".
	invalid := url.Values{"name": {"Claude"}, "redirect_uris": {"not-a-uri"}, "csrf_token": {csrfToken}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients", token,
		strings.NewReader(invalid.Encode()), map[string]string{"Origin": e.ts.URL})
	invalidBody := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(invalidBody, "must be absolute") {
		t.Fatalf("invalid redirect URI code = %d body = %s", res.StatusCode, invalidBody)
	}
	if !strings.Contains(invalidBody, `id="oauth-client-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("a rejected registration should keep the modal open: %s", invalidBody)
	}

	form := url.Values{
		"name":          {"Claude"},
		"redirect_uris": {"https://claude.ai/api/mcp/auth_callback"},
		"csrf_token":    {csrfToken},
	}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients", token,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	defer res.Body.Close()
	// Post/Redirect/Get, so a reload cannot register a second connector.
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/tokens" {
		t.Fatalf("register code = %d location = %q", res.StatusCode, res.Header.Get("Location"))
	}
	reveal := findUICookieOrNil(res.Cookies(), uiOAuthRevealCookieNameForTest)
	if reveal == nil {
		t.Fatalf("register set no reveal cookie: %+v", res.Cookies())
	}

	revealed := e.uiGetWithCookies(t, "/tokens", token, reveal)
	revealedBody := readBody(t, revealed)
	revealed.Body.Close()
	if !strings.Contains(revealedBody, "Copy the client secret now.") {
		t.Fatalf("secret was not revealed: %s", revealedBody)
	}
	match := uiOAuthSecretPattern.FindStringSubmatch(revealedBody)
	if match == nil {
		t.Fatalf("client secret not rendered: %s", revealedBody)
	}
	secret := match[1]

	// The secret is hashed on the way in, so a second look cannot show it.
	after := e.uiGet(t, "/tokens", token)
	if strings.Contains(after, secret) || strings.Contains(after, "Copy the client secret now.") {
		t.Fatalf("client secret shown after its one-time response: %s", after)
	}
	if !strings.Contains(after, "Claude") || !strings.Contains(after, "https://claude.ai/api/mcp/auth_callback") {
		t.Fatalf("connector row missing: %s", after)
	}

	clients, err := e.store.ListOAuthClientsForUser(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListOAuthClientsForUser: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("clients = %+v", clients)
	}
	// The revealed secret really is this client's.
	if _, err := e.store.AuthenticateOAuthClient(e.ctx, clients[0].ClientID, secret); err != nil {
		t.Fatalf("AuthenticateOAuthClient with the revealed secret: %v", err)
	}

	revokeForm := url.Values{"csrf_token": {csrfToken}}
	res2 := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients/"+clients[0].ID.String()+"/revoke", token,
		strings.NewReader(revokeForm.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	res2.Body.Close()
	if res2.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke code = %d", res2.StatusCode)
	}
	remaining, err := e.store.ListOAuthClientsForUser(e.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListOAuthClientsForUser: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("revoked connector still listed: %+v", remaining)
	}
}

func TestUIConnectorsAreScopedToTheirOwner(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	owner, ownerToken := e.mustProjectMemberToken(t, "ui-oauth-owner")
	_, otherToken := e.mustProjectMemberToken(t, "ui-oauth-other")

	created, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       owner.ID,
		Name:         "Owner connector",
		RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"},
	})
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}

	if body := e.uiGet(t, "/tokens", otherToken); strings.Contains(body, "Owner connector") {
		t.Fatalf("another user's connector is listed: %s", body)
	}

	form := url.Values{"csrf_token": {uiCSRFTokenForTest("session", otherToken)}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients/"+created.Client.ID.String()+"/revoke", otherToken,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("revoking another user's connector code = %d, want 404", res.StatusCode)
	}

	if body := e.uiGet(t, "/tokens", ownerToken); !strings.Contains(body, "Owner connector") {
		t.Fatalf("the owner's connector should survive: %s", body)
	}
}

func TestUIRevokeConnectorRejectsAMalformedID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-oauth-bad-id")

	form := url.Values{"csrf_token": {uiCSRFTokenForTest("session", token)}}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients/not-a-uuid/revoke", token,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

func TestUIRegisterConnectorRejectsAMissingName(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-oauth-no-name")

	form := url.Values{
		"name":          {""},
		"redirect_uris": {"https://claude.ai/api/mcp/auth_callback"},
		"csrf_token":    {uiCSRFTokenForTest("session", token)},
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients", token,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Name required") {
		t.Fatalf("code = %d body = %s", res.StatusCode, body)
	}
}

// An htmx submission answers with the rebuilt panel instead of redirecting,
// because an htmx post never becomes the browser's address and so has nothing
// to replay on reload.
func TestUIRegisterConnectorOverHTMXRevealsInline(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-oauth-htmx")

	form := url.Values{
		"name":          {"Claude"},
		"redirect_uris": {"https://claude.ai/api/mcp/auth_callback"},
		"csrf_token":    {uiCSRFTokenForTest("session", token)},
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/oauth-clients", token,
		strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "HX-Request": "true"})
	body := readBody(t, res)
	res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Copy the client secret now.") {
		t.Fatalf("htmx response should reveal the secret inline: %s", body)
	}
	if uiOAuthSecretPattern.FindStringSubmatch(body) == nil {
		t.Fatalf("htmx response missing the secret: %s", body)
	}
	// No reveal cookie is needed when the secret is in the response already.
	if findUICookieOrNil(res.Cookies(), uiOAuthRevealCookieNameForTest) != nil {
		t.Fatalf("htmx creation should not stash a reveal cookie: %+v", res.Cookies())
	}
}
