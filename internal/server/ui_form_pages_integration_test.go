package server_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func uiFormTestDefinition() model.FormDefinition {
	no := false
	return model.FormDefinition{Sections: []model.FormSection{
		{
			Title:       "The plan",
			Description: "Pick **one** environment.",
			Questions: []model.FormQuestion{
				{
					ID: "env", Type: model.FormQuestionSingleChoice, Label: "Which environment?",
					Help: "Staging is *safe*.",
					Options: []model.FormOption{
						{ID: "staging", Label: "Staging", Description: "Safe to break."},
						{ID: "prod", Label: "Production"},
					},
					AllowOther: true,
					Default:    &model.FormValue{Selected: []string{"staging"}},
				},
				{
					ID: "regions", Type: model.FormQuestionMultiChoice, Label: "Which regions?", Required: &no,
					Options: []model.FormOption{{ID: "eu", Label: "Europe"}, {ID: "us", Label: "US"}, {ID: "ap", Label: "Asia"}},
				},
			},
		},
		{
			Title: "Wrap up",
			Questions: []model.FormQuestion{
				{ID: "rotated", Type: model.FormQuestionConfirm, Label: "Keys rotated?", Default: &model.FormValue{Checked: true}},
				{ID: "reason", Type: model.FormQuestionText, Label: "Why now?", Placeholder: "A short reason"},
				{
					ID: "details", Type: model.FormQuestionText, Label: "Details", Multiline: true, Required: &no,
					Default: &model.FormValue{Text: "See the ticket"},
				},
			},
		},
	}}
}

// uiFormFlatDefinition is uiFormTestDefinition as one section, which the form
// page shows all at once. The multi-section form is covered by the step tests.
func uiFormFlatDefinition() model.FormDefinition {
	def := uiFormTestDefinition()
	flat := def.Sections[0]
	flat.Questions = append(flat.Questions, def.Sections[1].Questions...)
	return model.FormDefinition{Sections: []model.FormSection{flat}}
}

func (e *httpEnv) mustUIForm(t *testing.T, assignee uuid.UUID, mutate func(*store.CreateFormParams)) model.Form {
	t.Helper()
	params := store.CreateFormParams{
		ProjectID:   e.projectID,
		Title:       "Release decisions",
		Description: "Please **decide** before Friday.",
		Definition:  uiFormFlatDefinition(),
		AssigneeID:  assignee,
		CreatedByID: e.adminID,
		OriginLabel: "Release chat",
		OriginURL:   "https://chat.example.com/thread/42",
	}
	if mutate != nil {
		mutate(&params)
	}
	form, err := e.store.CreateForm(e.ctx, params)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	return form
}

func (e *httpEnv) uiFormPath(form model.Form) string {
	return e.projectPath() + "/forms/" + form.Ref
}

// uiFormPost posts a form control or button the way htmx does (or, when plain
// is set, as a browser form post) and returns the response and its body.
func (e *httpEnv) uiFormPost(t *testing.T, path, token string, values url.Values, plain bool) (*http.Response, string) {
	t.Helper()
	headers := map[string]string{}
	if !plain {
		headers["HX-Request"] = "true"
	}
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, path, token, strings.NewReader(values.Encode()), headers)
	defer res.Body.Close()
	return res, readBody(t, res)
}

func (e *httpEnv) uiFormGetDoc(t *testing.T, path, token string) (*http.Response, string) {
	t.Helper()
	res := e.uiDoNoRedirect(t, http.MethodGet, path, token, nil)
	defer res.Body.Close()
	return res, readBody(t, res)
}

func (e *httpEnv) mustReloadForm(t *testing.T, form model.Form) model.Form {
	t.Helper()
	got, err := e.store.GetForm(e.ctx, form.ID)
	if err != nil {
		t.Fatalf("GetForm: %v", err)
	}
	return got
}

func requireContains(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Fatalf("%s missing %q: %s", what, want, body)
		}
	}
}

func requireOmits(t *testing.T, what, body string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(body, bad) {
			t.Fatalf("%s should not contain %q: %s", what, bad, body)
		}
	}
}

var uiFormDoneDisabled = regexp.MustCompile(`<button type="submit" disabled`)

