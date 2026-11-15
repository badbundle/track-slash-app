package server_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
)

func mcpFormArgs(e *httpEnv, extra map[string]any) map[string]any {
	out := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// mcpFormSections is formTestSections in the shape the MCP client sends.
func mcpFormSections() []any {
	sections := formTestSections()
	out := make([]any, len(sections))
	for i, section := range sections {
		out[i] = section
	}
	return out
}

func TestMCPFormParity(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	issue := e.mustCreateIssue(t, "Ship 2.3")

	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{
		"track_create_form":      false,
		"track_get_form":         true,
		"track_list_forms":       true,
		"track_list_inbox":       true,
		"track_acknowledge_form": false,
		"track_cancel_form":      false,
	}
	found := 0
	for _, tool := range tools.Tools {
		if readOnly, ok := want[tool.Name]; ok {
			found++
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
				t.Fatalf("%s annotations = %+v, want read-only %v", tool.Name, tool.Annotations, readOnly)
			}
			continue
		}
		// A form is answered by a person in the browser: no tool saves answers,
		// submits or reopens one.
		if strings.Contains(tool.Name, "form") {
			t.Fatalf("unexpected form tool %s: MCP must not be able to answer a form", tool.Name)
		}
	}
	if found != len(want) {
		t.Fatalf("found %d form tools, want %d", found, len(want))
	}

	created := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_create_form", mcpFormArgs(e, map[string]any{
		"title":        "Release decisions",
		"description":  "Blocked on **you**.",
		"issue":        issue.Identifier,
		"origin_label": "Claude Code · release",
		"origin_url":   "https://example.com/session/1",
		"sections":     mcpFormSections(),
	})), "form")
	if created.Ref != "form-1" || created.Status != model.FormStatusOpen || created.AssigneeID != e.adminID || created.Description != "Blocked on **you**." {
		t.Fatalf("created = %+v", created)
	}
	if created.URL != "/"+e.ownerUsername+"/projects/"+e.projKey+"/forms/form-1" || created.Issue == nil || *created.Issue != issue.Identifier || created.OriginLabel != "Claude Code · release" {
		t.Fatalf("created link/issue/origin = %q %v %q", created.URL, created.Issue, created.OriginLabel)
	}
	// The brief form pairs each question with its answer and leaves the definition out.
	if created.Definition != nil || created.Answers != nil || created.QuestionCount != 3 || created.RequiredCount != 2 || created.RequiredAnswered != 1 {
		t.Fatalf("created brief = %+v, want counts and no definition", created)
	}
	if len(created.Responses) != 3 || created.Responses[0].Selected[0].Label != "TestFlight" || created.Responses[0].ChangedFromDefault {
		t.Fatalf("created responses = %+v", created.Responses)
	}

	// The same form reads back over REST.
	code, body := e.do(t, http.MethodGet, e.formPath("form-1"), nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Title != "Release decisions" || got.Definition == nil {
		t.Fatalf("REST get code = %d form = %+v", code, got)
	}

	// The person answers; the agent reads what they chose.
	if code, body := e.do(t, http.MethodPut, e.formPath("form-1")+"/answers", map[string]any{
		"answers": map[string]any{
			"channel": map[string]any{"selected": []string{"store"}, "note": "Testers are done."},
			"rotated": map[string]any{"checked": true},
		},
		"comment": "Ship on Monday.",
	}); code != http.StatusOK {
		t.Fatalf("save answers code = %d body = %s", code, body)
	}
	if code, body := e.do(t, http.MethodPost, e.formPath("form-1")+"/submit", nil); code != http.StatusOK {
		t.Fatalf("submit code = %d body = %s", code, body)
	}
	got := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_get_form", mcpFormArgs(e, map[string]any{"form": "form-1"})), "form")
	if got.Status != model.FormStatusSubmitted || got.Comment != "Ship on Monday." || got.SubmittedVia == nil || *got.SubmittedVia != "api" || got.RequiredAnswered != 2 || got.Definition != nil {
		t.Fatalf("submitted brief = %+v", got)
	}
	channel := got.Responses[0]
	if channel.Selected[0].ID != "store" || channel.Selected[0].Label != "App Store" || channel.Note != "Testers are done." || !channel.ChangedFromDefault {
		t.Fatalf("channel response = %+v, want App Store, changed from the suggestion", channel)
	}
	if rotated := got.Responses[1]; rotated.Checked == nil || !*rotated.Checked || !rotated.Answered {
		t.Fatalf("rotated response = %+v", rotated)
	}

	full := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_get_form", mcpFormArgs(e, map[string]any{"form": "form-1", "include_definition": true})), "form")
	if full.Definition == nil || len(full.Definition.Questions()) != 3 || !full.Answers["rotated"].Checked || len(full.Responses) != 3 {
		t.Fatalf("full form = %+v, want the definition and raw answers too", full)
	}

	resource, err := session.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: "track://form/" + e.ownerUsername + "/" + e.projKey + "/form-1"})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("ReadResource = %+v, %v", resource, err)
	}
	var fromResource formJSON
	if err := json.Unmarshal([]byte(resource.Contents[0].Text), &fromResource); err != nil || fromResource.Ref != "form-1" || fromResource.Definition == nil || len(fromResource.Responses) != 3 {
		t.Fatalf("resource form = %+v, %v", fromResource, err)
	}
	for _, uri := range []string{
		"track://form/" + e.ownerUsername + "/" + e.projKey,
		"track://form/" + e.ownerUsername + "/" + e.projKey + "/form-999",
	} {
		if _, err := session.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("ReadResource %s err = nil, want not found", uri)
		}
	}

	second := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_create_form", mcpFormArgs(e, map[string]any{"title": "Second", "sections": mcpFormSections()})), "form")
	listRefs := func(extra map[string]any) ([]string, *string) {
		t.Helper()
		out := mcpCall(t, e, session, "track_list_forms", mcpFormArgs(e, extra))
		items := decodeMCPField[[]formJSON](t, out, "items")
		refs := make([]string, len(items))
		for i, item := range items {
			refs[i] = item.Ref
			if item.URL == "" || item.Definition != nil {
				t.Fatalf("list row = %+v, want a link and no definition", item)
			}
		}
		return refs, decodeMCPField[*string](t, out, "next_cursor")
	}
	if refs, next := listRefs(nil); !reflect.DeepEqual(refs, []string{"form-2", "form-1"}) || next != nil {
		t.Fatalf("list = %v next = %v", refs, next)
	}
	refs, next := listRefs(map[string]any{"limit": 1})
	if !reflect.DeepEqual(refs, []string{"form-2"}) || next == nil {
		t.Fatalf("page 1 = %v next = %v", refs, next)
	}
	if refs, next := listRefs(map[string]any{"limit": 1, "cursor": *next}); !reflect.DeepEqual(refs, []string{"form-1"}) || next != nil {
		t.Fatalf("page 2 = %v next = %v", refs, next)
	}
	if refs, _ := listRefs(map[string]any{"statuses": []any{"submitted"}}); !reflect.DeepEqual(refs, []string{"form-1"}) {
		t.Fatalf("submitted forms = %v", refs)
	}
	if refs, _ := listRefs(map[string]any{"issue": issue.Identifier}); !reflect.DeepEqual(refs, []string{"form-1"}) {
		t.Fatalf("issue forms = %v", refs)
	}
	if refs, _ := listRefs(map[string]any{"assignee_id": e.adminID.String(), "statuses": []any{"open"}}); !reflect.DeepEqual(refs, []string{"form-2"}) {
		t.Fatalf("admin's open forms = %v", refs)
	}

	picked := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_acknowledge_form", mcpFormArgs(e, map[string]any{"form": "form-1"})), "form")
	if picked.Status != model.FormStatusAcknowledged {
		t.Fatalf("acknowledged = %+v", picked)
	}
	cancelled := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_cancel_form", mcpFormArgs(e, map[string]any{"form": second.Ref})), "form")
	if cancelled.Status != model.FormStatusCancelled {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	// Picked up and cancelled are both final.
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_cancel_form", mcpFormArgs(e, map[string]any{"form": "form-1"})), "conflict")
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_acknowledge_form", mcpFormArgs(e, map[string]any{"form": second.Ref})), "conflict")
}

