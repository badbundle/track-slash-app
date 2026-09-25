package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestMCPWhiteboardPageParity(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)

	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"track_list_whiteboard_pages":  true,
		"track_get_whiteboard_page":    true,
		"track_create_whiteboard_page": false,
		"track_update_whiteboard_page": false,
		"track_delete_whiteboard_page": false,
	}
	found := 0
	for _, tool := range tools.Tools {
		if readOnly, ok := want[tool.Name]; ok {
			found++
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
				t.Fatalf("%s annotations = %+v, want read-only %v", tool.Name, tool.Annotations, readOnly)
			}
		}
	}
	if found != len(want) {
		t.Fatalf("found %d whiteboard tools, want %d", found, len(want))
	}

	project := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range project {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	first := decodeMCPField[model.WhiteboardPage](t, mcpCall(t, e, session, "track_create_whiteboard_page", with(map[string]any{"title": "Agent notes"})), "page")
	if first.Ref != "whiteboard-1" || first.Body != "" || first.CreatedByID != e.adminID {
		t.Fatalf("created page = %+v", first)
	}
	second := decodeMCPField[model.WhiteboardPage](t, mcpCall(t, e, session, "track_create_whiteboard_page", with(map[string]any{"title": "Plan", "body": "- step one"})), "page")
	if second.Ref != "whiteboard-2" || second.Body != "- step one" {
		t.Fatalf("second page = %+v", second)
	}

	listOut := mcpCall(t, e, session, "track_list_whiteboard_pages", with(map[string]any{"limit": 1}))
	items := decodeMCPField[[]model.WhiteboardPageSummary](t, listOut, "items")
	next := decodeMCPField[*string](t, listOut, "next_cursor")
	if len(items) != 1 || items[0].Ref != second.Ref || next == nil {
		t.Fatalf("list page 1 = %+v next=%v", items, next)
	}
	listOut = mcpCall(t, e, session, "track_list_whiteboard_pages", with(map[string]any{"limit": 1, "cursor": *next}))
	items = decodeMCPField[[]model.WhiteboardPageSummary](t, listOut, "items")
	if len(items) != 1 || items[0].Ref != first.Ref || decodeMCPField[*string](t, listOut, "next_cursor") != nil {
		t.Fatalf("list page 2 = %+v", items)
	}

	got := decodeMCPField[model.WhiteboardPage](t, mcpCall(t, e, session, "track_get_whiteboard_page", with(map[string]any{"page": second.Ref})), "page")
	if got.ID != second.ID || got.Body != second.Body {
		t.Fatalf("got page = %+v", got)
	}

	updated := decodeMCPField[model.WhiteboardPage](t, mcpCall(t, e, session, "track_update_whiteboard_page", with(map[string]any{"page": first.Ref, "title": "Agent scratch", "body": "Found a *thing*."})), "page")
	if updated.Title != "Agent scratch" || updated.Body != "Found a *thing*." {
		t.Fatalf("updated page = %+v", updated)
	}

	res, err := session.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: "track://whiteboard/" + e.ownerUsername + "/" + e.projKey + "/" + first.Ref})
	if err != nil {
		t.Fatalf("ReadResource whiteboard: %v", err)
	}
	var resource model.WhiteboardPage
	if len(res.Contents) != 1 || json.Unmarshal([]byte(res.Contents[0].Text), &resource) != nil || resource.Body != "Found a *thing*." {
		t.Fatalf("whiteboard resource = %+v", res.Contents)
	}
	for _, uri := range []string{
		"track://whiteboard/" + e.ownerUsername + "/" + e.projKey,
		"track://whiteboard/" + e.ownerUsername + "/" + e.projKey + "/whiteboard-999",
	} {
		if _, err := session.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("ReadResource %s err = nil, want not found", uri)
		}
	}

	mcpCall(t, e, session, "track_delete_whiteboard_page", with(map[string]any{"page": first.Ref}))
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_get_whiteboard_page", with(map[string]any{"page": first.Ref})), "not_found")
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_delete_whiteboard_page", with(map[string]any{"page": first.Ref})), "not_found")
}

