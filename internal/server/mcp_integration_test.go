package server_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	objectstorage "github.com/bradleymackey/track-slash/internal/storage"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

type staticOAuthHandler struct {
	token string
}

func (h staticOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	if h.token == "" {
		return nil, nil
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token}), nil
}

func (h staticOAuthHandler) Authorize(context.Context, *http.Request, *http.Response) error {
	return errors.New("authorization unavailable in tests")
}

func newMCPHTTPEnv(t *testing.T, storageSvc *objectstorage.Service) *httpEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)
	srv := server.NewWithOptions(st, nil, server.Options{ObjectStorage: storageSvc})
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	key := uniqueProjectKey(t)
	admin, err := st.CreateOrUpdateAdminUser(ctx, "admin-"+key+"@example.com", "Admin")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	proj, err := st.CreateProjectForUser(ctx, admin.ID, key, "mcp-test", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	token, err := st.CreateAuthToken(ctx, store.CreateAuthTokenParams{
		UserID: admin.ID,
		Kind:   model.AuthTokenKindAPI,
		Name:   "test",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}

	return &httpEnv{
		ctx: ctx, ts: ts, pool: db.Pool, store: st, projectID: proj.ID, projKey: key, ownerUsername: admin.Username,
		adminID: admin.ID, authToken: token.RawToken,
	}
}

func mcpConnect(t *testing.T, e *httpEnv, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "track-slash-test", Version: "v1"}, nil)
	session, err := client.Connect(e.ctx, &mcp.StreamableClientTransport{
		Endpoint:     e.ts.URL + "/mcp",
		OAuthHandler: staticOAuthHandler{token: token},
	}, nil)
	if err != nil {
		t.Fatalf("Connect MCP: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func mcpCall(t *testing.T, e *httpEnv, session *mcp.ClientSession, name string, args map[string]any) map[string]json.RawMessage {
	t.Helper()
	res, err := session.CallTool(e.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content %s: %v raw=%s", name, err, raw)
	}
	if res.IsError {
		content, _ := json.Marshal(res.Content)
		t.Fatalf("tool %s error: structured=%s content=%s", name, raw, content)
	}
	return out
}

func mcpCallExpectError(t *testing.T, e *httpEnv, session *mcp.ClientSession, name string, args map[string]any) map[string]json.RawMessage {
	t.Helper()
	res, err := session.CallTool(e.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured error content: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured error content %s: %v raw=%s", name, err, raw)
	}
	if !res.IsError {
		t.Fatalf("tool %s succeeded, want error: %s", name, raw)
	}
	return out
}

func decodeMCPField[T any](t *testing.T, out map[string]json.RawMessage, field string) T {
	t.Helper()
	raw, ok := out[field]
	if !ok {
		t.Fatalf("missing field %q in %v", field, out)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal field %q: %v raw=%s", field, err, raw)
	}
	return v
}

func requireMCPErrorCode(t *testing.T, out map[string]json.RawMessage, want string) {
	t.Helper()
	var got struct {
		Code string `json:"code"`
	}
	raw := decodeMCPField[json.RawMessage](t, out, "error")
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal error: %v raw=%s", err, raw)
	}
	if got.Code != want {
		t.Fatalf("error code = %q, want %q", got.Code, want)
	}
}

func TestMCPToolErrorsAreStructured(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024*1024)
	e := newMCPHTTPEnv(t, storageSvc)
	session := mcpConnect(t, e, e.authToken)

	out := mcpCallExpectError(t, e, session, "track_create_issue", map[string]any{
		"owner": e.ownerUsername,
		"key":   e.projKey,
		"title": "",
	})
	requireMCPErrorCode(t, out, "validation_error")
}

func TestMCPBulkIssueContextLinks(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name == "track_bulk_link_issue_contexts" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("track_bulk_link_issue_contexts not found")
	}

	createContext := func(title string) model.ProjectContext {
		contextItem, err := e.store.CreateProjectContext(e.ctx, store.CreateProjectContextParams{
			ProjectID:   e.projectID,
			Title:       title,
			Body:        title + " body",
			CreatedByID: e.adminID,
		})
		if err != nil {
			t.Fatalf("CreateProjectContext %q: %v", title, err)
		}
		return contextItem
	}
	firstContext := createContext("Architecture")
	secondContext := createContext("Runbook")
	issueA, err := e.store.CreateIssue(e.ctx, storeCreateIssue(e.projectID, "MCP A"))
	if err != nil {
		t.Fatalf("CreateIssue A: %v", err)
	}
	issueB, err := e.store.CreateIssue(e.ctx, storeCreateIssue(e.projectID, "MCP B"))
	if err != nil {
		t.Fatalf("CreateIssue B: %v", err)
	}
	if _, err := e.store.CreateIssueContextLink(e.ctx, issueA.ID, firstContext.ID); err != nil {
		t.Fatalf("CreateIssueContextLink existing: %v", err)
	}
	links := []map[string]string{
		{"issue": issueA.Identifier, "context": firstContext.Ref},
		{"issue": issueA.Identifier, "context": secondContext.Ref},
		{"issue": issueB.Identifier, "context": firstContext.Ref},
		{"issue": issueB.Identifier, "context": firstContext.Ref},
	}
	args := map[string]any{"owner": e.ownerUsername, "key": e.projKey, "links": links}
	out := mcpCall(t, e, session, "track_bulk_link_issue_contexts", args)
	if requested, created, unchanged := decodeMCPField[int](t, out, "requested"), decodeMCPField[int](t, out, "created"), decodeMCPField[int](t, out, "unchanged"); requested != 4 || created != 2 || unchanged != 2 {
		t.Fatalf("bulk counts = requested:%d created:%d unchanged:%d", requested, created, unchanged)
	}
	out = mcpCall(t, e, session, "track_bulk_link_issue_contexts", args)
	if requested, created, unchanged := decodeMCPField[int](t, out, "requested"), decodeMCPField[int](t, out, "created"), decodeMCPField[int](t, out, "unchanged"); requested != 4 || created != 0 || unchanged != 4 {
		t.Fatalf("repeat bulk counts = requested:%d created:%d unchanged:%d", requested, created, unchanged)
	}

	readonly, readonlyToken := e.mustUserToken(t, "mcp-bulk-context-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	readonlySession := mcpConnect(t, e, readonlyToken)
	errOut := mcpCallExpectError(t, e, readonlySession, "track_bulk_link_issue_contexts", args)
	requireMCPErrorCode(t, errOut, "forbidden")
}

