package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type githubCredentialsBody struct {
	Configured bool                     `json:"configured"`
	Tokens     []model.GitHubCredential `json:"tokens"`
}

func githubSecondAdminToken(t *testing.T, e *httpEnv) string {
	t.Helper()
	key := uniqueProjectKey(t)
	admin, err := e.store.CreateOrUpdateAdminUser(e.ctx, "second-admin-"+key+"@example.com", "Second Admin")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	token, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: admin.ID, Kind: model.AuthTokenKindAPI, Name: "test"})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	return token.RawToken
}

func TestHTTPGitHubSavedTokenLifecycle(t *testing.T) {
	e, provider := newGitHubHTTPEnv(t)
	otherAdminToken := githubSecondAdminToken(t, e)
	tokensPath := "/me/github-tokens"
	connectionsPath := e.projectPath() + "/github/connections"

	code, body := e.do(t, http.MethodGet, tokensPath, nil)
	if list := decode[githubCredentialsBody](t, body); code != http.StatusOK || !list.Configured || len(list.Tokens) != 0 {
		t.Fatalf("initial list = %d %s", code, body)
	}
	for _, bad := range []map[string]any{{"name": "", "token": "x"}, {"name": "Personal"}, {"nope": true}} {
		if code, body := e.do(t, http.MethodPost, tokensPath, bad); code != http.StatusBadRequest {
			t.Fatalf("create %v = %d %s", bad, code, body)
		}
	}
	provider.loginErr = githubintegration.ErrUnauthorized
	if code, body := e.do(t, http.MethodPost, tokensPath, map[string]any{"name": "Personal", "token": "expired-token"}); code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected token = %d %s", code, body)
	}
	provider.loginErr = nil
	code, body = e.do(t, http.MethodPost, tokensPath, map[string]any{"name": "Personal", "token": " saved-token "})
	if code != http.StatusCreated || strings.Contains(string(body), "saved-token") {
		t.Fatalf("create = %d %s", code, body)
	}
	credential := decode[model.GitHubCredential](t, body)
	if credential.Name != "Personal" || credential.GitHubLogin != "octocat" || provider.tokens[len(provider.tokens)-1] != "saved-token" {
		t.Fatalf("credential = %+v tokens=%v", credential, provider.tokens)
	}
	if code, body := e.do(t, http.MethodPost, tokensPath, map[string]any{"name": "personal", "token": "saved-token"}); code != http.StatusConflict {
		t.Fatalf("duplicate name = %d %s", code, body)
	}

	if code, body := e.do(t, http.MethodPost, connectionsPath, map[string]any{"repository": "acme/private", "credential_id": credential.ID, "token": "pasted"}); code != http.StatusBadRequest {
		t.Fatalf("both token sources = %d %s", code, body)
	}
	if code, body := e.doWithToken(t, otherAdminToken, http.MethodPost, connectionsPath, map[string]any{"repository": "acme/private", "credential_id": credential.ID}); code != http.StatusNotFound {
		t.Fatalf("another user's saved token = %d %s", code, body)
	}
	code, body = e.do(t, http.MethodPost, connectionsPath, map[string]any{"repository": "acme/private", "credential_id": credential.ID})
	if code != http.StatusCreated || !strings.Contains(string(body), `"credential_id":"`+credential.ID.String()+`"`) || provider.tokens[len(provider.tokens)-1] != "saved-token" {
		t.Fatalf("connect with saved token = %d %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, tokensPath, nil)
	if list := decode[githubCredentialsBody](t, body); code != http.StatusOK || len(list.Tokens) != 1 || list.Tokens[0].ConnectionCount != 1 {
		t.Fatalf("list after connect = %d %s", code, body)
	}

	credentialPath := tokensPath + "/" + credential.ID.String()
	for _, tt := range []struct {
		path string
		body any
		want int
	}{
		{credentialPath, map[string]any{}, http.StatusBadRequest},
		{credentialPath, map[string]any{"nope": true}, http.StatusBadRequest},
		{tokensPath + "/not-a-uuid", map[string]any{"name": "Work"}, http.StatusBadRequest},
		{tokensPath + "/" + uuid.NewString(), map[string]any{"name": "Work"}, http.StatusNotFound},
	} {
		if code, body := e.do(t, http.MethodPatch, tt.path, tt.body); code != tt.want {
			t.Fatalf("PATCH %s %v = %d %s, want %d", tt.path, tt.body, code, body, tt.want)
		}
	}
	if code, body := e.doWithToken(t, otherAdminToken, http.MethodPatch, credentialPath, map[string]any{"name": "Mine now"}); code != http.StatusNotFound {
		t.Fatalf("rename another user's token = %d %s", code, body)
	}
	code, body = e.do(t, http.MethodPatch, credentialPath, map[string]any{"name": "Work"})
	if renamed := decode[model.GitHubCredential](t, body); code != http.StatusOK || renamed.Name != "Work" {
		t.Fatalf("rename = %d %s", code, body)
	}
	code, body = e.do(t, http.MethodPatch, credentialPath, map[string]any{"token": "rotated-token"})
	if code != http.StatusOK || strings.Contains(string(body), "rotated-token") || provider.tokens[len(provider.tokens)-1] != "rotated-token" {
		t.Fatalf("rotate = %d %s", code, body)
	}

	if code, _ := e.do(t, http.MethodDelete, tokensPath+"/not-a-uuid", nil); code != http.StatusBadRequest {
		t.Fatalf("delete bad id = %d", code)
	}
	if code, _ := e.doWithToken(t, otherAdminToken, http.MethodDelete, credentialPath, nil); code != http.StatusNotFound {
		t.Fatalf("delete another user's token = %d", code)
	}
	if code, body := e.do(t, http.MethodDelete, credentialPath, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, connectionsPath, nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"connections":[]`) {
		t.Fatalf("connections after deleting their token = %d %s", code, body)
	}
	if code, _ := e.do(t, http.MethodDelete, credentialPath, nil); code != http.StatusNotFound {
		t.Fatalf("second delete = %d", code)
	}
}

func TestHTTPGitHubSavedTokensUnconfiguredServer(t *testing.T) {
	e := newHTTPEnv(t)
	code, body := e.do(t, http.MethodGet, "/me/github-tokens", nil)
	if list := decode[githubCredentialsBody](t, body); code != http.StatusOK || list.Configured {
		t.Fatalf("unconfigured list = %d %s", code, body)
	}
	if code, _ := e.do(t, http.MethodPost, "/me/github-tokens", map[string]any{"name": "Personal", "token": "x"}); code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured create = %d", code)
	}
	if code, _ := e.do(t, http.MethodPatch, "/me/github-tokens/"+uuid.NewString(), map[string]any{"name": "Work"}); code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured update = %d", code)
	}
	// Deleting needs no encryption key, so tokens saved before it was removed
	// can still be cleaned up.
	if code, _ := e.do(t, http.MethodDelete, "/me/github-tokens/"+uuid.NewString(), nil); code != http.StatusNotFound {
		t.Fatalf("unconfigured delete = %d", code)
	}
}

func TestHTTPConnectorTokenCannotUseSavedGitHubToken(t *testing.T) {
	e, provider := newGitHubHTTPEnv(t)
	admin, err := e.store.GetUser(e.ctx, e.adminID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	client, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{UserID: admin.ID, Name: "Claude", RedirectURIs: []string{oauthTestRedirectURI}})
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}
	oauth := &oauthHTTPEnv{httpEnv: e, user: admin, sessionToken: e.authToken, client: client}
	connectorToken, _ := oauth.exchange(t, oauth.approve(t, nil))["access_token"].(string)
	code, body := e.do(t, http.MethodPost, "/me/github-tokens", map[string]any{"name": "Personal", "token": "saved-token"})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	credential := decode[model.GitHubCredential](t, body)
	calls := len(provider.tokens)
	code, body = e.doWithToken(t, connectorToken, http.MethodPost, e.projectPath()+"/github/connections", map[string]any{"repository": "acme/private", "credential_id": credential.ID})
	if code != http.StatusForbidden || len(provider.tokens) != calls {
		t.Fatalf("connector connect with saved token = %d %s", code, body)
	}
}

func TestUIGitHubSavedTokensOnTokensPage(t *testing.T) {
	e, provider := newGitHubHTTPEnv(t)
	post := func(path string, form url.Values) string {
		t.Helper()
		res := e.uiDoNoRedirect(t, http.MethodPost, path, e.authToken, strings.NewReader(form.Encode()))
		defer res.Body.Close()
		body := readBody(t, res)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("POST %s = %d %s", path, res.StatusCode, body)
		}
		return body
	}
	body := e.uiGet(t, "/tokens", e.authToken)
	for _, want := range []string{`id="github-tokens"`, "GitHub tokens", `data-modal-open="github-token-create"`, "No GitHub tokens saved", `id="github-token-create" data-client-modal class="fixed inset-0 z-50 hidden`} {
		if !strings.Contains(body, want) {
			t.Fatalf("tokens page missing %q: %s", want, body)
		}
	}

	body = post("/settings/github-tokens", url.Values{"name": {""}, "token": {"saved-token"}})
	if !strings.Contains(body, "token name is required") || !strings.Contains(body, `id="github-token-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("create validation: %s", body)
	}
	provider.loginErr = githubintegration.ErrUnauthorized
	body = post("/settings/github-tokens", url.Values{"name": {"Personal"}, "token": {"expired-token"}})
	if !strings.Contains(body, "GitHub did not accept that token") || !strings.Contains(body, `value="Personal"`) || strings.Contains(body, "expired-token") {
		t.Fatalf("create rejected token: %s", body)
	}
	provider.loginErr = nil
	body = post("/settings/github-tokens", url.Values{"name": {"Personal"}, "token": {"saved-token"}})
	if !strings.Contains(body, "Saved “Personal”") || !strings.Contains(body, "@octocat") || !strings.Contains(body, "Not used by any repositories") || strings.Contains(body, "saved-token") {
		t.Fatalf("create: %s", body)
	}
	body = post("/settings/github-tokens", url.Values{"name": {"Personal"}, "token": {"saved-token"}})
	if !strings.Contains(body, "You already have a saved token with that name.") {
		t.Fatalf("create duplicate: %s", body)
	}

	credentials, err := e.store.ListGitHubCredentials(e.ctx, e.adminID)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials = %+v, %v", credentials, err)
	}
	credentialPath := "/settings/github-tokens/" + credentials[0].ID.String()
	body = post(credentialPath, url.Values{"name": {""}})
	if !strings.Contains(body, "token name is required") || !strings.Contains(body, `id="github-token-edit-`+credentials[0].ID.String()+`" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("edit validation: %s", body)
	}
	body = post(credentialPath, url.Values{"name": {"Work"}})
	if !strings.Contains(body, "Saved “Work”.") {
		t.Fatalf("rename: %s", body)
	}
	body = post(credentialPath, url.Values{"name": {"Work"}, "token": {"rotated-token"}})
	if !strings.Contains(body, "Replaced the token for “Work”") || provider.tokens[len(provider.tokens)-1] != "rotated-token" {
		t.Fatalf("rotate: %s", body)
	}

	for path, want := range map[string]int{
		"/settings/github-tokens/not-a-uuid":                      http.StatusBadRequest,
		"/settings/github-tokens/not-a-uuid/delete":               http.StatusBadRequest,
		"/settings/github-tokens/" + uuid.NewString() + "/delete": http.StatusNotFound,
	} {
		res := e.uiDoNoRedirect(t, http.MethodPost, path, e.authToken, strings.NewReader("name=Work"))
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("POST %s = %d, want %d", path, res.StatusCode, want)
		}
	}
	body = post(credentialPath+"/delete", url.Values{})
	if !strings.Contains(body, "Removed the saved token.") || !strings.Contains(body, "No GitHub tokens saved") {
		t.Fatalf("delete: %s", body)
	}
}