func TestUIFormAssigneePageAndAutosave(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-form-member")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "Rotate keys", ReporterID: &e.adminID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	form := e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) { p.IssueID = &issue.ID })
	path := e.uiFormPath(form)

	res, body := e.uiFormGetDoc(t, path, token)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<html") {
		t.Fatalf("page code = %d body = %s", res.StatusCode, body)
	}
	assertPopulatedCSRFTokens(t, path, body)
	requireContains(t, "form page", body,
		`data-form-status="open"`, "Release decisions", "form-1", ">"+issue.Identifier+"<",
		"Asked ", "Release chat", `href="https://chat.example.com/thread/42"`, `target="_blank" rel="noopener noreferrer"`, "chat.example.com",
		"<strong>decide</strong>",
		"The plan", "<strong>one</strong>",
		`id="q-env"`, `id="q-regions"`, `id="q-rotated"`, `id="q-reason"`, `id="q-details"`,
		"Which environment?", "<em>safe</em>", "Safe to break.", "Other", "Optional",
		`data-form-suggested`, "Suggested: Yes", "Suggested: See the ticket",
		`placeholder="A short reason"`, "Add a note", "Anything else?",
		`hx-trigger="change, input delay:700ms"`, `hx-swap="none"`, `hx-sync="this:replace"`,
		path+"/answers/env", path+"/comment", path+"/submit",
		"2 of 3 answered", `aria-live="polite"`,
	)
	if !uiFormDoneDisabled.MatchString(body) {
		t.Fatalf("Done should be disabled while a required question is open: %s", body)
	}
	requireOmits(t, "form page", body, "<script>", ` style=`, "Needs an answer", "Edit answers")
	// The page's only Suggested badge is on the option the form suggests.
	if got := strings.Count(body, "data-form-suggested"); got != 1 {
		t.Fatalf("Suggested badges = %d, want 1", got)
	}
	// Breadcrumb: Forms is plain text, the form is the current page.
	requireContains(t, "breadcrumb", body, `aria-current="page" class="min-w-0 truncate font-medium text-slate-600 dark:text-slate-300">form-1`, `<span class="min-w-0 truncate">Forms</span>`)

	// A text answer saves and completes the required questions.
	res, body = e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"  Because  "}}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("save text code = %d body = %s", res.StatusCode, body)
	}
	requireContains(t, "save response", body, `id="form-footer"`, `hx-swap-oob="true"`, "3 of 3 answered", "Saved", `id="form-question-error-reason"`)
	if uiFormDoneDisabled.MatchString(body) {
		t.Fatalf("Done still disabled once every required question is answered: %s", body)
	}
	requireOmits(t, "save response", body, "<html")
	if got := e.mustReloadForm(t, form).Answers["reason"].Text; got != "Because" {
		t.Fatalf("saved text = %q", got)
	}
	_, body = e.uiFormGetDoc(t, path, token)
	if uiFormDoneDisabled.MatchString(body) {
		t.Fatalf("page should enable Done: %s", body)
	}
	requireContains(t, "page", body, `value="Because"`)

	// Choice answers keep the Other text only while Other is chosen.
	e.uiFormPost(t, path+"/answers/env", token, url.Values{"selected": {"other"}, "other": {"Blue"}}, false)
	got := e.mustReloadForm(t, form).Answers["env"]
	if len(got.Selected) != 1 || got.Selected[0] != "other" || got.Other != "Blue" {
		t.Fatalf("other answer = %+v", got)
	}
	_, body = e.uiFormGetDoc(t, path, token)
	requireContains(t, "page with other", body, `value="Blue"`)
	e.uiFormPost(t, path+"/answers/env", token, url.Values{"selected": {"prod"}, "other": {"stale"}}, false)
	got = e.mustReloadForm(t, form).Answers["env"]
	if len(got.Selected) != 1 || got.Selected[0] != "prod" || got.Other != "" {
		t.Fatalf("answer after leaving Other = %+v", got)
	}

	// Multi choice, confirm and notes.
	e.uiFormPost(t, path+"/answers/regions", token, url.Values{"selected": {"eu", "us"}, "note": {"EU first"}}, false)
	got = e.mustReloadForm(t, form).Answers["regions"]
	if len(got.Selected) != 2 || got.Note != "EU first" {
		t.Fatalf("regions answer = %+v", got)
	}
	_, body = e.uiFormGetDoc(t, path, token)
	requireContains(t, "page with note", body, "EU first", "<details class=\"mt-3\" open>")
	e.uiFormPost(t, path+"/answers/rotated", token, url.Values{}, false)
	if e.mustReloadForm(t, form).Answers["rotated"].Checked {
		t.Fatal("unticked confirm still checked")
	}
	e.uiFormPost(t, path+"/answers/rotated", token, url.Values{"checked": {"true"}}, false)
	if !e.mustReloadForm(t, form).Answers["rotated"].Checked {
		t.Fatal("ticked confirm not saved")
	}
	e.uiFormPost(t, path+"/answers/details", token, url.Values{"text": {"line one\nline two"}}, false)
	if got := e.mustReloadForm(t, form).Answers["details"].Text; got != "line one\nline two" {
		t.Fatalf("multiline text = %q", got)
	}

	// A refused answer fills its error slot and saves nothing.
	before := e.mustReloadForm(t, form).Answers["env"]
	res, body = e.uiFormPost(t, path+"/answers/env", token, url.Values{"selected": {"bogus"}}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("refused answer code = %d body = %s", res.StatusCode, body)
	}
	if !regexp.MustCompile(`id="form-question-error-env"[^>]*>[^<]+</p>`).MatchString(body) {
		t.Fatalf("refused answer left the error slot empty: %s", body)
	}
	if after := e.mustReloadForm(t, form).Answers["env"]; after.Selected[0] != before.Selected[0] {
		t.Fatalf("refused answer changed the saved one: %+v", after)
	}

	// Unknown question.
	res, _ = e.uiFormPost(t, path+"/answers/nope", token, url.Values{"text": {"x"}}, false)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown question code = %d", res.StatusCode)
	}

	// The closing comment.
	res, body = e.uiFormPost(t, path+"/comment", token, url.Values{"comment": {"  Ship Monday.  "}}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("comment code = %d body = %s", res.StatusCode, body)
	}
	requireContains(t, "comment response", body, `id="form-footer"`, `id="form-comment-error"`)
	if got := e.mustReloadForm(t, form).Comment; got != "Ship Monday." {
		t.Fatalf("saved comment = %q", got)
	}
	_, body = e.uiFormGetDoc(t, path, token)
	requireContains(t, "page with comment", body, "Ship Monday.")
	res, body = e.uiFormPost(t, path+"/comment", token, url.Values{"comment": {strings.Repeat("c", model.FormMaxCommentLength+1)}}, false)
	if res.StatusCode != http.StatusOK || !regexp.MustCompile(`id="form-comment-error"[^>]*>[^<]+</p>`).MatchString(body) {
		t.Fatalf("long comment code = %d body = %s", res.StatusCode, body)
	}
	if got := e.mustReloadForm(t, form).Comment; got != "Ship Monday." {
		t.Fatalf("long comment changed the saved one to %q", got)
	}

	// Plain browser posts go back to the page.
	res, _ = e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Plain"}}, true)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != path {
		t.Fatalf("plain save code = %d location = %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = e.uiFormPost(t, path+"/answers/env", token, url.Values{"selected": {"bogus"}}, true)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain refused save code = %d", res.StatusCode)
	}
}

