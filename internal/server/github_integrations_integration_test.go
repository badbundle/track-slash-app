package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/githubintegration"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

type githubHTTPProvider struct {
	login      string
	loginErr   error
	repository githubintegration.Repository
	snapshot   githubintegration.Snapshot
	err        error
	tokens     []string
}

func (p *githubHTTPProvider) GetAuthenticatedUser(_ context.Context, token string) (string, error) {
	p.tokens = append(p.tokens, token)
	return p.login, p.loginErr
}

func (p *githubHTTPProvider) GetRepository(_ context.Context, token, _, _ string) (githubintegration.Repository, error) {
	p.tokens = append(p.tokens, token)
	return p.repository, p.err
}

func (p *githubHTTPProvider) GetBranch(_ context.Context, token, _, _, _ string) (githubintegration.Snapshot, error) {
	p.tokens = append(p.tokens, token)
	return p.snapshot, p.err
}

func (p *githubHTTPProvider) GetPullRequest(_ context.Context, token, _, _ string, _ int) (githubintegration.Snapshot, error) {
	p.tokens = append(p.tokens, token)
	return p.snapshot, p.err
}

func newGitHubHTTPEnv(t *testing.T) (*httpEnv, *githubHTTPProvider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)
	cryptor, err := githubintegration.NewCryptor(bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatalf("NewCryptor: %v", err)
	}
	prID, prNumber := int64(404), 12
	provider := &githubHTTPProvider{
		login:      "octocat",
		repository: githubintegration.Repository{ID: 77, Owner: "acme", Name: "private", HTMLURL: "https://github.com/acme/private", Private: true},
		snapshot:   githubintegration.Snapshot{ResourceType: model.GitHubResourcePullRequest, PullRequestID: &prID, PullRequestNumber: &prNumber, Title: "Ship private feature", HTMLURL: "https://github.com/acme/private/pull/12", State: model.GitHubLinkStateDraft},
	}
	service := githubintegration.NewService(st, provider, cryptor, githubintegration.ServiceOptions{})
	testServer := httptest.NewServer(server.NewWithOptions(st, nil, server.Options{GitHubIntegration: service}).Router())
	t.Cleanup(testServer.Close)
	key := uniqueProjectKey(t)
	admin, err := st.CreateOrUpdateAdminUser(ctx, "admin-"+key+"@example.com", "Admin")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	project, err := st.CreateProjectForUser(ctx, admin.ID, key, "github-http", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	token, err := st.CreateAuthToken(ctx, store.CreateAuthTokenParams{UserID: admin.ID, Kind: model.AuthTokenKindAPI, Name: "test"})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	return &httpEnv{
		ctx: ctx, ts: testServer, pool: db.Pool, store: st, projectID: project.ID, projKey: key,
		ownerUsername: admin.Username, adminID: admin.ID, authToken: token.RawToken,
	}, provider
}

func githubRoleToken(t *testing.T, e *httpEnv, role model.ProjectMemberRole) string {
	t.Helper()
	suffix := strings.ToLower(string(role)) + "-" + uniqueProjectKey(t)
	user, err := e.store.CreateUserProfile(e.ctx, suffix, suffix+"@example.com", suffix)
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, user.ID, role); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	token, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: user.ID, Kind: model.AuthTokenKindAPI, Name: "test"})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	return token.RawToken
}

