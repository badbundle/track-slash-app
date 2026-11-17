package server_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
)

// formJSON is a form as the API returns it.
type formJSON struct {
	Ref          string                 `json:"ref"`
	URL          string                 `json:"url"`
	WaitURL      string                 `json:"wait_url"`
	Title        string                 `json:"title"`
	Description  string                 `json:"description"`
	Status       model.FormStatus       `json:"status"`
	AssigneeID   uuid.UUID              `json:"assignee_id"`
	CreatedByID  uuid.UUID              `json:"created_by_id"`
	Issue        *string                `json:"issue"`
	OriginLabel  string                 `json:"origin_label"`
	OriginURL    string                 `json:"origin_url"`
	Comment      string                 `json:"comment"`
	SubmittedVia *string                `json:"submitted_via"`
	Definition   *model.FormDefinition  `json:"definition"`
	Answers      model.FormAnswers      `json:"answers"`
	Responses    []model.FormAnswerView `json:"responses"`
	Attachments  []formAttachmentJSON   `json:"attachments"`
	// MediaRemovedAt is when the form's files were deleted.
	MediaRemovedAt *time.Time `json:"media_removed_at"`
	// Counts are only on list rows and the MCP's brief form.
	QuestionCount    int `json:"question_count"`
	RequiredCount    int `json:"required_count"`
	RequiredAnswered int `json:"required_answered"`
}

func (e *httpEnv) formsPath() string {
	return e.projectPath() + "/forms"
}

func (e *httpEnv) formPath(ref string) string {
	return e.formsPath() + "/" + ref
}

// formTestSections is a small valid form as a request body: a choice with a
// suggested answer, a confirm box and an optional text question.
func formTestSections() []map[string]any {
	return []map[string]any{{
		"questions": []map[string]any{
			{
				"id": "channel", "type": "single_choice", "label": "Which channel?",
				"options": []map[string]any{
					{"id": "testflight", "label": "TestFlight", "description": "Internal testers first."},
					{"id": "store", "label": "App Store"},
				},
				"allow_other": true,
				"default":     map[string]any{"selected": []string{"testflight"}},
			},
			{"id": "rotated", "type": "confirm", "label": "I rotated the key"},
			{"id": "notes", "type": "text", "label": "Release notes", "required": false},
		},
	}}
}

func (e *httpEnv) mustCreateFormWithToken(t *testing.T, token string, extra map[string]any) formJSON {
	t.Helper()
	body := map[string]any{"title": "Release decisions", "sections": formTestSections()}
	for k, v := range extra {
		body[k] = v
	}
	code, raw := e.doWithToken(t, token, http.MethodPost, e.formsPath(), body)
	if code != http.StatusCreated {
		t.Fatalf("create form code = %d body = %s", code, raw)
	}
	return decode[formJSON](t, raw)
}

func (e *httpEnv) mustCreateForm(t *testing.T, extra map[string]any) formJSON {
	t.Helper()
	return e.mustCreateFormWithToken(t, e.authToken, extra)
}

func (e *httpEnv) mustSubmitForm(t *testing.T, token, ref string) formJSON {
	t.Helper()
	code, raw := e.doWithToken(t, token, http.MethodPut, e.formPath(ref)+"/answers", map[string]any{"answers": map[string]any{"rotated": map[string]any{"checked": true}}})
	if code != http.StatusOK {
		t.Fatalf("save answers code = %d body = %s", code, raw)
	}
	code, raw = e.doWithToken(t, token, http.MethodPost, e.formPath(ref)+"/submit", nil)
	if code != http.StatusOK {
		t.Fatalf("submit code = %d body = %s", code, raw)
	}
	return decode[formJSON](t, raw)
}