func TestUIFormSubmitReopenAndCancel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-form-submitter")
	form := e.mustUIForm(t, member.ID, nil)
	path := e.uiFormPath(form)

	// Incomplete: the form comes back with a notice and the cards marked.
	res, body := e.uiFormPost(t, path+"/submit", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("incomplete submit code = %d body = %s", res.StatusCode, body)
	}
	requireContains(t, "incomplete submit", body, "1 question still needs an answer.", "data-form-notice", "data-form-unanswered", "Needs an answer", `data-form-question="reason"`)
	if got := strings.Count(body, "data-form-unanswered"); got != 1 {
		t.Fatalf("marked cards = %d, want 1", got)
	}
	requireOmits(t, "incomplete submit", body, "<html")
	if got := e.mustReloadForm(t, form).Status; got != model.FormStatusOpen {
		t.Fatalf("status after incomplete submit = %s", got)
	}
	// Two unanswered reads in the plural, and a plain post renders the page.
	other := e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) {
		p.Definition.Sections[0].Questions[0].Default = nil
	})
	res, body = e.uiFormPost(t, e.uiFormPath(other)+"/submit", token, url.Values{}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "2 questions still need an answer.") || !strings.Contains(body, "<html") {
		t.Fatalf("plain incomplete submit code = %d body = %s", res.StatusCode, body)
	}

	// Complete.
	e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}}, false)
	e.uiFormPost(t, path+"/answers/details", token, url.Values{"text": {"Different"}, "note": {"changed it"}}, false)
	e.uiFormPost(t, path+"/comment", token, url.Values{"comment": {"Thanks"}}, false)
	res, body = e.uiFormPost(t, path+"/submit", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d body = %s", res.StatusCode, body)
	}
	requireContains(t, "submitted view", body,
		`data-form-status="submitted"`, "Submitted ", "Edit answers", "Copy answers", "Open thread", `data-form-answers`,
		"Staging", "Yes", "Because", "Different", "Note: changed it", "Anything else?", "Thanks", "Changed from suggestion",
		"data-copy-text=", "form-1 · Release decisions", "1. Which environment?: Staging",
	)
	requireOmits(t, "submitted view", body, `name="selected"`)
	submitted := e.mustReloadForm(t, form)
	if submitted.Status != model.FormStatusSubmitted || submitted.SubmittedVia == nil || *submitted.SubmittedVia != model.FormSubmitWeb {
		t.Fatalf("submitted form = %+v", submitted)
	}
	// Submitting again is a conflict: the page shows the form as it now is.
	res, body = e.uiFormPost(t, path+"/submit", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `data-form-status="submitted"`) {
		t.Fatalf("second submit code = %d body = %s", res.StatusCode, body)
	}
	// Answers can't change once submitted: reload.
	res, body = e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Late"}}, false)
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Refresh") != "true" {
		t.Fatalf("late save code = %d refresh = %q body = %s", res.StatusCode, res.Header.Get("HX-Refresh"), body)
	}
	res, _ = e.uiFormPost(t, path+"/comment", token, url.Values{"comment": {"Late"}}, true)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("late plain comment code = %d", res.StatusCode)
	}

	// Reopen keeps the answers.
	res, body = e.uiFormPost(t, path+"/reopen", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reopen code = %d body = %s", res.StatusCode, body)
	}
	requireContains(t, "reopened view", body, `data-form-status="open"`, `name="selected"`, `value="Because"`)
	reopened := e.mustReloadForm(t, form)
	if reopened.Status != model.FormStatusOpen || reopened.Answers["reason"].Text != "Because" {
		t.Fatalf("reopened form = %+v", reopened)
	}
	// Reopening an open form is a conflict and shows it as it is.
	res, body = e.uiFormPost(t, path+"/reopen", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `data-form-status="open"`) {
		t.Fatalf("reopen conflict code = %d body = %s", res.StatusCode, body)
	}

	// Plain posts redirect to the page.
	res, _ = e.uiFormPost(t, path+"/submit", token, url.Values{}, true)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != path {
		t.Fatalf("plain submit code = %d location = %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = e.uiFormPost(t, path+"/reopen", token, url.Values{}, true)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain reopen code = %d", res.StatusCode)
	}

	// The owner can cancel; the form then reads as cancelled with no actions to edit.
	res, body = e.uiFormPost(t, path+"/cancel", e.authToken, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cancel code = %d body = %s", res.StatusCode, body)
	}
	requireContains(t, "cancelled view", body, `data-form-status="cancelled"`, "Cancelled ", "Copy answers")
	requireOmits(t, "cancelled view", body, "Cancel form", "Edit answers", `name="selected"`)
	if got := e.mustReloadForm(t, form).Status; got != model.FormStatusCancelled {
		t.Fatalf("status after cancel = %s", got)
	}
	// Cancelling again is a conflict and shows the form as it is.
	res, body = e.uiFormPost(t, path+"/cancel", e.authToken, url.Values{}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `data-form-status="cancelled"`) {
		t.Fatalf("second cancel code = %d body = %s", res.StatusCode, body)
	}
	res, _ = e.uiFormPost(t, e.uiFormPath(other)+"/cancel", e.authToken, url.Values{}, true)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain cancel code = %d", res.StatusCode)
	}
}