func TestMCPBulkIssueContextLinkValidationAndAtomicity(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	shared, err := e.store.CreateProjectContext(e.ctx, store.CreateProjectContextParams{
		ProjectID: e.projectID, Title: "Shared", Body: "Shared body", CreatedByID: e.adminID,
	})
	if err != nil {
		t.Fatalf("CreateProjectContext: %v", err)
	}
	target, err := e.store.CreateIssue(e.ctx, storeCreateIssue(e.projectID, "MCP atomic target"))
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	scopeOwner, err := e.store.CreateIssue(e.ctx, storeCreateIssue(e.projectID, "MCP scope owner"))
	if err != nil {
		t.Fatalf("CreateIssue scope owner: %v", err)
	}
	issueScoped, err := e.store.CreateIssueContext(e.ctx, store.CreateIssueContextParams{
		IssueID: scopeOwner.ID, Title: "Issue only", Body: "Issue scoped.", CreatedByID: e.adminID,
	})
	if err != nil {
		t.Fatalf("CreateIssueContext: %v", err)
	}

	oversized := make([]map[string]string, 201)
	for i := range oversized {
		oversized[i] = map[string]string{"issue": target.Identifier, "context": shared.Ref}
	}
	base := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
	out := mcpCallExpectError(t, e, session, "track_bulk_link_issue_contexts", map[string]any{
		"owner": e.ownerUsername,
		"key":   "1",
		"links": []map[string]string{{"issue": target.Identifier, "context": shared.Ref}},
	})
	requireMCPErrorCode(t, out, "validation_error")
	for _, test := range []struct {
		name  string
		links any
	}{
		{name: "empty", links: []map[string]string{}},
		{name: "too many", links: oversized},
		{name: "malformed issue", links: []map[string]string{{"issue": "bad", "context": shared.Ref}}},
		{name: "cross project", links: []map[string]string{{"issue": "OTHER-1", "context": shared.Ref}}},
		{name: "malformed context", links: []map[string]string{{"issue": target.Identifier, "context": "bad"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := map[string]any{"owner": base["owner"], "key": base["key"], "links": test.links}
			errOut := mcpCallExpectError(t, e, session, "track_bulk_link_issue_contexts", args)
			requireMCPErrorCode(t, errOut, "validation_error")
		})
	}

	valid := map[string]string{"issue": target.Identifier, "context": shared.Ref}
	for _, test := range []struct {
		name    string
		invalid map[string]string
	}{
		{name: "missing issue", invalid: map[string]string{"issue": e.projKey + "-999999", "context": shared.Ref}},
		{name: "missing context", invalid: map[string]string{"issue": target.Identifier, "context": "context-999999"}},
		{name: "issue scoped context", invalid: map[string]string{"issue": target.Identifier, "context": issueScoped.Ref}},
	} {
		t.Run(test.name, func(t *testing.T) {
			errOut := mcpCallExpectError(t, e, session, "track_bulk_link_issue_contexts", map[string]any{
				"owner": e.ownerUsername,
				"key":   e.projKey,
				"links": []map[string]string{valid, test.invalid},
			})
			requireMCPErrorCode(t, errOut, "not_found")
			contexts, _, err := e.store.ListContextsForIssue(e.ctx, store.ListContextsForIssueParams{IssueID: target.ID, Limit: 10})
			if err != nil {
				t.Fatalf("ListContextsForIssue: %v", err)
			}
			if len(contexts) != 0 {
				t.Fatalf("target contexts after rejected batch = %+v", contexts)
			}
		})
	}
}

func TestMCPProjectMemberRolesAndReadonlyAccess(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)

	projectOwner, err := e.store.CreateUserProfile(e.ctx, "mcp-owner-"+e.projKey, "mcp-owner-"+e.projKey+"@example.com", "MCP Owner")
	if err != nil {
		t.Fatalf("CreateUserProfile owner: %v", err)
	}
	member, err := e.store.CreateUserProfile(e.ctx, "mcp-member-"+e.projKey, "mcp-member-"+e.projKey+"@example.com", "MCP Member")
	if err != nil {
		t.Fatalf("CreateUserProfile member: %v", err)
	}
	readonly, err := e.store.CreateUserProfile(e.ctx, "mcp-readonly-"+e.projKey, "mcp-readonly-"+e.projKey+"@example.com", "MCP Readonly")
	if err != nil {
		t.Fatalf("CreateUserProfile readonly: %v", err)
	}
	outsider, err := e.store.CreateUserProfile(e.ctx, "mcp-outsider-"+e.projKey, "mcp-outsider-"+e.projKey+"@example.com", "MCP Outsider")
	if err != nil {
		t.Fatalf("CreateUserProfile outsider: %v", err)
	}
	projectKey := uniqueProjectKey(t)
	project, err := e.store.CreateProjectForUser(e.ctx, projectOwner.ID, projectKey, "MCP role project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}

	tokenFor := func(t *testing.T, user model.User) string {
		t.Helper()
		token, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: user.ID, Kind: model.AuthTokenKindAPI, Name: "member role test"})
		if err != nil {
			t.Fatalf("CreateAuthToken %s: %v", user.Username, err)
		}
		return token.RawToken
	}
	ownerSession := mcpConnect(t, e, tokenFor(t, projectOwner))
	memberSession := mcpConnect(t, e, tokenFor(t, member))
	readonlySession := mcpConnect(t, e, tokenFor(t, readonly))
	outsiderSession := mcpConnect(t, e, tokenFor(t, outsider))
	adminSession := mcpConnect(t, e, e.authToken)
	projectArgs := map[string]any{"owner": projectOwner.Username, "key": project.Key}
	for _, query := range []string{"", " ", "m"} {
		args := map[string]any{"owner": projectOwner.Username, "key": project.Key, "query": query}
		out := mcpCall(t, e, ownerSession, "track_search_project_member_candidates", args)
		if candidates := decodeMCPField[[]model.ProjectMemberCandidate](t, out, "users"); len(candidates) != 0 {
			t.Fatalf("short MCP candidate query %q = %+v", query, candidates)
		}
	}

	candidateOut := mcpCall(t, e, ownerSession, "track_search_project_member_candidates", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "query": "mcp-", "limit": 10,
	})
	candidates := decodeMCPField[[]model.ProjectMemberCandidate](t, candidateOut, "users")
	if len(candidates) != 3 {
		t.Fatalf("candidate count = %d, want 3: %+v", len(candidates), candidates)
	}

	memberOut := mcpCall(t, e, ownerSession, "track_grant_project_member", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "username": member.Username,
	})
	if got := decodeMCPField[model.ProjectMember](t, memberOut, "member"); got.Role != model.ProjectMemberRoleMember {
		t.Fatalf("default member role = %q", got.Role)
	}
	readonlyOut := mcpCall(t, e, ownerSession, "track_grant_project_member", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "username": readonly.Username, "role": "readonly",
	})
	if got := decodeMCPField[model.ProjectMember](t, readonlyOut, "member"); got.Role != model.ProjectMemberRoleReadonly {
		t.Fatalf("readonly role = %q", got.Role)
	}

	listOut := mcpCall(t, e, readonlySession, "track_list_project_members", projectArgs)
	members := decodeMCPField[[]model.ProjectMember](t, listOut, "members")
	if len(members) != 3 || members[0].UserID != projectOwner.ID {
		t.Fatalf("members = %+v", members)
	}
	if bytes.Contains(listOut["members"], []byte("@example.com")) {
		t.Fatalf("membership output exposed an email: %s", listOut["members"])
	}
	searchOut := mcpCall(t, e, readonlySession, "track_search_project_members", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "query": "mcp-", "limit": 10,
	})
	searched := decodeMCPField[[]model.ProjectMemberCandidate](t, searchOut, "users")
	if len(searched) != 3 || bytes.Contains(searchOut["users"], []byte("@example.com")) {
		t.Fatalf("safe member search = %s", searchOut["users"])
	}

	if out := mcpCallExpectError(t, e, memberSession, "track_search_project_member_candidates", projectArgs); true {
		requireMCPErrorCode(t, out, "forbidden")
	}
	if out := mcpCallExpectError(t, e, memberSession, "track_grant_project_member", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "username": outsider.Username,
	}); true {
		requireMCPErrorCode(t, out, "forbidden")
	}
	created := mcpCall(t, e, memberSession, "track_create_issue", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "title": "member can write",
	})
	if issue := decodeMCPField[model.Issue](t, created, "issue"); issue.ProjectID != project.ID {
		t.Fatalf("created issue project = %s, want %s", issue.ProjectID, project.ID)
	}
	if out := mcpCallExpectError(t, e, readonlySession, "track_create_issue", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "title": "readonly cannot write",
	}); true {
		requireMCPErrorCode(t, out, "forbidden")
	}
	if out := mcpCallExpectError(t, e, outsiderSession, "track_list_project_members", projectArgs); true {
		requireMCPErrorCode(t, out, "forbidden")
	}

	for _, test := range []struct {
		name string
		tool string
		args map[string]any
		code string
	}{
		{name: "invalid role", tool: "track_grant_project_member", args: map[string]any{"owner": projectOwner.Username, "key": project.Key, "username": outsider.Username, "role": "invalid"}, code: "validation_error"},
		{name: "owner downgrade", tool: "track_grant_project_member", args: map[string]any{"owner": projectOwner.Username, "key": project.Key, "username": projectOwner.Username, "role": "readonly"}, code: "conflict"},
		{name: "owner removal", tool: "track_revoke_project_member", args: map[string]any{"owner": projectOwner.Username, "key": project.Key, "username": projectOwner.Username}, code: "conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := mcpCallExpectError(t, e, ownerSession, test.tool, test.args)
			requireMCPErrorCode(t, out, test.code)
		})
	}

	adminUpdate := mcpCall(t, e, adminSession, "track_grant_project_member", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "username": readonly.Username, "role": "member",
	})
	if got := decodeMCPField[model.ProjectMember](t, adminUpdate, "member"); got.Role != model.ProjectMemberRoleMember {
		t.Fatalf("admin-updated role = %q", got.Role)
	}
	mcpCall(t, e, adminSession, "track_revoke_project_member", map[string]any{
		"owner": projectOwner.Username, "key": project.Key, "username": readonly.Username,
	})
}