func TestHTTPFormLifecycle(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	issue := e.mustCreateIssue(t, "Ship 2.3")

	created := e.mustCreateForm(t, map[string]any{
		"title":        "  Release decisions  ",
		"description":  "Blocked on **you**.",
		"issue":        strings.ToLower(issue.Identifier),
		"origin_label": "Claude Code · release",
		"origin_url":   "https://example.com/session/1",
	})
	wantURL := "/" + e.ownerUsername + "/projects/" + e.projKey + "/forms/form-1"
	if created.Ref != "form-1" || created.URL != wantURL || created.Title != "Release decisions" || created.Description != "Blocked on **you**." {
		t.Fatalf("created = %+v, want form-1 at %s", created, wantURL)
	}
	if created.Status != model.FormStatusOpen || created.AssigneeID != e.adminID || created.CreatedByID != e.adminID {
		t.Fatalf("created = %+v, want open and assigned to its creator", created)
	}
	if created.Issue == nil || *created.Issue != issue.Identifier || created.OriginLabel != "Claude Code · release" || created.OriginURL != "https://example.com/session/1" {
		t.Fatalf("created issue/origin = %v %q %q", created.Issue, created.OriginLabel, created.OriginURL)
	}
	if created.Definition == nil || len(created.Definition.Questions()) != 3 {
		t.Fatalf("created definition = %+v", created.Definition)
	}
	// The form starts with the suggested answer in place, and says so.
	if len(created.Responses) != 3 || created.Responses[0].ID != "channel" || !created.Responses[0].Answered || created.Responses[0].ChangedFromDefault {
		t.Fatalf("created responses = %+v", created.Responses)
	}
	if got := created.Responses[0].Selected; len(got) != 1 || got[0].Label != "TestFlight" {
		t.Fatalf("suggested answer = %+v, want TestFlight by label", got)
	}

	code, body := e.do(t, http.MethodGet, e.formPath("FORM-1"), nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Ref != "form-1" || !reflect.DeepEqual(got.Answers, created.Answers) {
		t.Fatalf("get code = %d form = %+v", code, got)
	}

	// Submitting with a required question unanswered names it.
	code, body = e.do(t, http.MethodPost, e.formPath("form-1")+"/submit", nil)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "questions still need an answer: rotated") {
		t.Fatalf("incomplete submit code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPut, e.formPath("form-1")+"/answers", map[string]any{
		"answers": map[string]any{
			"channel": map[string]any{"selected": []string{"other"}, "other": "A private beta", "note": "Ask Sam first."},
			"rotated": map[string]any{"checked": true},
		},
		"comment": "  Ship on Monday.  ",
	})
	saved := decode[formJSON](t, body)
	if code != http.StatusOK || saved.Comment != "Ship on Monday." || saved.Status != model.FormStatusOpen {
		t.Fatalf("save code = %d form = %+v", code, saved)
	}
	if got := saved.Responses[0]; got.Other != "A private beta" || got.Note != "Ask Sam first." || !got.ChangedFromDefault || got.Selected[0].Label != "Other" {
		t.Fatalf("saved channel response = %+v", got)
	}

	code, body = e.do(t, http.MethodPost, e.formPath("form-1")+"/submit", nil)
	submitted := decode[formJSON](t, body)
	if code != http.StatusOK || submitted.Status != model.FormStatusSubmitted || submitted.SubmittedVia == nil || *submitted.SubmittedVia != "api" {
		t.Fatalf("submit code = %d form = %+v", code, submitted)
	}
	// A submitted form's answers are fixed, and it can't be submitted twice.
	if code, body := e.do(t, http.MethodPut, e.formPath("form-1")+"/answers", map[string]any{"answers": map[string]any{"rotated": map[string]any{}}}); code != http.StatusConflict {
		t.Fatalf("save to submitted code = %d body = %s", code, body)
	}
	if code, body := e.do(t, http.MethodPost, e.formPath("form-1")+"/submit", nil); code != http.StatusConflict {
		t.Fatalf("second submit code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPost, e.formPath("form-1")+"/reopen", nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Status != model.FormStatusOpen || got.SubmittedVia != nil || !got.Answers["rotated"].Checked {
		t.Fatalf("reopen code = %d form = %+v", code, got)
	}
	if code, body := e.do(t, http.MethodPost, e.formPath("form-1")+"/reopen", nil); code != http.StatusConflict {
		t.Fatalf("reopen open code = %d body = %s", code, body)
	}
	if code, body := e.do(t, http.MethodPost, e.formPath("form-1")+"/acknowledge", nil); code != http.StatusConflict {
		t.Fatalf("acknowledge open code = %d body = %s", code, body)
	}

	e.mustSubmitForm(t, e.authToken, "form-1")
	code, body = e.do(t, http.MethodPost, e.formPath("form-1")+"/acknowledge", nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Status != model.FormStatusAcknowledged {
		t.Fatalf("acknowledge code = %d form = %+v", code, got)
	}
	// Picked up is final.
	for _, action := range []string{"cancel", "reopen", "acknowledge", "submit"} {
		if code, body := e.do(t, http.MethodPost, e.formPath("form-1")+"/"+action, nil); code != http.StatusConflict {
			t.Fatalf("%s picked-up form code = %d body = %s", action, code, body)
		}
	}
}

func TestHTTPFormCancelAndAssignee(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, memberToken := e.mustProjectMemberToken(t, "forms-member")

	// A form can be addressed to another member, who then answers it.
	form := e.mustCreateForm(t, map[string]any{"assignee_id": member.ID})
	if form.AssigneeID != member.ID || form.CreatedByID != e.adminID {
		t.Fatalf("form = %+v, want assigned to the member", form)
	}
	if got := e.mustSubmitForm(t, memberToken, form.Ref); got.Status != model.FormStatusSubmitted {
		t.Fatalf("member submit = %+v", got)
	}
	code, body := e.do(t, http.MethodPost, e.formPath(form.Ref)+"/cancel", nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Status != model.FormStatusCancelled {
		t.Fatalf("cancel submitted code = %d form = %+v", code, got)
	}
	if code, body := e.do(t, http.MethodPost, e.formPath(form.Ref)+"/cancel", nil); code != http.StatusConflict {
		t.Fatalf("second cancel code = %d body = %s", code, body)
	}

	open := e.mustCreateForm(t, nil)
	code, body = e.do(t, http.MethodPost, e.formPath(open.Ref)+"/cancel", nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Status != model.FormStatusCancelled {
		t.Fatalf("cancel open code = %d form = %+v", code, got)
	}
}

func TestHTTPFormValidation(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	outsider, _ := e.mustUserToken(t, "forms-outsider")
	otherProject, err := e.store.CreateProjectForUser(e.ctx, e.adminID, uniqueProjectKey(t), "other", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	otherIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: otherProject.ID, Title: "Elsewhere"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	form := e.mustCreateForm(t, nil)

	body := func(extra map[string]any) map[string]any {
		out := map[string]any{"title": "Decisions", "sections": formTestSections()}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	oneOption := formTestSections()
	oneOption[0]["questions"].([]map[string]any)[0]["options"] = []map[string]any{{"id": "only", "label": "Only"}}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
		want   string
	}{
		{"create with no body", http.MethodPost, e.formsPath(), nil, "empty body"},
		{"create with an unknown field", http.MethodPost, e.formsPath(), body(map[string]any{"definition": map[string]any{}}), "unknown field"},
		{"create with a blank title", http.MethodPost, e.formsPath(), body(map[string]any{"title": " "}), "title: required"},
		{"create with no sections", http.MethodPost, e.formsPath(), body(map[string]any{"sections": []any{}}), "sections: a form needs at least one section"},
		{"create with one option", http.MethodPost, e.formsPath(), body(map[string]any{"sections": oneOption}), "sections[0].questions[0].options: a choice question needs 2 to 20 options"},
		{"create with an http origin", http.MethodPost, e.formsPath(), body(map[string]any{"origin_url": "http://example.com"}), "origin_url: must be an https link"},
		{"create with a malformed issue ref", http.MethodPost, e.formsPath(), body(map[string]any{"issue": "nope"}), "issue: issue ref must look like KEY-123"},
		{"create with another project's issue", http.MethodPost, e.formsPath(), body(map[string]any{"issue": otherIssue.Identifier}), "issue: not found in this project"},
		{"create with a missing issue", http.MethodPost, e.formsPath(), body(map[string]any{"issue": e.projKey + "-999"}), "issue: not found in this project"},
		{"create for someone outside the project", http.MethodPost, e.formsPath(), body(map[string]any{"assignee_id": outsider.ID}), "assignee_id: must be a member of the project"},
		{"create with a malformed assignee", http.MethodPost, e.formsPath(), body(map[string]any{"assignee_id": "me"}), "invalid UUID"},
		{"list with a bad limit", http.MethodGet, e.formsPath() + "?limit=0", nil, "limit must be a positive integer"},
		{"list with a bad cursor", http.MethodGet, e.formsPath() + "?cursor=not-a-cursor!", nil, "invalid cursor"},
		{"list with a bad status", http.MethodGet, e.formsPath() + "?status=done", nil, "status must be open, submitted, acknowledged or cancelled"},
		{"list with a bad assignee", http.MethodGet, e.formsPath() + "?assignee_id=me", nil, "assignee_id must be a user id"},
		{"list with a bad issue", http.MethodGet, e.formsPath() + "?issue=nope", nil, "issue: issue ref must look like KEY-123"},
		{"list with another project's issue", http.MethodGet, e.formsPath() + "?issue=" + otherIssue.Identifier, nil, "issue: not found in this project"},
		{"get with a malformed ref", http.MethodGet, e.formPath("whiteboard-1"), nil, "form ref must look like form-1"},
		{"save with no body", http.MethodPut, e.formPath(form.Ref) + "/answers", nil, "empty body"},
		{"save an unknown option", http.MethodPut, e.formPath(form.Ref) + "/answers", map[string]any{"answers": map[string]any{"channel": map[string]any{"selected": []string{"beta"}}}}, `channel: unknown option \"beta\"`},
		{"save an unknown question", http.MethodPut, e.formPath(form.Ref) + "/answers", map[string]any{"answers": map[string]any{"nope": map[string]any{}}}, "nope: the form has no question with this id"},
		{"save a long comment", http.MethodPut, e.formPath(form.Ref) + "/answers", map[string]any{"comment": strings.Repeat("a", model.FormMaxCommentLength+1)}, "comment: at most 10000 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := e.do(t, tc.method, tc.path, tc.body)
			if code != http.StatusBadRequest || !strings.Contains(string(raw), tc.want) {
				t.Fatalf("code = %d body = %s, want 400 with %q", code, raw, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, e.formPath("form-999"), nil},
		{http.MethodPut, e.formPath("form-999") + "/answers", map[string]any{}},
		{http.MethodPost, e.formPath("form-999") + "/submit", nil},
		{http.MethodPost, e.formPath("form-999") + "/reopen", nil},
		{http.MethodPost, e.formPath("form-999") + "/acknowledge", nil},
		{http.MethodPost, e.formPath("form-999") + "/cancel", nil},
		{http.MethodGet, "/" + e.ownerUsername + "/projects/NOPE/forms", nil},
	} {
		if code, raw := e.do(t, tc.method, tc.path, tc.body); code != http.StatusNotFound {
			t.Fatalf("%s %s code = %d body = %s, want 404", tc.method, tc.path, code, raw)
		}
	}

	// Nothing refused created a form or changed the one that exists.
	code, raw := e.do(t, http.MethodGet, e.formsPath(), nil)
	if page := decodePage[formJSON](t, raw); code != http.StatusOK || len(page.Items) != 1 {
		t.Fatalf("forms after refusals = %+v", page.Items)
	}
}

func TestHTTPFormListFiltersAndPaging(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, memberToken := e.mustProjectMemberToken(t, "forms-list-member")
	issue := e.mustCreateIssue(t, "Linked")

	e.mustCreateForm(t, nil)                                                                                    // form-1: open, admin's
	e.mustCreateForm(t, map[string]any{"assignee_id": member.ID, "issue": issue.Identifier, "title": "Linked"}) // form-2: open, member's
	e.mustSubmitForm(t, e.authToken, e.mustCreateForm(t, nil).Ref)                                              // form-3: submitted
	cancelled := e.mustCreateForm(t, nil)                                                                       // form-4: cancelled
	if code, body := e.do(t, http.MethodPost, e.formPath(cancelled.Ref)+"/cancel", nil); code != http.StatusOK {
		t.Fatalf("cancel code = %d body = %s", code, body)
	}

	list := func(token, query string) pageDecoded[formJSON] {
		t.Helper()
		code, body := e.doWithToken(t, token, http.MethodGet, e.formsPath()+query, nil)
		if code != http.StatusOK {
			t.Fatalf("list %q code = %d body = %s", query, code, body)
		}
		return decodePage[formJSON](t, body)
	}
	refs := func(page pageDecoded[formJSON]) []string {
		out := make([]string, len(page.Items))
		for i, item := range page.Items {
			out[i] = item.Ref
		}
		return out
	}

	all := list(e.authToken, "")
	if got := refs(all); !reflect.DeepEqual(got, []string{"form-4", "form-3", "form-2", "form-1"}) || all.NextCursor != nil {
		t.Fatalf("all forms = %v next = %v, want newest first", got, all.NextCursor)
	}
	// A row carries the form's link and counts, not its questions.
	row := all.Items[2]
	if row.URL != "/"+e.ownerUsername+"/projects/"+e.projKey+"/forms/form-2" || row.Title != "Linked" || row.Issue == nil || *row.Issue != issue.Identifier {
		t.Fatalf("form-2 row = %+v", row)
	}
	if row.QuestionCount != 3 || row.RequiredCount != 2 || row.RequiredAnswered != 1 || row.Definition != nil || row.Answers != nil {
		t.Fatalf("form-2 row = %+v, want counts and no questions or answers", row)
	}

	first := list(e.authToken, "?limit=3")
	if got := refs(first); !reflect.DeepEqual(got, []string{"form-4", "form-3", "form-2"}) || first.NextCursor == nil {
		t.Fatalf("page 1 = %v next = %v", got, first.NextCursor)
	}
	second := list(e.authToken, "?limit=3&cursor="+*first.NextCursor)
	if got := refs(second); !reflect.DeepEqual(got, []string{"form-1"}) || second.NextCursor != nil {
		t.Fatalf("page 2 = %v next = %v", got, second.NextCursor)
	}

	if got := refs(list(e.authToken, "?status=open")); !reflect.DeepEqual(got, []string{"form-2", "form-1"}) {
		t.Fatalf("open forms = %v", got)
	}
	if got := refs(list(e.authToken, "?status=Submitted&status=cancelled")); !reflect.DeepEqual(got, []string{"form-4", "form-3"}) {
		t.Fatalf("submitted and cancelled forms = %v", got)
	}
	if got := refs(list(e.authToken, "?issue="+issue.Identifier)); !reflect.DeepEqual(got, []string{"form-2"}) {
		t.Fatalf("issue forms = %v", got)
	}
	// A member sees every form of the project, and can ask for their own.
	if got := refs(list(memberToken, "?assignee_id="+member.ID.String())); !reflect.DeepEqual(got, []string{"form-2"}) {
		t.Fatalf("member's forms = %v", got)
	}
	if got := refs(list(memberToken, "?assignee_id="+e.adminID.String()+"&status=open")); !reflect.DeepEqual(got, []string{"form-1"}) {
		t.Fatalf("admin's open forms, as the member sees them = %v", got)
	}
}

func TestHTTPFormPermissionsForMembers(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, memberToken := e.mustProjectMemberToken(t, "forms-writer")
	readonly, readonlyToken := e.mustUserToken(t, "forms-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	form := e.mustCreateForm(t, nil) // the admin's to answer

	// Another member reads the form but can't answer it for its assignee.
	if code, body := e.doWithToken(t, memberToken, http.MethodGet, e.formPath(form.Ref), nil); code != http.StatusOK {
		t.Fatalf("member get code = %d body = %s", code, body)
	}
	for _, tc := range []struct {
		method string
		action string
		body   any
	}{
		{http.MethodPut, "/answers", map[string]any{"answers": map[string]any{"rotated": map[string]any{"checked": true}}}},
		{http.MethodPost, "/submit", nil},
		{http.MethodPost, "/reopen", nil},
	} {
		code, body := e.doWithToken(t, memberToken, tc.method, e.formPath(form.Ref)+tc.action, tc.body)
		if code != http.StatusForbidden || !strings.Contains(string(body), "only the form's assignee can answer it") {
			t.Fatalf("member %s code = %d body = %s, want 403", tc.action, code, body)
		}
	}
	if code, body := e.do(t, http.MethodGet, e.formPath(form.Ref), nil); code != http.StatusOK || decode[formJSON](t, body).Answers["rotated"].Checked {
		t.Fatalf("the refused save changed the form: %s", body)
	}

	// A read-only member reads forms but can't create, pick up or cancel one.
	if code, body := e.doWithToken(t, readonlyToken, http.MethodGet, e.formsPath(), nil); code != http.StatusOK || len(decodePage[formJSON](t, body).Items) != 1 {
		t.Fatalf("readonly list code = %d body = %s", code, body)
	}
	if code, body := e.doWithToken(t, readonlyToken, http.MethodGet, e.formPath(form.Ref), nil); code != http.StatusOK {
		t.Fatalf("readonly get code = %d body = %s", code, body)
	}
	if code, body := e.doWithToken(t, readonlyToken, http.MethodPost, e.formsPath(), map[string]any{"title": "Denied", "sections": formTestSections()}); code != http.StatusForbidden {
		t.Fatalf("readonly create code = %d body = %s", code, body)
	}
	e.mustSubmitForm(t, e.authToken, form.Ref)
	for _, action := range []string{"/acknowledge", "/cancel"} {
		if code, body := e.doWithToken(t, readonlyToken, http.MethodPost, e.formPath(form.Ref)+action, nil); code != http.StatusForbidden {
			t.Fatalf("readonly %s code = %d body = %s", action, code, body)
		}
	}
	// A member who can write picks the form up, without being its assignee.
	if code, body := e.doWithToken(t, memberToken, http.MethodPost, e.formPath(form.Ref)+"/acknowledge", nil); code != http.StatusOK {
		t.Fatalf("member acknowledge code = %d body = %s", code, body)
	}

	// A read-only member can be asked, and answering their own form is not a project write.
	theirs := e.mustCreateForm(t, map[string]any{"assignee_id": readonly.ID})
	if got := e.mustSubmitForm(t, readonlyToken, theirs.Ref); got.Status != model.FormStatusSubmitted {
		t.Fatalf("readonly submit = %+v", got)
	}
	if code, body := e.doWithToken(t, readonlyToken, http.MethodPost, e.formPath(theirs.Ref)+"/reopen", nil); code != http.StatusOK {
		t.Fatalf("readonly reopen code = %d body = %s", code, body)
	}
}

// formRoutes is every form route, for checking what someone outside the
// project's membership is told. ref names a form that exists or one that doesn't.
func (e *httpEnv) formRoutes(ref string) []struct {
	method string
	path   string
	body   any
} {
	return []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, e.formsPath(), nil},
		{http.MethodPost, e.formsPath(), map[string]any{"title": "Denied", "sections": formTestSections()}},
		{http.MethodGet, e.formPath(ref), nil},
		{http.MethodPut, e.formPath(ref) + "/answers", map[string]any{"answers": map[string]any{}}},
		{http.MethodPost, e.formPath(ref) + "/submit", nil},
		{http.MethodPost, e.formPath(ref) + "/reopen", nil},
		{http.MethodPost, e.formPath(ref) + "/acknowledge", nil},
		{http.MethodPost, e.formPath(ref) + "/cancel", nil},
	}
}

func TestHTTPFormsAreForMembersOnly(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, outsiderToken := e.mustUserToken(t, "forms-outsider")
	blocked, blockedToken := e.mustUserToken(t, "forms-blocked")
	form := e.mustCreateForm(t, nil)

	// On a private project an outsider is refused as on any of its routes,
	// whether or not the form exists.
	for _, ref := range []string{form.Ref, "form-999"} {
		for _, route := range e.formRoutes(ref) {
			if code, body := e.doWithToken(t, outsiderToken, route.method, route.path, route.body); code != http.StatusForbidden {
				t.Fatalf("outsider %s %s code = %d body = %s, want 403", route.method, route.path, code, body)
			}
			if code, body := e.doUnauth(t, route.method, route.path, route.body); code != http.StatusUnauthorized {
				t.Fatalf("signed-out %s %s code = %d body = %s, want 401", route.method, route.path, code, body)
			}
		}
	}

	// On a public project, someone who can read it without being a member is
	// told nothing is there: the same answer for a form that exists and one
	// that doesn't, on every route.
	e.makeProjectPublic(t)
	if code, body := e.doWithToken(t, outsiderToken, http.MethodGet, e.projectPath(), nil); code != http.StatusOK {
		t.Fatalf("public viewer reads the project: code = %d body = %s", code, body)
	}
	for _, ref := range []string{form.Ref, "form-999"} {
		for _, route := range e.formRoutes(ref) {
			code, body := e.doWithToken(t, outsiderToken, route.method, route.path, route.body)
			if code != http.StatusNotFound || !strings.Contains(string(body), "not found") {
				t.Fatalf("public viewer %s %s code = %d body = %s, want 404", route.method, route.path, code, body)
			}
			if strings.Contains(string(body), "Release decisions") {
				t.Fatalf("public viewer %s %s body names the form: %s", route.method, route.path, body)
			}
		}
	}
	// A signed-out visitor gets no further than a signed-in one.
	for _, route := range e.formRoutes(form.Ref) {
		if code, body := e.doUnauth(t, route.method, route.path, route.body); code != http.StatusNotFound && code != http.StatusUnauthorized {
			t.Fatalf("signed-out %s %s on a public project code = %d body = %s, want 401 or 404", route.method, route.path, code, body)
		}
	}

	// A blocked user is refused everything, public project or not.
	if _, err := e.store.BlockProjectUser(e.ctx, e.projectID, blocked.ID, e.adminID); err != nil {
		t.Fatalf("BlockProjectUser: %v", err)
	}
	for _, route := range e.formRoutes(form.Ref) {
		if code, body := e.doWithToken(t, blockedToken, route.method, route.path, route.body); code != http.StatusForbidden {
			t.Fatalf("blocked %s %s code = %d body = %s, want 403", route.method, route.path, code, body)
		}
	}

	// The form is untouched.
	code, body := e.do(t, http.MethodGet, e.formPath(form.Ref), nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Status != model.FormStatusOpen {
		t.Fatalf("form after refusals = %+v", got)
	}
}

func TestHTTPFormsAreHiddenFromHelpDeskReporters(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	form := e.mustCreateForm(t, nil)
	f := newHelpDeskFixture(t, e)

	// A help-desk reporter has no project-level read, so every form route
	// refuses them as every project route does, form or no form.
	for _, ref := range []string{form.Ref, "form-999"} {
		for _, route := range e.formRoutes(ref) {
			if code, body := e.doWithToken(t, f.aliceToken, route.method, route.path, route.body); code != http.StatusForbidden {
				t.Fatalf("reporter %s %s code = %d body = %s, want 403", route.method, route.path, code, body)
			}
		}
	}
}

func TestConnectorTokenCannotAnswerAForm(t *testing.T) {
	t.Parallel()
	e := newOAuthHTTPEnv(t)
	tokens := e.exchange(t, e.approve(t, nil))
	connector, _ := tokens["access_token"].(string)

	// The connector acts for a member: it creates a form, addressed to them by default.
	form := e.mustCreateFormWithToken(t, connector, nil)
	if form.AssigneeID != e.user.ID {
		t.Fatalf("form assignee = %s, want the connector's user", form.AssigneeID)
	}

	// It can't answer the form it asked, even though it acts for the assignee.
	for _, tc := range []struct {
		method string
		action string
		body   any
	}{
		{http.MethodPut, "/answers", map[string]any{"answers": map[string]any{"rotated": map[string]any{"checked": true}}}},
		{http.MethodPost, "/submit", nil},
		{http.MethodPost, "/reopen", nil},
	} {
		code, body := e.doWithToken(t, connector, tc.method, e.formPath(form.Ref)+tc.action, tc.body)
		if code != http.StatusForbidden || !strings.Contains(string(body), "connected applications cannot answer forms") {
			t.Fatalf("connector %s code = %d body = %s, want 403", tc.action, code, body)
		}
	}
	code, body := e.doWithToken(t, connector, http.MethodGet, e.formPath(form.Ref), nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Answers["rotated"].Checked || got.Status != model.FormStatusOpen {
		t.Fatalf("form after the connector's attempts = %+v", got)
	}

	// The person answers with their own token; then the connector reads the
	// answers and picks the form up.
	if got := e.mustSubmitForm(t, e.sessionToken, form.Ref); got.Status != model.FormStatusSubmitted {
		t.Fatalf("user submit = %+v", got)
	}
	code, body = e.doWithToken(t, connector, http.MethodPost, e.formPath(form.Ref)+"/acknowledge", nil)
	if got := decode[formJSON](t, body); code != http.StatusOK || got.Status != model.FormStatusAcknowledged || !got.Answers["rotated"].Checked {
		t.Fatalf("connector acknowledge code = %d form = %+v", code, got)
	}
}

func TestFormURLUsesThePublicOrigin(t *testing.T) {
	t.Parallel()
	e := newHTTPEnvWithOptions(t, server.Options{PublicOrigin: "https://track.example/"})
	form := e.mustCreateForm(t, nil)
	if want := "https://track.example/" + e.ownerUsername + "/projects/" + e.projKey + "/forms/form-1"; form.URL != want {
		t.Fatalf("form url = %q, want %q", form.URL, want)
	}
}