func TestMCPFormValidationAndPermissions(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	outsider, outsiderToken := e.mustUserToken(t, "mcp-forms-outsider")
	form := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_create_form", mcpFormArgs(e, map[string]any{"title": "Shared", "sections": mcpFormSections()})), "form")

	create := func(extra map[string]any) map[string]any {
		out := mcpFormArgs(e, map[string]any{"title": "Decisions", "sections": mcpFormSections()})
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
		{"create blank title", "track_create_form", create(map[string]any{"title": " "}), "validation_error"},
		{"create no sections", "track_create_form", create(map[string]any{"sections": []any{}}), "validation_error"},
		{"create http origin", "track_create_form", create(map[string]any{"origin_url": "http://example.com"}), "validation_error"},
		{"create malformed issue", "track_create_form", create(map[string]any{"issue": "nope"}), "validation_error"},
		{"create missing issue", "track_create_form", create(map[string]any{"issue": e.projKey + "-999"}), "validation_error"},
		{"create malformed assignee", "track_create_form", create(map[string]any{"assignee_id": "me"}), "validation_error"},
		{"create outsider assignee", "track_create_form", create(map[string]any{"assignee_id": outsider.ID.String()}), "validation_error"},
		{"create unknown project", "track_create_form", map[string]any{"owner": e.ownerUsername, "key": "NOPE", "title": "x", "sections": mcpFormSections()}, "not_found"},
		{"get malformed ref", "track_get_form", mcpFormArgs(e, map[string]any{"form": "whiteboard-1"}), "validation_error"},
		{"get unknown ref", "track_get_form", mcpFormArgs(e, map[string]any{"form": "form-999"}), "not_found"},
		{"get unknown project", "track_get_form", map[string]any{"owner": e.ownerUsername, "key": "NOPE", "form": form.Ref}, "not_found"},
		{"list bad limit", "track_list_forms", mcpFormArgs(e, map[string]any{"limit": -1}), "validation_error"},
		{"list bad cursor", "track_list_forms", mcpFormArgs(e, map[string]any{"cursor": "not-a-cursor!"}), "validation_error"},
		{"list bad status", "track_list_forms", mcpFormArgs(e, map[string]any{"statuses": []any{"done"}}), "validation_error"},
		{"list bad assignee", "track_list_forms", mcpFormArgs(e, map[string]any{"assignee_id": "me"}), "validation_error"},
		{"list bad issue", "track_list_forms", mcpFormArgs(e, map[string]any{"issue": "nope"}), "validation_error"},
		{"list unknown project", "track_list_forms", map[string]any{"owner": e.ownerUsername, "key": "NOPE"}, "not_found"},
		{"acknowledge open form", "track_acknowledge_form", mcpFormArgs(e, map[string]any{"form": form.Ref}), "conflict"},
		{"acknowledge unknown ref", "track_acknowledge_form", mcpFormArgs(e, map[string]any{"form": "form-999"}), "not_found"},
		{"cancel malformed ref", "track_cancel_form", mcpFormArgs(e, map[string]any{"form": "nope"}), "validation_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireMCPErrorCode(t, mcpCallExpectError(t, e, session, tc.tool, tc.args), tc.code)
		})
	}

	// A read-only member reads forms but can't create, pick up or cancel one.
	readonly, readonlyToken := e.mustUserToken(t, "mcp-forms-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	readonlySession := mcpConnect(t, e, readonlyToken)
	if got := decodeMCPField[formJSON](t, mcpCall(t, e, readonlySession, "track_get_form", mcpFormArgs(e, map[string]any{"form": form.Ref})), "form"); got.Ref != form.Ref {
		t.Fatalf("readonly get = %+v", got)
	}
	if items := decodeMCPField[[]formJSON](t, mcpCall(t, e, readonlySession, "track_list_forms", mcpFormArgs(e, nil)), "items"); len(items) != 1 {
		t.Fatalf("readonly list = %+v", items)
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"track_create_form", create(nil)},
		{"track_acknowledge_form", mcpFormArgs(e, map[string]any{"form": form.Ref})},
		{"track_cancel_form", mcpFormArgs(e, map[string]any{"form": form.Ref})},
	} {
		requireMCPErrorCode(t, mcpCallExpectError(t, e, readonlySession, tc.tool, tc.args), "forbidden")
	}

	formTools := func(ref string) []struct {
		tool string
		args map[string]any
	} {
		return []struct {
			tool string
			args map[string]any
		}{
			{"track_list_forms", mcpFormArgs(e, nil)},
			{"track_create_form", create(nil)},
			{"track_get_form", mcpFormArgs(e, map[string]any{"form": ref})},
			{"track_acknowledge_form", mcpFormArgs(e, map[string]any{"form": ref})},
			{"track_cancel_form", mcpFormArgs(e, map[string]any{"form": ref})},
		}
	}
	formURI := "track://form/" + e.ownerUsername + "/" + e.projKey + "/" + form.Ref

	// An outsider of a private project is refused as by any of its tools.
	outsiderSession := mcpConnect(t, e, outsiderToken)
	for _, ref := range []string{form.Ref, "form-999"} {
		for _, tc := range formTools(ref) {
			requireMCPErrorCode(t, mcpCallExpectError(t, e, outsiderSession, tc.tool, tc.args), "forbidden")
		}
	}
	if _, err := outsiderSession.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: formURI}); err == nil {
		t.Fatal("outsider ReadResource err = nil, want forbidden")
	}

	// Someone who reads a public project without being a member is told
	// nothing is there, for a form that exists and one that doesn't.
	e.makeProjectPublic(t)
	if got := decodeMCPField[model.Project](t, mcpCall(t, e, outsiderSession, "track_get_project", mcpFormArgs(e, nil)), "project"); got.ID != e.projectID {
		t.Fatalf("public viewer reads the project = %+v", got)
	}
	for _, ref := range []string{form.Ref, "form-999"} {
		for _, tc := range formTools(ref) {
			requireMCPErrorCode(t, mcpCallExpectError(t, e, outsiderSession, tc.tool, tc.args), "not_found")
		}
	}
	if _, err := outsiderSession.ReadResource(e.ctx, &mcp.ReadResourceParams{URI: formURI}); err == nil {
		t.Fatal("public viewer ReadResource err = nil, want not found")
	}

	// The form is untouched by everything refused above.
	if got := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_get_form", mcpFormArgs(e, map[string]any{"form": form.Ref})), "form"); got.Status != model.FormStatusOpen {
		t.Fatalf("form after refusals = %+v", got)
	}
}