func TestUIGitHubSavedTokensUnconfigured(t *testing.T) {
	e := newHTTPEnv(t)
	if body := e.uiGet(t, "/tokens", e.authToken); !strings.Contains(body, "GitHub integration is not configured on this deployment") || strings.Contains(body, `data-modal-open="github-token-create"`) {
		t.Fatalf("unconfigured tokens page: %s", body)
	}
	// A form left open from before the key was removed lands back on the
	// section that explains why.
	for _, path := range []string{"/settings/github-tokens", "/settings/github-tokens/" + uuid.NewString()} {
		res := e.uiDoNoRedirect(t, http.MethodPost, path, e.authToken, strings.NewReader("name=Work&token=x"))
		body := readBody(t, res)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "GitHub integration is not configured on this deployment") {
			t.Fatalf("POST %s = %d %s", path, res.StatusCode, body)
		}
	}
}

func TestUIGitHubConnectWithSavedTokens(t *testing.T) {
	e, provider := newGitHubHTTPEnv(t)
	connect := func(form url.Values) string {
		t.Helper()
		res := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/github/connections", e.authToken, strings.NewReader(form.Encode()))
		defer res.Body.Close()
		body := readBody(t, res)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("connect %v = %d %s", form, res.StatusCode, body)
		}
		return body
	}
	for _, tt := range []struct {
		form url.Values
		want string
	}{
		{url.Values{"repository": {"bad"}, "token_name": {"Work"}, "token": {"saved-token"}}, "repository must be owner/name"},
		{url.Values{"repository": {"acme/private"}, "token": {"saved-token"}}, "token name is required"},
		{url.Values{"repository": {"acme/private"}}, "Choose a saved token or add a new one."},
		{url.Values{"repository": {"acme/private"}, "credential_id": {uuid.NewString()}}, "That saved token no longer exists."},
	} {
		if body := connect(tt.form); !strings.Contains(body, tt.want) || !strings.Contains(body, `id="project-github-connection" data-client-modal class="fixed inset-0 z-50 grid`) {
			t.Fatalf("connect %v missing %q: %s", tt.form, tt.want, body)
		}
	}
	if credentials, _ := e.store.ListGitHubCredentials(e.ctx, e.adminID); len(credentials) != 0 {
		t.Fatalf("a failed validation saved a token: %+v", credentials)
	}

	// A pasted token is saved even when the connection then fails, and the
	// retry offers it from the saved list.
	provider.err = githubintegration.ErrUnavailable
	body := connect(url.Values{"repository": {"acme/private"}, "token_name": {"Work"}, "token": {"saved-token"}})
	credentials, err := e.store.ListGitHubCredentials(e.ctx, e.adminID)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("credentials = %+v, %v", credentials, err)
	}
	selected := `<option value="` + credentials[0].ID.String() + `" selected>Work (@octocat)</option>`
	if !strings.Contains(body, "Saved “Work” to your account. GitHub could not find") || !strings.Contains(body, selected) || strings.Contains(body, "saved-token") {
		t.Fatalf("connect failure after saving: %s", body)
	}
	provider.err = nil
	body = connect(url.Values{"repository": {"acme/private"}, "credential_id": {credentials[0].ID.String()}})
	if !strings.Contains(body, "acme/private") || !strings.Contains(body, "your token “Work”") || !strings.Contains(body, `id="project-github-connection" data-client-modal class="fixed inset-0 z-50 hidden`) {
		t.Fatalf("connect with saved token: %s", body)
	}
	if provider.tokens[len(provider.tokens)-1] != "saved-token" {
		t.Fatalf("GitHub saw %v", provider.tokens)
	}

	unconfigured := newHTTPEnv(t)
	res := unconfigured.uiDoNoRedirect(t, http.MethodPost, unconfigured.projectPath()+"/github/connections", unconfigured.authToken, strings.NewReader("repository=acme%2Frepo"))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Set the server encryption key to enable repository connections.") {
		t.Fatalf("unconfigured connect = %d %s", res.StatusCode, body)
	}
}