func TestUIFormStatusViews(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-form-status")
	form := e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) {
		p.Definition.Sections[0].Questions[3].Default = &model.FormValue{Text: "Because"}
		p.OriginLabel, p.OriginURL, p.Description = "", "", ""
	})
	path := e.uiFormPath(form)

	// An open form with no origin or description draws neither.
	_, body := e.uiFormGetDoc(t, path, token)
	requireOmits(t, "open page", body, "data-form-origin", "data-form-description", "Open thread")
	if uiFormDoneDisabled.MatchString(body) {
		t.Fatalf("Done disabled although every required question has a suggestion: %s", body)
	}

	if _, err := e.store.SubmitForm(e.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: member.ID, Via: model.FormSubmitAPI}); err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}
	_, body = e.uiFormGetDoc(t, path, token)
	requireContains(t, "submitted page", body, `data-form-status="submitted"`, "Submitted ", "Edit answers", "Copy answers")
	// Everyone but the assignee sees no edit action.
	_, body = e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "submitted page for owner", body, "Copy answers", "Cancel form")
	requireOmits(t, "submitted page for owner", body, "Edit answers")

	if _, err := e.store.AcknowledgeForm(e.ctx, form.ID); err != nil {
		t.Fatalf("AcknowledgeForm: %v", err)
	}
	for _, tok := range []string{token, e.authToken} {
		_, body = e.uiFormGetDoc(t, path, tok)
		requireContains(t, "picked up page", body, `data-form-status="acknowledged"`, "Picked up", "Copy answers", `data-form-answers`)
		requireOmits(t, "picked up page", body, "Edit answers", "Cancel form", `name="selected"`)
	}
	// Reopen and cancel a picked-up form: the page shows it as it is.
	res, body := e.uiFormPost(t, path+"/reopen", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `data-form-status="acknowledged"`) {
		t.Fatalf("reopen picked up code = %d body = %s", res.StatusCode, body)
	}
	res, body = e.uiFormPost(t, path+"/cancel", e.authToken, url.Values{}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `data-form-status="acknowledged"`) {
		t.Fatalf("cancel picked up code = %d body = %s", res.StatusCode, body)
	}
}