func TestMCPDeleteProjectRequiresOwnerOrAdmin(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)

	owner, err := e.store.CreateUserProfile(e.ctx, "mcp-del-owner-"+e.projKey, "mcp-del-owner-"+e.projKey+"@example.com", "MCP Delete Owner")
	if err != nil {
		t.Fatalf("CreateUserProfile owner: %v", err)
	}
	member, err := e.store.CreateUserProfile(e.ctx, "mcp-del-member-"+e.projKey, "mcp-del-member-"+e.projKey+"@example.com", "MCP Delete Member")
	if err != nil {
		t.Fatalf("CreateUserProfile member: %v", err)
	}
	tokenFor := func(t *testing.T, user model.User) string {
		t.Helper()
		token, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: user.ID, Kind: model.AuthTokenKindAPI, Name: "delete project test"})
		if err != nil {
			t.Fatalf("CreateAuthToken %s: %v", user.Username, err)
		}
		return token.RawToken
	}
	project, err := e.store.CreateProjectForUser(e.ctx, owner.ID, uniqueProjectKey(t), "MCP deletable", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, project.ID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	ownerSession := mcpConnect(t, e, tokenFor(t, owner))
	memberSession := mcpConnect(t, e, tokenFor(t, member))
	adminSession := mcpConnect(t, e, e.authToken)
	args := map[string]any{"owner": owner.Username, "key": project.Key}

	mcpCallExpectError(t, e, memberSession, "track_delete_project", args)
	if _, err := e.store.GetProject(e.ctx, project.ID); err != nil {
		t.Fatalf("project deleted by a write member: %v", err)
	}

	mcpCall(t, e, ownerSession, "track_delete_project", args)
	if _, err := e.store.GetProject(e.ctx, project.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after owner delete = %v, want ErrNotFound", err)
	}

	adminRemovable, err := e.store.CreateProjectForUser(e.ctx, owner.ID, uniqueProjectKey(t), "MCP admin deletable", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser admin removable: %v", err)
	}
	mcpCall(t, e, adminSession, "track_delete_project", map[string]any{"owner": owner.Username, "key": adminRemovable.Key})
	if _, err := e.store.GetProject(e.ctx, adminRemovable.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after admin delete = %v, want ErrNotFound", err)
	}
}

