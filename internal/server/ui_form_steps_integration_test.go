package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// uiFormStepsDefinition is three sections: "The plan" and "Wrap up" from the
// shared test definition, then "Sign off". Out of the box env and rotated are
// answered by their suggestions, so reason (step 2) and approver (step 3) are
// the questions still open.
func uiFormStepsDefinition() model.FormDefinition {
	def := uiFormTestDefinition()
	def.Sections = append(def.Sections, model.FormSection{
		Title:     "Sign off",
		Questions: []model.FormQuestion{{ID: "approver", Type: model.FormQuestionText, Label: "Who approved?"}},
	})
	return def
}

func (e *httpEnv) mustUIStepsForm(t *testing.T, assignee uuid.UUID) model.Form {
	t.Helper()
	return e.mustUIForm(t, assignee, func(p *store.CreateFormParams) { p.Definition = uiFormStepsDefinition() })
}

// questionsOn lists which of the form's questions a page shows.
func questionsOn(body string) string {
	var out []string
	for _, id := range []string{"env", "regions", "rotated", "reason", "details", "approver"} {
		if strings.Contains(body, `data-form-question="`+id+`"`) {
			out = append(out, id)
		}
	}
	return strings.Join(out, ",")
}

func TestUIFormStepsEachStepShowsOneSection(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-step-sections")
	form := e.mustUIStepsForm(t, member.ID)
	path := e.uiFormPath(form)

	for step, want := range map[string]string{"1": "env,regions", "2": "rotated,reason,details", "3": "approver"} {
		res, body := e.uiFormGetDoc(t, path+"?step="+step, token)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("step %s code = %d", step, res.StatusCode)
		}
		if got := questionsOn(body); got != want {
			t.Fatalf("step %s shows %q, want %q", step, got, want)
		}
		requireContains(t, "step "+step, body, `<nav aria-label="Form steps"`, "Step "+step+" of 4", "data-form-step-heading", `tabindex="-1"`,
			`<input type="hidden" name="step" value="`+step+`">`)
		requireOmits(t, "step "+step, body, "Anything else?", ">Done<")
		if got := strings.Count(body, "data-form-step-heading"); got != 1 {
			t.Fatalf("step %s headings = %d", step, got)
		}
	}
	_, body := e.uiFormGetDoc(t, path+"?step=2", token)
	requireContains(t, "step 2", body, ">Wrap up<")

	// Markers: states and links. Step 2 is current; env is suggested so step 1
	// is complete; step 3 and Review are not finished.
	for key, state := range map[string]string{"1": "complete", "2": "current", "3": "incomplete", "review": "incomplete"} {
		requireContains(t, "marker "+key, body, `data-form-step="`+key+`" data-form-step-state="`+state+`"`)
	}
	requireContains(t, "markers", body, `aria-current="step"`, `hx-get="`+path+`/panel?step=3"`, `hx-push-url="`+path+`?step=3"`,
		`hx-target="#main"`, `data-lucide="list-checks"`, "Step 1, complete", "Step 3, not finished", "Review, not finished", "Step 2, current step", "The plan", "Sign off")
	if got := strings.Count(body, `aria-current="step"`); got != 1 {
		t.Fatalf("current markers = %d", got)
	}

	// Back and Next in the footer.
	requireContains(t, "step 2 footer", body, `data-form-back`, `href="`+path+`?step=1"`, `data-form-next`, `href="`+path+`?step=3"`)
	_, body = e.uiFormGetDoc(t, path+"?step=1", token)
	requireOmits(t, "step 1 footer", body, "data-form-back")
	requireContains(t, "step 1 footer", body, `data-form-next`, `href="`+path+`?step=2"`)
	_, body = e.uiFormGetDoc(t, path+"?step=3", token)
	requireContains(t, "step 3 footer", body, `data-form-back`, `href="`+path+`?step=2"`, `href="`+path+`?step=review"`)
	requireOmits(t, "step 3 footer", body, ">Done<")

	// The panel twin takes the same parameter.
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, path+"/panel?step=3", token, nil, map[string]string{"HX-Request": "true"})
	body = readBody(t, res)
	res.Body.Close()
	if questionsOn(body) != "approver" || strings.Contains(body, "<html") {
		t.Fatalf("panel step 3 = %s", body)
	}
}

func TestUIFormStepsLandingAndBadValues(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-step-landing")
	form := e.mustUIStepsForm(t, member.ID)
	path := e.uiFormPath(form)

	landing := func(want string) {
		t.Helper()
		_, body := e.uiFormGetDoc(t, path, token)
		requireContains(t, "landing", body, want)
	}
	// Fresh: the suggestions answer section 1, so land on section 2.
	landing("Step 2 of 4")
	e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}}, false)
	landing("Step 3 of 4")
	e.uiFormPost(t, path+"/answers/approver", token, url.Values{"text": {"Sam"}}, false)
	landing("Step 4 of 4")

	for _, bad := range []string{"0", "99", "abc", "-2", "4"} {
		_, body := e.uiFormGetDoc(t, path+"?step="+bad, token)
		requireContains(t, "bad step "+bad, body, "Step 1 of 4")
		if got := questionsOn(body); got != "env,regions" {
			t.Fatalf("bad step %q shows %q", bad, got)
		}
	}
	// A half-answered first section still lands on step 1.
	other := e.mustUIStepsForm(t, member.ID)
	e.uiFormPost(t, e.uiFormPath(other)+"/answers/env", token, url.Values{}, false)
	_, body := e.uiFormGetDoc(t, e.uiFormPath(other), token)
	requireContains(t, "half-answered", body, "Step 1 of 4")
}