func TestUIFormOtherMembersAndRoles(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, memberToken := e.mustProjectMemberToken(t, "ui-form-assignee")
	readonly, readonlyToken := e.mustUserToken(t, "ui-form-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	form := e.mustUIForm(t, member.ID, nil)
	path := e.uiFormPath(form)

	// Another member sees who it is waiting on and the answers so far.
	for _, token := range []string{e.authToken, readonlyToken} {
		res, body := e.uiFormGetDoc(t, path, token)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("other member code = %d body = %s", res.StatusCode, body)
		}
		requireContains(t, "waiting view", body, "Waiting on "+member.Name+".", `data-form-answers`, "Staging", "No answer", "Copy answers")
		requireOmits(t, "waiting view", body, `name="selected"`, `id="form-footer"`, "Done", "Edit answers")
	}
	// Only a writer sees Cancel form.
	_, body := e.uiFormGetDoc(t, path, e.authToken)
	requireContains(t, "owner view", body, "Cancel form", `hx-confirm="Cancel this form? It can't be reopened."`)
	_, body = e.uiFormGetDoc(t, path, readonlyToken)
	requireOmits(t, "read-only view", body, "Cancel form")

	// Nobody but the assignee answers, comments, submits or reopens.
	for _, token := range []string{e.authToken, readonlyToken} {
		for _, p := range []string{"/answers/reason", "/comment", "/submit", "/reopen"} {
			res, _ := e.uiFormPost(t, path+p, token, url.Values{"text": {"x"}, "comment": {"x"}}, false)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("POST %s by non-assignee code = %d", p, res.StatusCode)
			}
		}
	}
	if got := e.mustReloadForm(t, form); got.Status != model.FormStatusOpen || got.Answers["reason"].Text != "" || got.Comment != "" {
		t.Fatalf("form changed by non-assignees: %+v", got)
	}
	// Cancelling needs write access.
	res, _ := e.uiFormPost(t, path+"/cancel", readonlyToken, url.Values{}, false)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only cancel code = %d", res.StatusCode)
	}
	if got := e.mustReloadForm(t, form).Status; got != model.FormStatusOpen {
		t.Fatalf("read-only cancel changed status to %s", got)
	}
	// The assignee, who can write, may cancel too.
	_, body = e.uiFormGetDoc(t, path, memberToken)
	requireContains(t, "assignee view", body, "Cancel form")

	// A read-only member answers a form addressed to them.
	mine := e.mustUIForm(t, readonly.ID, nil)
	mine2 := e.uiFormPath(mine)
	_, body = e.uiFormGetDoc(t, mine2, readonlyToken)
	requireContains(t, "read-only assignee view", body, `name="selected"`, "Done")
	requireOmits(t, "read-only assignee view", body, "Cancel form")
	res, body = e.uiFormPost(t, mine2+"/answers/reason", readonlyToken, url.Values{"text": {"Mine"}}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "3 of 3 answered") {
		t.Fatalf("read-only answer code = %d body = %s", res.StatusCode, body)
	}
	res, _ = e.uiFormPost(t, mine2+"/submit", readonlyToken, url.Values{}, false)
	if res.StatusCode != http.StatusOK || e.mustReloadForm(t, mine).Status != model.FormStatusSubmitted {
		t.Fatalf("read-only submit code = %d", res.StatusCode)
	}
	res, _ = e.uiFormPost(t, mine2+"/cancel", readonlyToken, url.Values{}, false)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only assignee cancel code = %d", res.StatusCode)
	}
	res, _ = e.uiFormPost(t, mine2+"/reopen", readonlyToken, url.Values{}, false)
	if res.StatusCode != http.StatusOK || e.mustReloadForm(t, mine).Status != model.FormStatusOpen {
		t.Fatalf("read-only reopen code = %d", res.StatusCode)
	}
}