func TestMCPPublicProjectAccessAndBlocks(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	ownerSession := mcpConnect(t, e, e.authToken)
	outsider, outsiderToken := e.mustUserToken(t, "mcp-public-outsider")
	outsiderSession := mcpConnect(t, e, outsiderToken)
	projectArgs := map[string]any{"owner": e.ownerUsername, "key": e.projKey}

	if out := mcpCallExpectError(t, e, outsiderSession, "track_get_project_access", projectArgs); true {
		requireMCPErrorCode(t, out, "forbidden")
	}
	defaultOut := mcpCall(t, e, ownerSession, "track_get_project_access", projectArgs)
	defaults := decodeMCPField[model.ProjectAccessSettings](t, defaultOut, "access")
	if defaults.IsPublic || defaults.PublicIssueCreation {
		t.Fatalf("default project access = %+v", defaults)
	}
	updatedOut := mcpCall(t, e, ownerSession, "track_update_project_access", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "is_public": true, "public_issue_creation": true,
	})
	updated := decodeMCPField[model.ProjectAccessSettings](t, updatedOut, "access")
	if !updated.IsPublic || !updated.PublicIssueCreation {
		t.Fatalf("updated project access = %+v", updated)
	}
	publicOut := mcpCall(t, e, outsiderSession, "track_get_project_access", projectArgs)
	if public := decodeMCPField[model.ProjectAccessSettings](t, publicOut, "access"); public != updated {
		t.Fatalf("public project access = %+v, want %+v", public, updated)
	}
	if out := mcpCallExpectError(t, e, outsiderSession, "track_update_project_access", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "is_public": false, "public_issue_creation": false,
	}); true {
		requireMCPErrorCode(t, out, "forbidden")
	}
	createdOut := mcpCall(t, e, outsiderSession, "track_create_issue", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "title": "MCP public submission",
	})
	created := decodeMCPField[model.Issue](t, createdOut, "issue")
	if created.ReporterID == nil || *created.ReporterID != outsider.ID {
		t.Fatalf("public MCP issue reporter = %v, want %s", created.ReporterID, outsider.ID)
	}
	if out := mcpCallExpectError(t, e, outsiderSession, "track_create_issue", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "title": "MCP public assigned", "assignee_id": e.adminID.String(),
	}); true {
		requireMCPErrorCode(t, out, "forbidden")
	}
	blockOut := mcpCall(t, e, ownerSession, "track_block_project_user", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "username": outsider.Username,
	})
	block := decodeMCPField[model.ProjectUserBlock](t, blockOut, "block")
	if block.UserID != outsider.ID || block.ProjectID != e.projectID {
		t.Fatalf("MCP project block = %+v", block)
	}
	listOut := mcpCall(t, e, ownerSession, "track_list_project_blocks", projectArgs)
	blocks := decodeMCPField[[]model.ProjectUserBlock](t, listOut, "blocks")
	if len(blocks) != 1 || blocks[0].ID != block.ID {
		t.Fatalf("MCP project blocks = %+v", blocks)
	}
	if out := mcpCallExpectError(t, e, outsiderSession, "track_get_project_access", projectArgs); true {
		requireMCPErrorCode(t, out, "forbidden")
	}
	if out := mcpCallExpectError(t, e, ownerSession, "track_grant_project_member", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "username": outsider.Username,
	}); true {
		requireMCPErrorCode(t, out, "conflict")
	}
	if out := mcpCallExpectError(t, e, ownerSession, "track_block_project_user", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "username": "not valid!",
	}); true {
		requireMCPErrorCode(t, out, "validation_error")
	}
	if out := mcpCallExpectError(t, e, ownerSession, "track_block_project_user", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "username": e.ownerUsername,
	}); true {
		requireMCPErrorCode(t, out, "conflict")
	}
	mcpCall(t, e, ownerSession, "track_unblock_project_user", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "username": outsider.Username,
	})
	if out := mcpCallExpectError(t, e, ownerSession, "track_unblock_project_user", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "username": outsider.Username,
	}); true {
		requireMCPErrorCode(t, out, "not_found")
	}
}

func TestMCPSprintOptionalDates(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)

	out := mcpCall(t, e, session, "track_create_sprint", map[string]any{
		"owner": e.ownerUsername,
		"key":   e.projKey,
		"name":  "No dates",
	})
	sp := decodeMCPField[model.Sprint](t, out, "sprint")
	if sp.StartDate != nil || sp.EndDate != nil {
		t.Fatalf("created dates = %v..%v, want nil..nil", sp.StartDate, sp.EndDate)
	}

	out = mcpCall(t, e, session, "track_update_sprint", map[string]any{
		"owner":      e.ownerUsername,
		"key":        e.projKey,
		"sprint":     sp.Ref,
		"start_date": "2026-07-01",
		"end_date":   "2026-07-14",
	})
	sp = decodeMCPField[model.Sprint](t, out, "sprint")
	if sp.StartDate == nil || sp.EndDate == nil {
		t.Fatalf("scheduled dates = %v..%v, want range", sp.StartDate, sp.EndDate)
	}

	out = mcpCall(t, e, session, "track_update_sprint", map[string]any{
		"owner":       e.ownerUsername,
		"key":         e.projKey,
		"sprint":      sp.Ref,
		"clear_dates": true,
	})
	sp = decodeMCPField[model.Sprint](t, out, "sprint")
	if sp.StartDate != nil || sp.EndDate != nil {
		t.Fatalf("cleared dates = %v..%v, want nil..nil", sp.StartDate, sp.EndDate)
	}
}

func TestMCPListSprintsCompletedSortAndCursor(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	olderAt := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	middleAt := olderAt.Add(24 * time.Hour)
	newestAt := middleAt.Add(24 * time.Hour)
	older := createCompletedSprintAtFor(t, e, e.projectID, "MCP older", sprintTestDate(2026, 9, 1), sprintTestDate(2026, 9, 14), &olderAt)
	middle := createCompletedSprintAtFor(t, e, e.projectID, "MCP middle", sprintTestDate(2026, 8, 1), sprintTestDate(2026, 8, 14), &middleAt)
	newest := createCompletedSprintAtFor(t, e, e.projectID, "MCP newest", sprintTestDate(2026, 6, 1), sprintTestDate(2026, 6, 14), &newestAt)

	args := map[string]any{
		"owner":  e.ownerUsername,
		"key":    e.projKey,
		"status": string(model.SprintStatusCompleted),
		"sort":   string(store.ListSprintsSortCompleted),
		"limit":  2,
	}
	out := mcpCall(t, e, session, "track_list_sprints", args)
	page1 := decodeMCPField[[]model.Sprint](t, out, "items")
	if len(page1) != 2 || page1[0].ID != newest.ID || page1[1].ID != middle.ID {
		t.Fatalf("page 1 sprints = %+v, want newest/middle", page1)
	}
	next := decodeMCPField[*string](t, out, "next_cursor")
	if next == nil {
		t.Fatal("page 1 next_cursor = nil, want cursor")
	}

	args["cursor"] = *next
	out = mcpCall(t, e, session, "track_list_sprints", args)
	page2 := decodeMCPField[[]model.Sprint](t, out, "items")
	if len(page2) != 1 || page2[0].ID != older.ID {
		t.Fatalf("page 2 sprints = %+v, want older", page2)
	}
	if next = decodeMCPField[*string](t, out, "next_cursor"); next != nil {
		t.Fatalf("page 2 next_cursor = %q, want nil", *next)
	}

	for _, badArgs := range []map[string]any{
		{"owner": e.ownerUsername, "key": e.projKey, "sort": "banana"},
		{"owner": e.ownerUsername, "key": e.projKey, "sort": string(store.ListSprintsSortCompleted)},
	} {
		errOut := mcpCallExpectError(t, e, session, "track_list_sprints", badArgs)
		requireMCPErrorCode(t, errOut, "validation_error")
	}
}