func TestHTTPGitHubConnectionAndLinkLifecycleWithAuthorization(t *testing.T) {
	e, provider := newGitHubHTTPEnv(t)
	memberToken := githubRoleToken(t, e, model.ProjectMemberRoleMember)
	readonlyToken := githubRoleToken(t, e, model.ProjectMemberRoleReadonly)
	connectionsPath := e.projectPath() + "/github/connections"

	code, body := e.do(t, http.MethodGet, connectionsPath, nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"configured":true`) {
		t.Fatalf("initial connections = %d %s", code, body)
	}
	for _, token := range []string{memberToken, readonlyToken} {
		code, _ = e.doWithToken(t, token, http.MethodPost, connectionsPath, map[string]any{"repository": "acme/private", "token": "private-token"})
		if code != http.StatusForbidden {
			t.Fatalf("non-manager connect code = %d", code)
		}
	}
	code, body = e.do(t, http.MethodPost, connectionsPath, map[string]any{"repository": "acme/private", "token": "private-token"})
	if code != http.StatusCreated || bytes.Contains(body, []byte("private-token")) || len(provider.tokens) != 1 || provider.tokens[0] != "private-token" {
		t.Fatalf("connect = %d %s provider=%+v", code, body, provider)
	}
	connection := decode[model.GitHubConnection](t, body)
	if !connection.Private || connection.RepositoryID != 77 {
		t.Fatalf("connection = %+v", connection)
	}

	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "GitHub API"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	linksPath := e.issuePath(issue) + "/github-links"
	code, _ = e.doWithToken(t, readonlyToken, http.MethodPost, linksPath, map[string]any{"connection_id": connection.ID, "reference": "#12"})
	if code != http.StatusForbidden {
		t.Fatalf("readonly create link code = %d", code)
	}
	code, body = e.doWithToken(t, memberToken, http.MethodPost, linksPath, map[string]any{"connection_id": connection.ID, "reference": "https://github.com/acme/private/pull/12"})
	if code != http.StatusCreated {
		t.Fatalf("create link = %d %s", code, body)
	}
	link := decode[model.GitHubIssueLink](t, body)
	if link.State != model.GitHubLinkStateDraft || link.PullRequestID == nil || *link.PullRequestID != 404 {
		t.Fatalf("link = %+v", link)
	}
	code, _ = e.doWithToken(t, readonlyToken, http.MethodGet, linksPath, nil)
	if code != http.StatusOK {
		t.Fatalf("readonly list links code = %d", code)
	}
	code, _ = e.doWithToken(t, memberToken, http.MethodPost, linksPath, map[string]any{"connection_id": connection.ID, "reference": "#12"})
	if code != http.StatusConflict {
		t.Fatalf("duplicate link code = %d", code)
	}

	provider.err = &githubintegration.RateLimitError{RetryAt: time.Now().Add(time.Minute)}
	code, body = e.doWithToken(t, memberToken, http.MethodPost, linksPath+"/"+link.ID.String()+"/refresh", nil)
	if code != http.StatusOK || !strings.Contains(string(body), "rate limit") {
		t.Fatalf("cached rate-limited refresh = %d %s", code, body)
	}
	provider.err = nil
	provider.snapshot.Title = "Merged private feature"
	provider.snapshot.State = model.GitHubLinkStateMerged
	code, body = e.doWithToken(t, memberToken, http.MethodPost, linksPath+"/"+link.ID.String()+"/refresh", nil)
	if code != http.StatusOK || !strings.Contains(string(body), `"state":"merged"`) {
		t.Fatalf("merged refresh = %d %s", code, body)
	}
	code, _ = e.doWithToken(t, memberToken, http.MethodDelete, linksPath+"/"+link.ID.String(), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete link code = %d", code)
	}
	code, _ = e.do(t, http.MethodDelete, connectionsPath+"/"+connection.ID.String(), nil)
	if code != http.StatusNoContent {
		t.Fatalf("disconnect code = %d", code)
	}
}

func TestHTTPGitHubValidationProviderErrorsAndUnconfiguredServer(t *testing.T) {
	e, provider := newGitHubHTTPEnv(t)
	path := e.projectPath() + "/github/connections"
	code, _ := e.do(t, http.MethodPost, path, map[string]any{"repository": "bad", "token": "x"})
	if code != http.StatusBadRequest {
		t.Fatalf("invalid repository code = %d", code)
	}
	provider.err = githubintegration.ErrUnauthorized
	code, _ = e.do(t, http.MethodPost, path, map[string]any{"repository": "acme/private", "token": "x"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("unauthorized provider code = %d", code)
	}
	provider.err = &githubintegration.RateLimitError{RetryAt: time.Now().Add(time.Minute)}
	res := githubAPIRequest(t, e, http.MethodPost, path, map[string]any{"repository": "acme/private", "token": "x"})
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("rate limited provider code = %d retry-after=%q", res.StatusCode, res.Header.Get("Retry-After"))
	}
	res.Body.Close()
	provider.err = errors.New("network failure")
	code, _ = e.do(t, http.MethodPost, path, map[string]any{"repository": "acme/private", "token": "x"})
	if code != http.StatusBadGateway {
		t.Fatalf("network provider code = %d", code)
	}

	unconfigured := newHTTPEnv(t)
	code, _ = unconfigured.do(t, http.MethodPost, unconfigured.projectPath()+"/github/connections", map[string]any{"repository": "acme/repo", "token": "x"})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured connect code = %d", code)
	}
}

func githubAPIRequest(t *testing.T, e *httpEnv, method, path string, body any) *http.Response {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(e.ctx, method, e.ts.URL+apiPath(path), bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return res
}

func TestUIGitHubConnectionAndIssueActions(t *testing.T) {
	e, provider := newGitHubHTTPEnv(t)
	aboutPath := e.projectPath() + "/about"
	if body := e.uiGet(t, aboutPath, e.authToken); !strings.Contains(body, "GitHub repositories") || !strings.Contains(body, "Fine-grained token") || !strings.Contains(body, `data-modal-open="project-github-connection"`) || !strings.Contains(body, `id="project-github-connection" data-client-modal class="fixed inset-0 z-50 hidden`) {
		t.Fatalf("about page missing GitHub controls: %s", body)
	}
	form := url.Values{"repository": {"acme/private"}, "token_name": {"Work"}, "token": {"private-token"}}
	provider.err = githubintegration.ErrUnavailable
	res := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/github/connections", e.authToken, strings.NewReader(form.Encode()))
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "could not find") || !strings.Contains(body, `id="project-github-connection" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("UI connect error code=%d body=%s", res.StatusCode, body)
	}
	provider.err = nil
	credentials, err := e.store.ListGitHubCredentials(e.ctx, e.adminID)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("saved credentials = %+v, %v", credentials, err)
	}
	form = url.Values{"repository": {"acme/private"}, "credential_id": {credentials[0].ID.String()}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/github/connections", e.authToken, strings.NewReader(form.Encode()))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "acme/private") || strings.Contains(body, "private-token") || !strings.Contains(body, `id="project-github-connection" data-client-modal class="fixed inset-0 z-50 hidden`) {
		t.Fatalf("UI connect code=%d body=%s", res.StatusCode, body)
	}
	connections, err := e.store.ListGitHubConnections(e.ctx, e.projectID)
	if err != nil || len(connections) != 1 {
		t.Fatalf("connections = %+v, %v", connections, err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "UI GitHub"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	issuePath := e.issuePath(issue)
	if body := e.uiGet(t, issuePath, e.authToken); !strings.Contains(body, "Branch or pull request") || !strings.Contains(body, `data-modal-open="issue-github-link"`) || !strings.Contains(body, `id="issue-github-link" data-client-modal class="fixed inset-0 z-50 hidden`) {
		t.Fatalf("issue page missing GitHub form: %s", body)
	}
	badLinkForm := url.Values{"connection_id": {"not-a-uuid"}, "reference": {"feature/modal"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, issuePath+"/github-links", e.authToken, strings.NewReader(badLinkForm.Encode()))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Choose a repository") || !strings.Contains(body, `id="issue-github-link" data-client-modal class="fixed inset-0 z-50 grid`) || !strings.Contains(body, `value="feature/modal"`) {
		t.Fatalf("UI link error code=%d body=%s", res.StatusCode, body)
	}
	linkForm := url.Values{"connection_id": {connections[0].ID.String()}, "reference": {"#12"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, issuePath+"/github-links", e.authToken, strings.NewReader(linkForm.Encode()))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Ship private feature") || !strings.Contains(body, "Draft") {
		t.Fatalf("UI link code=%d body=%s", res.StatusCode, body)
	}
	links, err := e.store.ListGitHubIssueLinks(e.ctx, issue.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %+v, %v", links, err)
	}
	provider.err = githubintegration.ErrUnavailable
	res = e.uiDoNoRedirect(t, http.MethodPost, issuePath+"/github-links/"+links[0].ID.String()+"/refresh", e.authToken, nil)
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "unavailable") || !strings.Contains(body, "last known state") {
		t.Fatalf("UI refresh code=%d body=%s", res.StatusCode, body)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, issuePath+"/github-links/"+links[0].ID.String()+"/delete", e.authToken, nil)
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No branches or pull requests linked") {
		t.Fatalf("UI delete code=%d body=%s", res.StatusCode, body)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/github/connections/"+connections[0].ID.String()+"/disconnect", e.authToken, nil)
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No GitHub repositories connected") {
		t.Fatalf("UI disconnect code=%d body=%s", res.StatusCode, body)
	}
}