func TestUIFormIsNotFoundForAnyoneWhoIsNotAMember(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, _ := e.mustProjectMemberToken(t, "ui-form-hidden")
	form := e.mustUIForm(t, member.ID, nil)
	path := e.uiFormPath(form)
	e.makeProjectPublic(t)
	_, outsiderToken := e.mustUserToken(t, "ui-form-outsider")

	res, missing := e.uiFormGetDoc(t, e.projectPath()+"/forms/form-99", outsiderToken)
	if res.StatusCode != http.StatusNotFound || !strings.Contains(missing, "Page not found") {
		t.Fatalf("missing form code = %d body = %s", res.StatusCode, missing)
	}
	for _, target := range []string{path, path + "/panel"} {
		res, body := e.uiFormGetDoc(t, target, outsiderToken)
		if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "Page not found") {
			t.Fatalf("GET %s code = %d body = %s", target, res.StatusCode, body)
		}
		requireOmits(t, "outsider page", body, "Release decisions", "Staging", "form-1")
	}
	for _, p := range []string{"/answers/reason", "/comment", "/submit", "/reopen", "/cancel"} {
		res, body := e.uiFormPost(t, path+p, outsiderToken, url.Values{"text": {"x"}}, true)
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("POST %s by outsider code = %d", p, res.StatusCode)
		}
		requireOmits(t, "outsider post", body, "Release decisions")
	}
	if got := e.mustReloadForm(t, form); got.Status != model.FormStatusOpen || got.Answers["reason"].Text != "" {
		t.Fatalf("form changed by an outsider: %+v", got)
	}
	// A member gets the same page for a form that isn't there, and for refs
	// that aren't refs at all.
	for _, ref := range []string{"form-99", "form-0", "form-x", "whiteboard-1", "nope"} {
		res, body := e.uiFormGetDoc(t, e.projectPath()+"/forms/"+ref, e.authToken)
		if res.StatusCode != http.StatusNotFound || !strings.Contains(body, "Page not found") {
			t.Fatalf("member GET %s code = %d body = %s", ref, res.StatusCode, body)
		}
	}
	// A form of another project is not found through this one either.
	res, _ = e.uiFormGetDoc(t, "/"+e.ownerUsername+"/projects/NOPE/forms/form-1", e.authToken)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project code = %d", res.StatusCode)
	}
}

func TestUIFormPanelTwinAndConflicts(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-form-panel")
	form := e.mustUIForm(t, member.ID, nil)
	path := e.uiFormPath(form)

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, path+"/panel", token, nil, map[string]string{"HX-Request": "true"})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("panel code = %d body = %s", res.StatusCode, body)
	}
	requireContains(t, "panel", body, "data-form-panel", "Release decisions", "Which environment?")
	requireOmits(t, "panel", body, "<html", "data-mobile-sidebar")
	assertPopulatedCSRFTokens(t, path+"/panel", body)
	// The page itself, fetched by htmx, is the same content without the shell.
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodGet, path, token, nil, map[string]string{"HX-Request": "true"})
	body = readBody(t, res)
	res.Body.Close()
	requireContains(t, "htmx page", body, "data-form-panel")
	requireOmits(t, "htmx page", body, "<html")

	// A form cancelled elsewhere while the page was open reloads it.
	if _, err := e.store.CancelForm(e.ctx, form.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	res2, _ := e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"x"}}, false)
	if res2.Header.Get("HX-Refresh") != "true" {
		t.Fatalf("save conflict missing HX-Refresh: %v", res2.Header)
	}
	if got := res2.StatusCode; got != http.StatusOK {
		t.Fatalf("save conflict code = %d", got)
	}
}

func TestUIFormRefusesAMalformedBody(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-form-body")
	path := e.uiFormPath(e.mustUIForm(t, member.ID, nil))
	for _, p := range []string{"/answers/reason", "/comment"} {
		res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, path+p, token, strings.NewReader("text=%zz"), map[string]string{"HX-Request": "true"})
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("POST %s with a malformed body code = %d", p, res.StatusCode)
		}
	}
}