func TestMCPListSprintHistoryIssues(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "MCP snapshot sprint"})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	planned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "MCP planned sprint"})
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}
	first := e.mustCreateIssue(t, "MCP first snapshot issue")
	second := e.mustCreateIssue(t, "MCP second snapshot issue")
	for _, issue := range []model.Issue{first, second} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sprint.ID}); err != nil {
			t.Fatalf("assign %s: %v", issue.Title, err)
		}
	}
	completed, err := e.store.CompleteSprint(e.ctx, sprint.ID)
	if err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	args := map[string]any{
		"owner":  e.ownerUsername,
		"key":    e.projKey,
		"sprint": completed.Ref,
		"limit":  1,
	}
	out := mcpCall(t, e, session, "track_list_sprint_history_issues", args)
	page1 := decodeMCPField[[]model.Issue](t, out, "items")
	if len(page1) != 1 || page1[0].ID != first.ID {
		t.Fatalf("page 1 issues = %+v, want first", page1)
	}
	next := decodeMCPField[*string](t, out, "next_cursor")
	if next == nil {
		t.Fatal("page 1 next_cursor = nil, want cursor")
	}
	args["cursor"] = *next
	out = mcpCall(t, e, session, "track_list_sprint_history_issues", args)
	page2 := decodeMCPField[[]model.Issue](t, out, "items")
	if len(page2) != 1 || page2[0].ID != second.ID {
		t.Fatalf("page 2 issues = %+v, want second", page2)
	}
	if next = decodeMCPField[*string](t, out, "next_cursor"); next != nil {
		t.Fatalf("page 2 next_cursor = %q, want nil", *next)
	}

	for _, badArgs := range []map[string]any{
		{"owner": e.ownerUsername, "key": e.projKey, "sprint": planned.Ref},
		{"owner": e.ownerUsername, "key": e.projKey, "sprint": completed.Ref, "cursor": "bad"},
		{"owner": e.ownerUsername, "key": e.projKey, "sprint": completed.Ref, "limit": -1},
	} {
		errOut := mcpCallExpectError(t, e, session, "track_list_sprint_history_issues", badArgs)
		requireMCPErrorCode(t, errOut, "validation_error")
	}
}

func TestMCPRequiresAPIToken(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024*1024)
	e := newMCPHTTPEnv(t, storageSvc)

	client := mcp.NewClient(&mcp.Implementation{Name: "track-slash-test", Version: "v1"}, nil)
	if _, err := client.Connect(e.ctx, &mcp.StreamableClientTransport{Endpoint: e.ts.URL + "/mcp"}, nil); err == nil {
		t.Fatalf("unauthenticated MCP connect succeeded")
	}

	sessionToken, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{
		UserID: e.adminID,
		Kind:   model.AuthTokenKindSession,
		Name:   "session",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken session: %v", err)
	}
	if _, err := client.Connect(e.ctx, &mcp.StreamableClientTransport{
		Endpoint:     e.ts.URL + "/mcp",
		OAuthHandler: staticOAuthHandler{token: sessionToken.RawToken},
	}, nil); err == nil {
		t.Fatalf("session token MCP connect succeeded")
	}
}

func TestMCPAcceptsStaleSessionID(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024*1024)
	e := newMCPHTTPEnv(t, storageSvc)

	body := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	req, err := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+"/mcp", body)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	req.Header.Set("Mcp-Session-Id", "stale-session-after-restart")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST stale session: %v", err)
	}
	defer resp.Body.Close()
	requireSecurityHeadersForTest(t, resp.Header)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.StatusCode, data)
	}
	var got struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v body=%s", err, data)
	}
	if got.Error != nil {
		t.Fatalf("unexpected error: %+v", got.Error)
	}
	for _, tool := range got.Result.Tools {
		if tool.Name == "track_get_me" {
			return
		}
	}
	t.Fatalf("track_get_me not found in tools: %+v", got.Result.Tools)
}

