package server_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type mcpPropertySchema struct {
	Type   any                `json:"type"`
	Format string             `json:"format"`
	Enum   []any              `json:"enum"`
	Items  *mcpPropertySchema `json:"items"`
}

func mcpToolSchemas(t *testing.T, e *httpEnv, session *mcp.ClientSession) (map[string]map[string]mcpPropertySchema, map[string]*mcp.Tool) {
	t.Helper()
	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	schemas := map[string]map[string]mcpPropertySchema{}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal %s schema: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]mcpPropertySchema `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("unmarshal %s schema: %v", tool.Name, err)
		}
		schemas[tool.Name] = schema.Properties
		byName[tool.Name] = tool
	}
	return schemas, byName
}

// Fields with a fixed set of values list them in the tool schemas, an
// optional one allowing null as well, and sprint dates say they are dates.
func TestMCPSchemaEnumsAndDates(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	schemas, _ := mcpToolSchemas(t, e, session)

	statuses := []any{"todo", "in_progress", "done", "closed"}
	priorities := []any{"P0", "P1", "P2", "P3", "P4"}
	closeReasons := []any{"duplicate", "wont_do", "invalid"}
	linkTypes := []any{"blocks", "duplicates", "relates_to", "clones"}
	sprintStatuses := []any{"planned", "active", "completed"}
	withNull := func(values []any) []any { return append(slices.Clip(values), nil) }
	for _, tc := range []struct {
		tool, field string
		want        []any
	}{
		{"track_update_issue", "status", withNull(statuses)},
		{"track_update_issue", "priority", withNull(priorities)},
		{"track_update_issue", "close_reason", withNull(closeReasons)},
		{"track_create_issue", "priority", withNull(priorities)},
		{"track_create_sub_issue", "priority", withNull(priorities)},
		{"track_list_issues", "status", statuses},
		{"track_list_issues", "priority", priorities},
		{"track_create_link", "link_type", linkTypes},
		{"track_update_link", "link_type", linkTypes},
		{"track_list_sprints", "status", sprintStatuses},
		{"track_update_sprint", "status", withNull(sprintStatuses)},
	} {
		prop, ok := schemas[tc.tool][tc.field]
		if !ok {
			t.Fatalf("%s has no %s", tc.tool, tc.field)
		}
		if !slices.Equal(prop.Enum, tc.want) {
			t.Fatalf("%s %s enum = %v, want %v", tc.tool, tc.field, prop.Enum, tc.want)
		}
	}
	for field, want := range map[string][]any{"statuses": statuses, "priorities": priorities} {
		if items := schemas["track_list_issues"][field].Items; items == nil || !slices.Equal(items.Enum, want) {
			t.Fatalf("track_list_issues %s items = %+v, want enum %v", field, items, want)
		}
	}
	for _, tool := range []string{"track_create_sprint", "track_update_sprint"} {
		for _, field := range []string{"start_date", "end_date"} {
			if prop := schemas[tool][field]; prop.Format != "date" {
				t.Fatalf("%s %s = %+v, want date format", tool, field, prop)
			}
		}
	}

	// A value outside the enum is refused before the tool runs; an optional
	// enum field sent as null is left as it is.
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "enum"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	args := map[string]any{"owner": e.ownerUsername, "issue": issue.Identifier, "status": "finished"}
	res, err := session.CallTool(e.ctx, &mcp.CallToolParams{Name: "track_update_issue", Arguments: args})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	content, _ := json.Marshal(res.Content)
	if !res.IsError || !strings.Contains(string(content), "enum") {
		t.Fatalf("status outside the enum = %s, want refused", content)
	}
	args = map[string]any{"owner": e.ownerUsername, "issue": issue.Identifier, "status": nil, "priority": nil, "title": "still todo"}
	updated := decodeMCPField[model.Issue](t, mcpCall(t, e, session, "track_update_issue", args), "issue")
	if updated.Status != model.StatusTodo || updated.Title != "still todo" {
		t.Fatalf("update with null status = %+v", updated)
	}
}

// Tools that only add something say they aren't destructive; tools that
// change or remove what is there say they are.
func TestMCPToolDestructiveHints(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	_, tools := mcpToolSchemas(t, e, session)

	for name, tool := range tools {
		hints := tool.Annotations
		if hints == nil || hints.DestructiveHint == nil {
			t.Fatalf("%s has no destructive hint", name)
		}
		if hints.ReadOnlyHint && *hints.DestructiveHint {
			t.Fatalf("%s is read-only and destructive", name)
		}
	}
	for _, name := range []string{
		"track_create_issue", "track_create_sub_issue", "track_create_comment", "track_create_sprint",
		"track_create_tag", "track_attach_issue_tag", "track_create_project", "track_create_attachment",
		"track_create_project_context", "track_create_issue_context", "track_bulk_link_issue_contexts",
		"track_create_whiteboard_page", "track_create_my_token",
	} {
		if hints := tools[name].Annotations; hints.ReadOnlyHint || *hints.DestructiveHint {
			t.Fatalf("%s annotations = %+v, want additive", name, hints)
		}
	}
	// Creating a duplicates link closes its source, so it is not only additive.
	for _, name := range []string{
		"track_create_link", "track_update_issue", "track_update_comment", "track_delete_issue",
		"track_delete_comment", "track_revoke_my_token", "track_complete_sprint", "track_update_project_access",
	} {
		if hints := tools[name].Annotations; hints.ReadOnlyHint || !*hints.DestructiveHint {
			t.Fatalf("%s annotations = %+v, want destructive", name, hints)
		}
	}
}

// The plan_sprint prompt's sprint is optional, and leaving it out must not
// leave a placeholder in the text.
func TestMCPPlanSprintPrompt(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	text := func(args map[string]string) string {
		t.Helper()
		res, err := session.GetPrompt(e.ctx, &mcp.GetPromptParams{Name: "plan_sprint", Arguments: args})
		if err != nil {
			t.Fatalf("GetPrompt: %v", err)
		}
		if len(res.Messages) != 1 {
			t.Fatalf("prompt messages = %+v", res.Messages)
		}
		content, ok := res.Messages[0].Content.(*mcp.TextContent)
		if !ok {
			t.Fatalf("prompt content = %T", res.Messages[0].Content)
		}
		return content.Text
	}
	project := map[string]string{"owner": e.ownerUsername, "key": e.projKey}
	without := text(project)
	if strings.Contains(without, "{{") || strings.Contains(without, "sprint=") || !strings.Contains(without, "key="+e.projKey) {
		t.Fatalf("prompt without sprint = %q", without)
	}
	project["sprint"] = "sprint-3"
	if with := text(project); !strings.Contains(with, "sprint=sprint-3") || strings.Contains(with, "{{") {
		t.Fatalf("prompt with sprint = %q", with)
	}
}

func TestMCPCreateMyTokenRefusesSessionKind(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	out := mcpCallExpectError(t, e, session, "track_create_my_token", map[string]any{"name": "cookie", "kind": "session"})
	requireMCPErrorCode(t, out, "validation_error")
	tokens, err := e.store.ListAuthTokens(e.ctx, e.adminID)
	if err != nil {
		t.Fatalf("ListAuthTokens: %v", err)
	}
	for _, token := range tokens {
		if token.Kind == model.AuthTokenKindSession {
			t.Fatalf("a session token was created: %+v", token)
		}
	}
	created := mcpCall(t, e, session, "track_create_my_token", map[string]any{"name": "agent", "kind": "api"})
	if _, ok := created["token"]; !ok {
		t.Fatalf("create api token = %v", created)
	}
}