func TestMCPWhiteboardPageValidationAndPermissions(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	page := e.mustWhiteboardPage(t, "Shared", "Body")
	args := func(extra map[string]any) map[string]any {
		out := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
		code string
	}{
		{name: "create blank title", tool: "track_create_whiteboard_page", args: args(map[string]any{"title": " "}), code: "validation_error"},
		{name: "create long body", tool: "track_create_whiteboard_page", args: args(map[string]any{"title": "x", "body": strings.Repeat("b", 100001)}), code: "validation_error"},
		{name: "create unknown project", tool: "track_create_whiteboard_page", args: map[string]any{"owner": e.ownerUsername, "key": "NOPE", "title": "x"}, code: "not_found"},
		{name: "list bad limit", tool: "track_list_whiteboard_pages", args: args(map[string]any{"limit": -1}), code: "validation_error"},
		{name: "list bad cursor", tool: "track_list_whiteboard_pages", args: args(map[string]any{"cursor": "not-a-cursor!"}), code: "validation_error"},
		{name: "list unknown project", tool: "track_list_whiteboard_pages", args: map[string]any{"owner": e.ownerUsername, "key": "NOPE"}, code: "not_found"},
		{name: "get malformed ref", tool: "track_get_whiteboard_page", args: args(map[string]any{"page": "context-1"}), code: "validation_error"},
		{name: "get unknown project", tool: "track_get_whiteboard_page", args: map[string]any{"owner": e.ownerUsername, "key": "NOPE", "page": page.Ref}, code: "not_found"},
		{name: "update blank title", tool: "track_update_whiteboard_page", args: args(map[string]any{"page": page.Ref, "title": ""}), code: "validation_error"},
		{name: "update long body", tool: "track_update_whiteboard_page", args: args(map[string]any{"page": page.Ref, "body": strings.Repeat("b", 100001)}), code: "validation_error"},
		{name: "update unknown ref", tool: "track_update_whiteboard_page", args: args(map[string]any{"page": "whiteboard-999", "title": "x"}), code: "not_found"},
		{name: "update unknown project", tool: "track_update_whiteboard_page", args: map[string]any{"owner": e.ownerUsername, "key": "NOPE", "page": page.Ref, "title": "x"}, code: "not_found"},
		{name: "delete malformed ref", tool: "track_delete_whiteboard_page", args: args(map[string]any{"page": "nope"}), code: "validation_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireMCPErrorCode(t, mcpCallExpectError(t, e, session, tc.tool, tc.args), tc.code)
		})
	}

	readonly, readonlyToken := e.mustUserToken(t, "mcp-whiteboard-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	readonlySession := mcpConnect(t, e, readonlyToken)
	if got := decodeMCPField[model.WhiteboardPage](t, mcpCall(t, e, readonlySession, "track_get_whiteboard_page", args(map[string]any{"page": page.Ref})), "page"); got.ID != page.ID {
		t.Fatalf("readonly get = %+v", got)
	}
	if items := decodeMCPField[[]model.WhiteboardPageSummary](t, mcpCall(t, e, readonlySession, "track_list_whiteboard_pages", args(nil)), "items"); len(items) != 1 {
		t.Fatalf("readonly list = %+v", items)
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"track_create_whiteboard_page", args(map[string]any{"title": "Denied"})},
		{"track_update_whiteboard_page", args(map[string]any{"page": page.Ref, "title": "Denied"})},
		{"track_delete_whiteboard_page", args(map[string]any{"page": page.Ref})},
	} {
		requireMCPErrorCode(t, mcpCallExpectError(t, e, readonlySession, tc.tool, tc.args), "forbidden")
	}

	_, outsiderToken := e.mustUserToken(t, "mcp-whiteboard-outsider")
	outsiderSession := mcpConnect(t, e, outsiderToken)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"track_list_whiteboard_pages", args(nil)},
		{"track_get_whiteboard_page", args(map[string]any{"page": page.Ref})},
		{"track_get_whiteboard_page", args(map[string]any{"page": "whiteboard-999"})},
		{"track_create_whiteboard_page", args(map[string]any{"title": "Denied"})},
		{"track_delete_whiteboard_page", args(map[string]any{"page": page.Ref})},
	} {
		requireMCPErrorCode(t, mcpCallExpectError(t, e, outsiderSession, tc.tool, tc.args), "forbidden")
	}
	if _, err := outsiderSession.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: "track://whiteboard/" + e.ownerUsername + "/" + e.projKey + "/" + page.Ref}); err == nil {
		t.Fatal("outsider ReadResource err = nil, want forbidden")
	}

	e.makeProjectPublic(t)
	if got := decodeMCPField[model.WhiteboardPage](t, mcpCall(t, e, outsiderSession, "track_get_whiteboard_page", args(map[string]any{"page": page.Ref})), "page"); got.ID != page.ID {
		t.Fatalf("public outsider get = %+v", got)
	}
	requireMCPErrorCode(t, mcpCallExpectError(t, e, outsiderSession, "track_update_whiteboard_page", args(map[string]any{"page": page.Ref, "body": "Denied"})), "forbidden")

	stored, err := e.store.GetWhiteboardPageByProjectNumber(e.ctx, e.projectID, page.Number)
	if err != nil || stored.Title != "Shared" || stored.Body != "Body" {
		t.Fatalf("page after denied writes = %+v err=%v", stored, err)
	}
}