func TestMCPProjectIssueCommentParity(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024*1024)
	e := newMCPHTTPEnv(t, storageSvc)
	session := mcpConnect(t, e, e.authToken)

	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) < 40 {
		t.Fatalf("tool count = %d, want broad parity", len(tools.Tools))
	}

	projectOut := mcpCall(t, e, session, "track_get_project", map[string]any{
		"owner": e.ownerUsername,
		"key":   e.projKey,
	})
	project := decodeMCPField[model.Project](t, projectOut, "project")
	if project.ID != e.projectID {
		t.Fatalf("project id = %s, want %s", project.ID, e.projectID)
	}

	issueOut := mcpCall(t, e, session, "track_create_issue", map[string]any{
		"owner":       e.ownerUsername,
		"key":         e.projKey,
		"title":       "MCP issue",
		"description": "created by MCP",
		"priority":    "P1",
	})
	issue := decodeMCPField[model.Issue](t, issueOut, "issue")
	if issue.Identifier == "" || issue.Title != "MCP issue" || issue.Priority != model.PriorityP1 {
		t.Fatalf("bad issue from MCP: %+v", issue)
	}

	code, body := e.do(t, http.MethodGet, e.issuePath(issue), nil)
	if code != http.StatusOK {
		t.Fatalf("API get issue code = %d body=%s", code, body)
	}
	apiIssue := decode[model.Issue](t, body)
	if apiIssue.ID != issue.ID || apiIssue.Identifier != issue.Identifier {
		t.Fatalf("API/MCP issue mismatch: api=%+v mcp=%+v", apiIssue, issue)
	}

	commentOut := mcpCall(t, e, session, "track_create_comment", map[string]any{
		"owner": e.ownerUsername,
		"issue": issue.Identifier,
		"body":  "MCP comment",
	})
	comment := decodeMCPField[model.Comment](t, commentOut, "comment")
	if comment.Ref != "comment-1" || comment.Body != "MCP comment" {
		t.Fatalf("bad comment from MCP: %+v", comment)
	}

	listOut := mcpCall(t, e, session, "track_list_comments", map[string]any{
		"owner": e.ownerUsername,
		"issue": issue.Identifier,
	})
	comments := decodeMCPField[[]model.Comment](t, listOut, "items")
	if len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("comments = %+v, want %+v", comments, comment)
	}

	_, memberToken := e.mustProjectMemberToken(t, "mcp-comment-other")
	memberSession := mcpConnect(t, e, memberToken)
	errOut := mcpCallExpectError(t, e, memberSession, "track_delete_comment", map[string]any{
		"owner":   e.ownerUsername,
		"issue":   issue.Identifier,
		"comment": comment.Ref,
	})
	requireMCPErrorCode(t, errOut, "forbidden")
	gotComment, err := e.store.GetComment(e.ctx, comment.ID)
	if err != nil {
		t.Fatalf("GetComment after forbidden delete: %v", err)
	}
	if gotComment.Body != "MCP comment" {
		t.Fatalf("forbidden delete changed comment: %+v", gotComment)
	}
	mcpCall(t, e, session, "track_delete_comment", map[string]any{
		"owner":   e.ownerUsername,
		"issue":   issue.Identifier,
		"comment": comment.Ref,
	})
	if _, err := e.store.GetComment(e.ctx, comment.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetComment after author delete err = %v, want ErrNotFound", err)
	}
}

func TestMCPCreateIssuePeopleRequireProjectMembers(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024*1024)
	e := newMCPHTTPEnv(t, storageSvc)
	session := mcpConnect(t, e, e.authToken)
	member, _ := e.mustProjectMemberToken(t, "mcp-issue-member")
	nonMember, _ := e.mustUserToken(t, "mcp-issue-outsider")

	out := mcpCall(t, e, session, "track_create_issue", map[string]any{
		"owner":       e.ownerUsername,
		"key":         e.projKey,
		"title":       "MCP member people",
		"assignee_id": member.ID.String(),
		"reporter_id": member.ID.String(),
	})
	issue := decodeMCPField[model.Issue](t, out, "issue")
	if issue.AssigneeID == nil || *issue.AssigneeID != member.ID || issue.ReporterID == nil || *issue.ReporterID != member.ID {
		t.Fatalf("issue people = assignee %v reporter %v, want %s", issue.AssigneeID, issue.ReporterID, member.ID)
	}

	listOut := mcpCall(t, e, session, "track_list_issues", map[string]any{
		"owner":        e.ownerUsername,
		"key":          e.projKey,
		"assignee_ids": []string{member.ID.String()},
	})
	assigned := decodeMCPField[[]model.Issue](t, listOut, "items")
	if len(assigned) != 1 || assigned[0].ID != issue.ID {
		t.Fatalf("assigned issues = %+v, want issue %s", assigned, issue.ID)
	}

	updateOut := mcpCall(t, e, session, "track_update_issue", map[string]any{
		"owner":       e.ownerUsername,
		"issue":       issue.Identifier,
		"assignee_id": member.ID.String(),
	})
	updated := decodeMCPField[model.Issue](t, updateOut, "issue")
	if updated.AssigneeID == nil || *updated.AssigneeID != member.ID {
		t.Fatalf("updated assignee = %v, want %s", updated.AssigneeID, member.ID)
	}

	out = mcpCallExpectError(t, e, session, "track_create_issue", map[string]any{
		"owner":       e.ownerUsername,
		"key":         e.projKey,
		"title":       "MCP invalid assignee",
		"assignee_id": "not-a-uuid",
	})
	requireMCPErrorCode(t, out, "validation_error")

	out = mcpCallExpectError(t, e, session, "track_create_issue", map[string]any{
		"owner":       e.ownerUsername,
		"key":         e.projKey,
		"title":       "MCP nonmember assignee",
		"assignee_id": nonMember.ID.String(),
	})
	requireMCPErrorCode(t, out, "not_found")

	out = mcpCallExpectError(t, e, session, "track_create_sub_issue", map[string]any{
		"owner":       e.ownerUsername,
		"issue":       issue.Identifier,
		"title":       "MCP child nonmember",
		"assignee_id": nonMember.ID.String(),
	})
	requireMCPErrorCode(t, out, "not_found")

	out = mcpCall(t, e, session, "track_create_sub_issue", map[string]any{
		"owner":       e.ownerUsername,
		"issue":       issue.Identifier,
		"title":       "MCP child member",
		"assignee_id": member.ID.String(),
	})
	child := decodeMCPField[model.Issue](t, out, "issue")
	if child.AssigneeID == nil || *child.AssigneeID != member.ID {
		t.Fatalf("child assignee = %v, want %s", child.AssigneeID, member.ID)
	}
}