func TestUIFormStepsReviewAndSubmit(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-step-review")
	form := e.mustUIStepsForm(t, member.ID)
	path := e.uiFormPath(form)

	_, body := e.uiFormGetDoc(t, path+"?step=review", token)
	requireContains(t, "review", body, "data-form-step-heading", ">Review<", "Step 4 of 4",
		"2 questions still need an answer.", "data-form-notice",
		"The plan", "Wrap up", "Sign off", "Which environment?", "Staging", "Keys rotated?", "Why now?", "Who approved?",
		"Anything else?", "Needs an answer", `data-form-step="review" data-form-step-state="current"`,
		`data-form-edit`, `href="`+path+`?step=1"`, `href="`+path+`?step=3"`, `<input type="hidden" name="step" value="review">`)
	if got := strings.Count(body, "data-form-missing"); got != 2 {
		t.Fatalf("missing marks = %d, want 2", got)
	}
	if got := strings.Count(body, "data-form-review-section"); got != 3 {
		t.Fatalf("review sections = %d, want 3", got)
	}
	if got := questionsOn(body); got != "" {
		t.Fatalf("review shows questions %q", got)
	}
	requireContains(t, "review footer", body, `data-form-back`, `href="`+path+`?step=3"`, ">Done<")
	requireOmits(t, "review footer", body, "data-form-next")
	if !uiFormDoneDisabled.MatchString(body) {
		t.Fatalf("Done should be disabled while questions are open: %s", body)
	}

	// Posting Done directly re-renders Review with the notice.
	res, body := e.uiFormPost(t, path+"/submit", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("incomplete submit code = %d", res.StatusCode)
	}
	requireContains(t, "incomplete submit", body, "2 questions still need an answer.", "data-form-review-section", "data-form-missing", `data-form-step-state="current"`)
	requireOmits(t, "incomplete submit", body, "<html")

	// One left, then complete.
	e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}}, false)
	_, body = e.uiFormGetDoc(t, path+"?step=review", token)
	requireContains(t, "review", body, "1 question still needs an answer.", "Because")
	e.uiFormPost(t, path+"/answers/approver", token, url.Values{"text": {"Sam"}}, false)
	_, body = e.uiFormGetDoc(t, path+"?step=review", token)
	requireOmits(t, "complete review", body, "data-form-notice", "data-form-missing")
	requireContains(t, "complete review", body, "Sam", `data-form-step="review" data-form-step-state="current"`, `data-form-step="3" data-form-step-state="complete"`)
	if uiFormDoneDisabled.MatchString(body) {
		t.Fatalf("Done disabled on a complete review: %s", body)
	}
	e.uiFormPost(t, path+"/comment", token, url.Values{"comment": {"Ship it"}, "step": {"review"}}, false)

	res, body = e.uiFormPost(t, path+"/submit", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d", res.StatusCode)
	}
	requireContains(t, "submitted", body, `data-form-status="submitted"`, "Submitted ", "Ship it")
	requireOmits(t, "submitted", body, "data-form-stepper", "data-form-footer")
	if got := e.mustReloadForm(t, form).Status; got != model.FormStatusSubmitted {
		t.Fatalf("status = %s", got)
	}
	_, body = e.uiFormGetDoc(t, path+"?step=2", token)
	requireOmits(t, "submitted page", body, "data-form-stepper", "data-form-footer")
}

func TestUIFormStepsAutosaveFooterFollowsTheStep(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-step-save")
	form := e.mustUIStepsForm(t, member.ID)
	path := e.uiFormPath(form)

	res, body := e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}, "step": {"2"}}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("save code = %d", res.StatusCode)
	}
	requireContains(t, "step 2 save", body, `hx-swap-oob="true"`, `data-form-step="2"`, `data-form-back`, `href="`+path+`?step=1"`, `data-form-next`, `href="`+path+`?step=3"`, "Saved")
	requireOmits(t, "step 2 save", body, ">Done<")
	_, body = e.uiFormPost(t, path+"/comment", token, url.Values{"comment": {"hi"}, "step": {"review"}}, false)
	requireContains(t, "review save", body, `data-form-back`, ">Done<")
	requireOmits(t, "review save", body, "data-form-next")
	_, body = e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}, "step": {"1"}}, false)
	requireContains(t, "step 1 save", body, `data-form-next`)
	requireOmits(t, "step 1 save", body, "data-form-back")
	// No step posted: the footer is for the landing step.
	_, body = e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}}, false)
	requireContains(t, "landing save", body, `href="`+path+`?step=`)
}

func TestUIFormStepsOnlyForTheAssigneeAnsweringSeveralSections(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-step-plain")
	form := e.mustUIStepsForm(t, member.ID)
	path := e.uiFormPath(form)

	// Another member sees the answers, with no stepper, even with a step.
	_, body := e.uiFormGetDoc(t, path+"?step=2", e.authToken)
	requireContains(t, "other member", body, "data-form-answers", "Waiting on")
	requireOmits(t, "other member", body, "data-form-stepper", "data-form-footer", "data-form-step-nav")

	// One section: no stepper, Done on the page, no hidden step field.
	single := e.mustUIForm(t, member.ID, nil)
	_, body = e.uiFormGetDoc(t, e.uiFormPath(single)+"?step=2", token)
	requireContains(t, "single section", body, ">Done<", "Anything else?", `data-form-question="reason"`)
	requireOmits(t, "single section", body, "data-form-stepper", "data-form-back", "data-form-next", `name="step"`)
}