func TestMCPListIssuesFiltersSortAndCursor(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024*1024)
	e := newMCPHTTPEnv(t, storageSvc)
	session := mcpConnect(t, e, e.authToken)

	todoP3, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "mcp todo p3",
		Priority:  model.PriorityP3,
	})
	if err != nil {
		t.Fatalf("CreateIssue todo: %v", err)
	}
	doneP0, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "mcp done p0",
		Priority:  model.PriorityP0,
	})
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	progressP1, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "mcp progress p1",
		Priority:  model.PriorityP1,
	})
	if err != nil {
		t.Fatalf("CreateIssue progress: %v", err)
	}
	skippedP4, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "mcp skipped p4",
		Priority:  model.PriorityP4,
	})
	if err != nil {
		t.Fatalf("CreateIssue skipped: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, doneP0.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("set done: %v", err)
	}
	progress := model.StatusInProgress
	if _, err := e.store.UpdateIssue(e.ctx, progressP1.ID, store.UpdateIssueParams{Status: &progress}); err != nil {
		t.Fatalf("set progress: %v", err)
	}
	closed := model.StatusClosed
	reason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, skippedP4.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &reason}); err != nil {
		t.Fatalf("set closed: %v", err)
	}

	args := map[string]any{
		"owner":      e.ownerUsername,
		"key":        e.projKey,
		"status":     string(model.StatusDone),
		"statuses":   []string{string(model.StatusInProgress), string(model.StatusTodo)},
		"priority":   string(model.PriorityP0),
		"priorities": []string{string(model.PriorityP1), string(model.PriorityP3)},
		"sort":       string(store.ListIssuesSortPriority),
		"limit":      2,
	}
	out := mcpCall(t, e, session, "track_list_issues", args)
	page1 := decodeMCPField[[]model.Issue](t, out, "items")
	if len(page1) != 2 || page1[0].ID != doneP0.ID || page1[1].ID != progressP1.ID {
		t.Fatalf("page 1 issues = %+v, want done/progress by priority", page1)
	}
	next := decodeMCPField[*string](t, out, "next_cursor")
	if next == nil {
		t.Fatal("page 1 next_cursor = nil, want cursor")
	}

	args["cursor"] = *next
	out = mcpCall(t, e, session, "track_list_issues", args)
	page2 := decodeMCPField[[]model.Issue](t, out, "items")
	if len(page2) != 1 || page2[0].ID != todoP3.ID {
		t.Fatalf("page 2 issues = %+v, want todo p3", page2)
	}
	next = decodeMCPField[*string](t, out, "next_cursor")
	if next != nil {
		t.Fatalf("page 2 next_cursor = %q, want nil", *next)
	}

	out = mcpCallExpectError(t, e, session, "track_list_issues", map[string]any{
		"owner":    e.ownerUsername,
		"key":      e.projKey,
		"priority": "P9",
	})
	requireMCPErrorCode(t, out, "validation_error")
}

func TestMCPIssueLinksRejectCrossProjectRefsBeforeLookup(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	source, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "MCP link source"})
	if err != nil {
		t.Fatalf("CreateIssue source: %v", err)
	}
	target, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "MCP link target"})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	other, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "other mcp links", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}

	errOut := mcpCallExpectError(t, e, session, "track_create_link", map[string]any{
		"owner":        e.ownerUsername,
		"issue":        source.Identifier,
		"target_issue": other.Key + "-999999",
		"link_type":    "blocks",
	})
	requireMCPErrorCode(t, errOut, "conflict")

	out := mcpCall(t, e, session, "track_create_link", map[string]any{
		"owner":        e.ownerUsername,
		"issue":        source.Identifier,
		"target_issue": target.Identifier,
		"link_type":    "blocks",
	})
	link := decodeMCPField[model.IssueLink](t, out, "link")

	errOut = mcpCallExpectError(t, e, session, "track_update_link", map[string]any{
		"owner":        e.ownerUsername,
		"key":          e.projKey,
		"link":         link.Ref,
		"source_issue": other.Key + "-999999",
		"target_issue": target.Identifier,
		"link_type":    "blocks",
	})
	requireMCPErrorCode(t, errOut, "conflict")

	errOut = mcpCallExpectError(t, e, session, "track_update_link", map[string]any{
		"owner":        e.ownerUsername,
		"key":          e.projKey,
		"link":         link.Ref,
		"source_issue": source.Identifier,
		"target_issue": other.Key + "-999999",
		"link_type":    "blocks",
	})
	requireMCPErrorCode(t, errOut, "conflict")
}

func TestMCPAttachmentAndResourceRoundTrip(t *testing.T) {
	t.Parallel()
	storageSvc, _ := newLocalStorageService(t, 1024*1024)
	e := newMCPHTTPEnv(t, storageSvc)
	session := mcpConnect(t, e, e.authToken)

	issueOut := mcpCall(t, e, session, "track_create_issue", map[string]any{
		"owner": e.ownerUsername,
		"key":   e.projKey,
		"title": "Attachment issue",
	})
	issue := decodeMCPField[model.Issue](t, issueOut, "issue")
	content := []byte("hello from mcp attachment")
	attachOut := mcpCall(t, e, session, "track_create_attachment", map[string]any{
		"owner":          e.ownerUsername,
		"issue":          issue.Identifier,
		"filename":       "note.txt",
		"content_type":   "text/plain",
		"content_base64": base64.StdEncoding.EncodeToString(content),
	})
	attachment := decodeMCPField[model.IssueAttachment](t, attachOut, "attachment")
	if attachment.Object.Ref != "object-1" || attachment.Object.Filename != "note.txt" {
		t.Fatalf("bad attachment: %+v", attachment)
	}

	readOut := mcpCall(t, e, session, "track_read_attachment_content", map[string]any{
		"owner":  e.ownerUsername,
		"issue":  issue.Identifier,
		"object": attachment.Object.Ref,
	})
	encoded := decodeMCPField[string](t, readOut, "content_base64")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	if string(decoded) != string(content) {
		t.Fatalf("content = %q, want %q", decoded, content)
	}

	res, err := session.ReadResource(e.ctx, &mcp.ReadResourceParams{
		URI: "track://attachment-content/" + e.ownerUsername + "/" + issue.Identifier + "/" + attachment.Object.Ref,
	})
	if err != nil {
		t.Fatalf("ReadResource attachment content: %v", err)
	}
	if len(res.Contents) != 1 || string(res.Contents[0].Blob) != string(content) {
		t.Fatalf("resource contents = %+v, want %q", res.Contents, content)
	}

	sprintOut := mcpCall(t, e, session, "track_create_sprint", map[string]any{
		"owner": e.ownerUsername,
		"key":   e.projKey,
		"name":  "Attachment sprint",
		"goal":  "Attach the plan",
	})
	sprint := decodeMCPField[model.Sprint](t, sprintOut, "sprint")
	sprintContent := []byte("sprint attachment content")
	sprintAttachOut := mcpCall(t, e, session, "track_create_sprint_attachment", map[string]any{
		"owner":          e.ownerUsername,
		"key":            e.projKey,
		"sprint":         sprint.Ref,
		"filename":       "plan.txt",
		"content_type":   "text/plain",
		"content_base64": base64.StdEncoding.EncodeToString(sprintContent),
	})
	sprintAttachment := decodeMCPField[model.SprintAttachment](t, sprintAttachOut, "attachment")
	if sprintAttachment.SprintID != sprint.ID || sprintAttachment.Object.Ref != "object-2" {
		t.Fatalf("bad sprint attachment: %+v", sprintAttachment)
	}

	listOut := mcpCall(t, e, session, "track_list_sprint_attachments", map[string]any{
		"owner":  e.ownerUsername,
		"key":    e.projKey,
		"sprint": sprint.Ref,
	})
	if items := decodeMCPField[[]model.SprintAttachment](t, listOut, "items"); len(items) != 1 || items[0].ID != sprintAttachment.ID {
		t.Fatalf("sprint attachment list = %+v", items)
	}

	readSprintOut := mcpCall(t, e, session, "track_read_sprint_attachment_content", map[string]any{
		"owner":  e.ownerUsername,
		"key":    e.projKey,
		"sprint": sprint.Ref,
		"object": sprintAttachment.Object.Ref,
	})
	encoded = decodeMCPField[string](t, readSprintOut, "content_base64")
	decoded, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != string(sprintContent) {
		t.Fatalf("sprint content = %q err=%v", decoded, err)
	}

	res, err = session.ReadResource(e.ctx, &mcp.ReadResourceParams{
		URI: "track://sprint-attachment-content/" + e.ownerUsername + "/" + e.projKey + "/" + sprint.Ref + "/" + sprintAttachment.Object.Ref,
	})
	if err != nil || len(res.Contents) != 1 || string(res.Contents[0].Blob) != string(sprintContent) {
		t.Fatalf("sprint resource contents = %+v err=%v", res, err)
	}

	mcpCall(t, e, session, "track_delete_sprint_attachment", map[string]any{
		"owner":  e.ownerUsername,
		"key":    e.projKey,
		"sprint": sprint.Ref,
		"object": sprintAttachment.Object.Ref,
	})

	projectContent := []byte("project description attachment")
	projectAttachOut := mcpCall(t, e, session, "track_create_project_attachment", map[string]any{
		"owner":          e.ownerUsername,
		"key":            e.projKey,
		"filename":       "project.txt",
		"content_type":   "text/plain",
		"content_base64": base64.StdEncoding.EncodeToString(projectContent),
	})
	projectAttachment := decodeMCPField[model.ProjectAttachment](t, projectAttachOut, "attachment")
	if projectAttachment.ProjectID != e.projectID || projectAttachment.Object.Ref != "object-3" {
		t.Fatalf("bad project attachment: %+v", projectAttachment)
	}
	projectListOut := mcpCall(t, e, session, "track_list_project_attachments", map[string]any{
		"owner": e.ownerUsername,
		"key":   e.projKey,
	})
	if items := decodeMCPField[[]model.ProjectAttachment](t, projectListOut, "items"); len(items) != 1 || items[0].ID != projectAttachment.ID {
		t.Fatalf("project attachment list = %+v", items)
	}
	readProjectOut := mcpCall(t, e, session, "track_read_project_attachment_content", map[string]any{
		"owner":  e.ownerUsername,
		"key":    e.projKey,
		"object": projectAttachment.Object.Ref,
	})
	encoded = decodeMCPField[string](t, readProjectOut, "content_base64")
	decoded, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != string(projectContent) {
		t.Fatalf("project content = %q err=%v", decoded, err)
	}
	mcpCall(t, e, session, "track_delete_project_attachment", map[string]any{
		"owner":  e.ownerUsername,
		"key":    e.projKey,
		"object": projectAttachment.Object.Ref,
	})

	contextOut := mcpCall(t, e, session, "track_create_project_context", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "title": "Runbook", "body": "# Runbook",
	})
	contextItem := decodeMCPField[model.ProjectContext](t, contextOut, "context")
	if contextItem.ContentType != "text/markdown; charset=utf-8" || contextItem.Position == nil || *contextItem.Position != 1 {
		t.Fatalf("bad project context page: %+v", contextItem)
	}
	secondContextOut := mcpCall(t, e, session, "track_create_project_context", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "title": "Reference", "body": "",
	})
	secondContext := decodeMCPField[model.ProjectContext](t, secondContextOut, "context")
	updatedContextOut := mcpCall(t, e, session, "track_update_project_context", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "context": contextItem.Ref,
		"body": "Runbook text", "content_type": "text/plain", "position": 2,
	})
	contextItem = decodeMCPField[model.ProjectContext](t, updatedContextOut, "context")
	if contextItem.Body != "Runbook text" || contextItem.ContentType != "text/plain; charset=utf-8" || contextItem.Position == nil || *contextItem.Position != 2 {
		t.Fatalf("updated project context page: %+v", contextItem)
	}
	contextListPagesOut := mcpCall(t, e, session, "track_list_project_context", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey,
	})
	contextPages := decodeMCPField[[]model.ProjectContextSummary](t, contextListPagesOut, "items")
	if len(contextPages) != 2 || contextPages[0].Ref != secondContext.Ref || contextPages[1].Ref != contextItem.Ref {
		t.Fatalf("ordered MCP context pages = %+v", contextPages)
	}
	contextGetOut := mcpCall(t, e, session, "track_get_project_context", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "context": contextItem.Ref,
	})
	if got := decodeMCPField[model.ProjectContext](t, contextGetOut, "context"); got.ID != contextItem.ID || got.Body != "Runbook text" {
		t.Fatalf("MCP project context get = %+v", got)
	}
	contextContent := []byte("context attachment")
	contextAttachOut := mcpCall(t, e, session, "track_create_project_context_attachment", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "context": contextItem.Ref,
		"filename": "runbook.txt", "content_type": "text/plain",
		"content_base64": base64.StdEncoding.EncodeToString(contextContent),
	})
	contextAttachment := decodeMCPField[model.ContextAttachment](t, contextAttachOut, "attachment")
	if contextAttachment.ContextID != contextItem.ID || contextAttachment.Object.Ref != "object-4" {
		t.Fatalf("bad context attachment: %+v", contextAttachment)
	}
	contextListOut := mcpCall(t, e, session, "track_list_project_context_attachments", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "context": contextItem.Ref,
	})
	if items := decodeMCPField[[]model.ContextAttachment](t, contextListOut, "items"); len(items) != 1 || items[0].ID != contextAttachment.ID {
		t.Fatalf("context attachment list = %+v", items)
	}
	readContextOut := mcpCall(t, e, session, "track_read_project_context_attachment_content", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "context": contextItem.Ref, "object": contextAttachment.Object.Ref,
	})
	encoded = decodeMCPField[string](t, readContextOut, "content_base64")
	decoded, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != string(contextContent) {
		t.Fatalf("context content = %q err=%v", decoded, err)
	}
	mcpCall(t, e, session, "track_delete_project_context_attachment", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "context": contextItem.Ref, "object": contextAttachment.Object.Ref,
	})
	mcpCall(t, e, session, "track_delete_project_context", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "context": secondContext.Ref,
	})
}
