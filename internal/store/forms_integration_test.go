package store_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func formsTestDefinition() model.FormDefinition {
	optional := false
	return model.FormDefinition{Sections: []model.FormSection{{
		Questions: []model.FormQuestion{
			{
				ID:    "channel",
				Type:  model.FormQuestionSingleChoice,
				Label: "Which channel?",
				Options: []model.FormOption{
					{ID: "testflight", Label: "TestFlight"},
					{ID: "store", Label: "App Store"},
				},
				AllowOther: true,
				Default:    &model.FormValue{Selected: []string{"testflight"}},
			},
			{ID: "rotated", Type: model.FormQuestionConfirm, Label: "I rotated the key"},
			{ID: "notes", Type: model.FormQuestionText, Label: "Release notes", Required: &optional},
		},
	}}}
}

func formsEnvOwner(t *testing.T, env *sprintsTestEnv) uuid.UUID {
	t.Helper()
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	return project.OwnerID
}

func mustCreateForm(t *testing.T, env *sprintsTestEnv, assigneeID uuid.UUID, title string) model.Form {
	t.Helper()
	form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
		ProjectID:   env.projectID,
		Title:       title,
		Definition:  formsTestDefinition(),
		AssigneeID:  assigneeID,
		CreatedByID: assigneeID,
	})
	if err != nil {
		t.Fatalf("CreateForm %q: %v", title, err)
	}
	return form
}

func mustTickRotated(t *testing.T, env *sprintsTestEnv, form model.Form) model.Form {
	t.Helper()
	saved, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{
		FormID:     form.ID,
		AssigneeID: form.AssigneeID,
		Answers:    model.FormAnswers{"rotated": {FormValue: model.FormValue{Checked: true}}},
	})
	if err != nil {
		t.Fatalf("SaveFormAnswers: %v", err)
	}
	return saved
}

func mustSubmitForm(t *testing.T, env *sprintsTestEnv, form model.Form) model.Form {
	t.Helper()
	mustTickRotated(t, env, form)
	submitted, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: form.AssigneeID, Via: model.FormSubmitWeb})
	if err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}
	return submitted
}

func TestCreateFormStoresDetailsDefinitionAndStartingAnswers(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	issue := mustCreateIssue(t, env, "Ship 2.3")

	form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
		ProjectID:   env.projectID,
		IssueID:     &issue.ID,
		Title:       "  Release decisions  ",
		Description: " Blocked on **you**. ",
		Definition:  formsTestDefinition(),
		AssigneeID:  owner,
		CreatedByID: owner,
		OriginLabel: " Claude Code · release ",
		OriginURL:   "https://example.com/session/1",
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if form.Ref != "form-1" || form.Number != 1 || form.ProjectID != env.projectID || form.ProjectKey != project.Key || form.OwnerUsername != project.OwnerUsername {
		t.Fatalf("form identity = %+v", form)
	}
	if form.Title != "Release decisions" || form.Description != "Blocked on **you**." || form.OriginLabel != "Claude Code · release" || form.OriginURL != "https://example.com/session/1" {
		t.Fatalf("form details = %+v, want trimmed", form)
	}
	if form.Status != model.FormStatusOpen || form.AssigneeID != owner || form.CreatedByID != owner || form.Comment != "" {
		t.Fatalf("new form = %+v, want open and assigned to its creator", form)
	}
	if form.SubmittedAt != nil || form.SubmittedVia != nil || form.AcknowledgedAt != nil || form.CancelledAt != nil {
		t.Fatalf("a new form has no submit, pick-up or cancel time: %+v", form)
	}
	if form.IssueID == nil || *form.IssueID != issue.ID || form.IssueIdentifier == nil || *form.IssueIdentifier != issue.Identifier {
		t.Fatalf("form issue = %v %v, want %s", form.IssueID, form.IssueIdentifier, issue.Identifier)
	}
	if got := len(form.Definition.Questions()); got != 3 {
		t.Fatalf("definition questions = %d, want 3", got)
	}
	// The form starts with each question's suggested answer already in place.
	wantAnswers := model.FormAnswers{"channel": {FormValue: model.FormValue{Selected: []string{"testflight"}}}}
	if !reflect.DeepEqual(form.Answers, wantAnswers) {
		t.Fatalf("starting answers = %+v, want %+v", form.Answers, wantAnswers)
	}

	second := mustCreateForm(t, env, owner, "Second")
	if second.Ref != "form-2" || second.IssueID != nil || second.IssueIdentifier != nil || second.OriginLabel != "" || second.OriginURL != "" {
		t.Fatalf("second form = %+v, want form-2 with no issue or origin", second)
	}

	got, err := env.store.GetForm(env.ctx, form.ID)
	if err != nil || got.ID != form.ID || !reflect.DeepEqual(got.Definition, form.Definition) {
		t.Fatalf("GetForm = %+v, %v", got, err)
	}
	byNumber, err := env.store.GetFormByProjectNumber(env.ctx, env.projectID, 2)
	if err != nil || byNumber.ID != second.ID {
		t.Fatalf("GetFormByProjectNumber(2) = %+v, %v", byNumber, err)
	}
	if _, err := env.store.GetForm(env.ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing form err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.GetFormByProjectNumber(env.ctx, env.projectID, 99); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing form number err = %v, want ErrNotFound", err)
	}
}

func TestCreateFormRefusals(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	outsider := mustCreateUser(t, env, "forms-outsider@example.com")

	params := func(mutate func(p *store.CreateFormParams)) store.CreateFormParams {
		p := store.CreateFormParams{
			ProjectID:   env.projectID,
			Title:       "Decisions",
			Definition:  formsTestDefinition(),
			AssigneeID:  owner,
			CreatedByID: owner,
		}
		mutate(&p)
		return p
	}

	var formErr *model.FormError
	if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.Title = "  " })); !errors.As(err, &formErr) || formErr.Field != "title" {
		t.Fatalf("blank title err = %v, want a FormError on title", err)
	}
	if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.OriginURL = "http://example.com" })); !errors.As(err, &formErr) || formErr.Field != "origin_url" {
		t.Fatalf("http origin err = %v, want a FormError on origin_url", err)
	}
	if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.Definition = model.FormDefinition{} })); !errors.As(err, &formErr) || formErr.Field != "sections" {
		t.Fatalf("empty definition err = %v, want a FormError on sections", err)
	}

	if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.ProjectID = uuid.New() })); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing project err = %v, want ErrNotFound", err)
	}
	// Someone outside the project can't be asked.
	if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.AssigneeID = outsider.ID })); !errors.Is(err, store.ErrFormAssigneeNotMember) || !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("outsider assignee err = %v, want ErrNotFound naming the assignee", err)
	}
	if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.AssigneeID = uuid.New() })); !errors.Is(err, store.ErrFormAssigneeNotMember) {
		t.Fatalf("unknown assignee err = %v, want ErrNotFound", err)
	}
	// A creator who doesn't exist fails the foreign key.
	if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.CreatedByID = uuid.New() })); !errors.Is(err, store.ErrNotFound) || !strings.Contains(err.Error(), "user not found") {
		t.Fatalf("unknown creator err = %v, want ErrNotFound from the foreign key", err)
	}

	// An issue from another project, a deleted issue and a missing issue can't be linked.
	otherProject, err := env.store.CreateProjectForUser(env.ctx, owner, uniqueProjectKey(t), "other", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	otherIssue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: otherProject.ID, Title: "Elsewhere"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	deleted := mustCreateIssue(t, env, "Deleted")
	if err := env.store.DeleteIssue(env.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	missing := uuid.New()
	for name, issueID := range map[string]*uuid.UUID{"other project": &otherIssue.ID, "deleted": &deleted.ID, "missing": &missing} {
		if _, err := env.store.CreateForm(env.ctx, params(func(p *store.CreateFormParams) { p.IssueID = issueID })); !errors.Is(err, store.ErrFormIssueNotInProject) || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s issue err = %v, want ErrNotFound naming the issue", name, err)
		}
	}

	// Nothing refused used up a number.
	form := mustCreateForm(t, env, owner, "First real form")
	if form.Ref != "form-1" {
		t.Fatalf("first form ref = %s, want form-1", form.Ref)
	}
}

func TestCreateFormForMembersOfAnyRole(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	member := mustCreateUser(t, env, "forms-member@example.com")
	readonly := mustCreateUser(t, env, "forms-readonly@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	for _, assignee := range []uuid.UUID{member.ID, readonly.ID} {
		form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
			ProjectID:   env.projectID,
			Title:       "For a member",
			Definition:  formsTestDefinition(),
			AssigneeID:  assignee,
			CreatedByID: owner,
		})
		if err != nil || form.AssigneeID != assignee || form.CreatedByID != owner {
			t.Fatalf("form for member %s = %+v, %v", assignee, form, err)
		}
	}
}

func TestCreateFormAllocatesNumbersUnderConcurrency(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)

	const count = 8
	refs := make([]string, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
				ProjectID:   env.projectID,
				Title:       "Concurrent",
				Definition:  formsTestDefinition(),
				AssigneeID:  owner,
				CreatedByID: owner,
			})
			refs[i], errs[i] = form.Ref, err
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreateForm %d: %v", i, err)
		}
		if seen[refs[i]] {
			t.Fatalf("ref %s was allocated twice: %v", refs[i], refs)
		}
		seen[refs[i]] = true
	}
	for n := 1; n <= count; n++ {
		if !seen[model.FormRef(n)] {
			t.Fatalf("refs = %v, want form-1 to form-%d with no gaps", refs, count)
		}
	}
}

func TestSaveFormAnswersMergesAndValidates(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	other := mustCreateUser(t, env, "forms-other@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, other.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	form := mustCreateForm(t, env, owner, "Answers")

	comment := "  Ship on Monday.  "
	saved, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{
		FormID:     form.ID,
		AssigneeID: owner,
		Answers: model.FormAnswers{
			"rotated": {FormValue: model.FormValue{Checked: true}, Note: " Done at 9am. "},
			"notes":   {FormValue: model.FormValue{Text: "  Faster sync.  "}},
		},
		Comment: &comment,
	})
	if err != nil {
		t.Fatalf("SaveFormAnswers: %v", err)
	}
	want := model.FormAnswers{
		"channel": {FormValue: model.FormValue{Selected: []string{"testflight"}}}, // untouched suggestion
		"rotated": {FormValue: model.FormValue{Checked: true}, Note: "Done at 9am."},
		"notes":   {FormValue: model.FormValue{Text: "Faster sync."}},
	}
	if !reflect.DeepEqual(saved.Answers, want) || saved.Comment != "Ship on Monday." || saved.Status != model.FormStatusOpen {
		t.Fatalf("saved = %+v / %q / %s, want %+v", saved.Answers, saved.Comment, saved.Status, want)
	}
	if !saved.UpdatedAt.After(form.UpdatedAt) && !saved.UpdatedAt.Equal(form.UpdatedAt) {
		t.Fatalf("updated_at went backwards: %s -> %s", form.UpdatedAt, saved.UpdatedAt)
	}

	// A later save changes only the questions it names and leaves the comment alone.
	saved, err = env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{
		FormID:     form.ID,
		AssigneeID: owner,
		Answers: model.FormAnswers{
			"channel": {FormValue: model.FormValue{Selected: []string{"other"}, Other: "A private beta"}},
			"notes":   {}, // cleared
		},
	})
	if err != nil {
		t.Fatalf("SaveFormAnswers (second): %v", err)
	}
	want = model.FormAnswers{
		"channel": {FormValue: model.FormValue{Selected: []string{"other"}, Other: "A private beta"}},
		"rotated": {FormValue: model.FormValue{Checked: true}, Note: "Done at 9am."},
	}
	if !reflect.DeepEqual(saved.Answers, want) || saved.Comment != "Ship on Monday." {
		t.Fatalf("second save = %+v / %q, want %+v", saved.Answers, saved.Comment, want)
	}
	reread, err := env.store.GetForm(env.ctx, form.ID)
	if err != nil || !reflect.DeepEqual(reread.Answers, want) {
		t.Fatalf("reread answers = %+v, %v", reread.Answers, err)
	}

	var formErr *model.FormError
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: form.ID, AssigneeID: owner, Answers: model.FormAnswers{"channel": {FormValue: model.FormValue{Selected: []string{"beta"}}}}}); !errors.As(err, &formErr) || formErr.Field != "channel" {
		t.Fatalf("unknown option err = %v, want a FormError on channel", err)
	}
	long := strings.Repeat("a", model.FormMaxCommentLength+1)
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: form.ID, AssigneeID: owner, Comment: &long}); !errors.As(err, &formErr) || formErr.Field != "comment" {
		t.Fatalf("long comment err = %v, want a FormError on comment", err)
	}
	// Only the assignee answers, and a refused save changes nothing.
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: form.ID, AssigneeID: other.ID, Answers: model.FormAnswers{"rotated": {}}}); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "only the form's assignee") {
		t.Fatalf("other member err = %v, want ErrConflict naming the assignee", err)
	}
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: uuid.New(), AssigneeID: owner}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing form err = %v, want ErrNotFound", err)
	}
	reread, err = env.store.GetForm(env.ctx, form.ID)
	if err != nil || !reflect.DeepEqual(reread.Answers, want) {
		t.Fatalf("answers after refused saves = %+v, %v", reread.Answers, err)
	}
}

func TestFormLifecycle(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	other := mustCreateUser(t, env, "forms-lifecycle-other@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, other.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	form := mustCreateForm(t, env, owner, "Lifecycle")

	// A form with a required question unanswered can't be submitted, and says which.
	var incomplete *model.FormIncompleteError
	if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb}); !errors.As(err, &incomplete) || !reflect.DeepEqual(incomplete.QuestionIDs, []string{"rotated"}) {
		t.Fatalf("incomplete submit err = %v, want FormIncompleteError for rotated", err)
	}
	if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: "mcp"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unknown surface err = %v, want ErrConflict", err)
	}
	// An open form can't be picked up or reopened.
	if _, err := env.store.AcknowledgeForm(env.ctx, form.ID); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "form-1 is open") {
		t.Fatalf("acknowledge open err = %v, want ErrConflict naming the status", err)
	}
	if _, err := env.store.ReopenForm(env.ctx, form.ID, owner); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reopen open err = %v, want ErrConflict", err)
	}

	mustTickRotated(t, env, form)
	if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: other.ID, Via: model.FormSubmitWeb}); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "only the form's assignee") {
		t.Fatalf("submit by another member err = %v, want ErrConflict naming the assignee", err)
	}
	submitted, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitAPI})
	if err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}
	if submitted.Status != model.FormStatusSubmitted || submitted.SubmittedAt == nil || submitted.SubmittedVia == nil || *submitted.SubmittedVia != model.FormSubmitAPI {
		t.Fatalf("submitted = %+v, want submitted through the API with a time", submitted)
	}
	// A submitted form's answers are fixed until it is reopened, and it can't be submitted twice.
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: form.ID, AssigneeID: owner, Answers: model.FormAnswers{"rotated": {}}}); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "answers can't change") {
		t.Fatalf("save to submitted err = %v, want ErrConflict", err)
	}
	if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second submit err = %v, want ErrConflict", err)
	}

	// Reopening is the assignee's, keeps the answers and clears the submit record.
	if _, err := env.store.ReopenForm(env.ctx, form.ID, other.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reopen by another member err = %v, want ErrConflict", err)
	}
	reopened, err := env.store.ReopenForm(env.ctx, form.ID, owner)
	if err != nil {
		t.Fatalf("ReopenForm: %v", err)
	}
	if reopened.Status != model.FormStatusOpen || reopened.SubmittedAt != nil || reopened.SubmittedVia != nil || !reopened.Answers["rotated"].Checked {
		t.Fatalf("reopened = %+v, want open with its answers and no submit record", reopened)
	}

	submitted, err = env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb})
	if err != nil || *submitted.SubmittedVia != model.FormSubmitWeb {
		t.Fatalf("resubmit = %+v, %v", submitted, err)
	}
	picked, err := env.store.AcknowledgeForm(env.ctx, form.ID)
	if err != nil {
		t.Fatalf("AcknowledgeForm: %v", err)
	}
	if picked.Status != model.FormStatusAcknowledged || picked.AcknowledgedAt == nil || picked.SubmittedAt == nil {
		t.Fatalf("picked up = %+v, want acknowledged with both times", picked)
	}
	// Picked up is final.
	if _, err := env.store.ReopenForm(env.ctx, form.ID, owner); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reopen picked-up err = %v, want ErrConflict", err)
	}
	if _, err := env.store.CancelForm(env.ctx, form.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cancel picked-up err = %v, want ErrConflict", err)
	}
	if _, err := env.store.AcknowledgeForm(env.ctx, form.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second acknowledge err = %v, want ErrConflict", err)
	}
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: form.ID, AssigneeID: owner}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("save to picked-up err = %v, want ErrConflict", err)
	}

	for _, missing := range []func() error{
		func() error {
			_, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: uuid.New(), AssigneeID: owner, Via: model.FormSubmitWeb})
			return err
		},
		func() error { _, err := env.store.ReopenForm(env.ctx, uuid.New(), owner); return err },
		func() error { _, err := env.store.AcknowledgeForm(env.ctx, uuid.New()); return err },
		func() error { _, err := env.store.CancelForm(env.ctx, uuid.New()); return err },
	} {
		if err := missing(); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("transition of a missing form err = %v, want ErrNotFound", err)
		}
	}
}

func TestCancelForm(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)

	open := mustCreateForm(t, env, owner, "Cancelled while open")
	cancelled, err := env.store.CancelForm(env.ctx, open.ID)
	if err != nil {
		t.Fatalf("CancelForm (open): %v", err)
	}
	if cancelled.Status != model.FormStatusCancelled || cancelled.CancelledAt == nil || cancelled.SubmittedAt != nil {
		t.Fatalf("cancelled open form = %+v", cancelled)
	}

	// A form cancelled after it was submitted keeps its submit record.
	submitted := mustSubmitForm(t, env, mustCreateForm(t, env, owner, "Cancelled after submit"))
	cancelled, err = env.store.CancelForm(env.ctx, submitted.ID)
	if err != nil {
		t.Fatalf("CancelForm (submitted): %v", err)
	}
	if cancelled.Status != model.FormStatusCancelled || cancelled.CancelledAt == nil || cancelled.SubmittedAt == nil || cancelled.SubmittedVia == nil {
		t.Fatalf("cancelled submitted form = %+v, want its submit time kept", cancelled)
	}

	// Cancelled is final.
	if _, err := env.store.CancelForm(env.ctx, open.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second cancel err = %v, want ErrConflict", err)
	}
	if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: open.ID, AssigneeID: owner, Via: model.FormSubmitWeb}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("submit cancelled err = %v, want ErrConflict", err)
	}
	if _, err := env.store.AcknowledgeForm(env.ctx, open.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("acknowledge cancelled err = %v, want ErrConflict", err)
	}
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: open.ID, AssigneeID: owner}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("save to cancelled err = %v, want ErrConflict", err)
	}
}

func TestSubmitRacingCancelLeavesOneWinner(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)

	for round := 0; round < 5; round++ {
		form := mustTickRotated(t, env, mustCreateForm(t, env, owner, "Race"))
		var submitErr, cancelErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, submitErr = env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb})
		}()
		go func() {
			defer wg.Done()
			_, cancelErr = env.store.CancelForm(env.ctx, form.ID)
		}()
		wg.Wait()

		// Cancelling a submitted form is allowed, so the cancel can only lose by
		// running first: then the submit must be the one refused.
		after, err := env.store.GetForm(env.ctx, form.ID)
		if err != nil {
			t.Fatalf("GetForm: %v", err)
		}
		if cancelErr != nil {
			t.Fatalf("round %d: cancel err = %v, a cancel of an open or submitted form always succeeds", round, cancelErr)
		}
		if after.Status != model.FormStatusCancelled {
			t.Fatalf("round %d: status = %s, want cancelled", round, after.Status)
		}
		if submitErr != nil && !errors.Is(submitErr, store.ErrConflict) {
			t.Fatalf("round %d: submit err = %v, want nil or ErrConflict", round, submitErr)
		}
		if (submitErr == nil) != (after.SubmittedAt != nil) {
			t.Fatalf("round %d: submit err = %v but submitted_at = %v", round, submitErr, after.SubmittedAt)
		}
	}
}

func TestSubmitRacingSubmitLeavesOneWinner(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	form := mustTickRotated(t, env, mustCreateForm(t, env, owner, "Double submit"))

	const count = 4
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb})
		}(i)
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, store.ErrConflict):
			t.Fatalf("racing submit err = %v, want nil or ErrConflict", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d submits won, want exactly 1: %v", won, errs)
	}
}

func TestListProjectForms(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	member := mustCreateUser(t, env, "forms-list-member@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	issue := mustCreateIssue(t, env, "Linked")

	first := mustCreateForm(t, env, owner, "First")
	second, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
		ProjectID: env.projectID, IssueID: &issue.ID, Title: "Second", Definition: formsTestDefinition(),
		AssigneeID: member.ID, CreatedByID: owner, OriginLabel: "Claude Code",
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	third := mustSubmitForm(t, env, mustCreateForm(t, env, owner, "Third"))
	fourth := mustCreateForm(t, env, owner, "Fourth")
	if _, err := env.store.CancelForm(env.ctx, fourth.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}

	list := func(p store.ListProjectFormsParams) ([]string, bool) {
		t.Helper()
		p.ProjectID = env.projectID
		if p.Limit == 0 {
			p.Limit = 50
		}
		forms, more, err := env.store.ListProjectForms(env.ctx, p)
		if err != nil {
			t.Fatalf("ListProjectForms: %v", err)
		}
		refs := make([]string, len(forms))
		for i, form := range forms {
			refs[i] = form.Ref
		}
		return refs, more
	}

	if refs, more := list(store.ListProjectFormsParams{}); !reflect.DeepEqual(refs, []string{"form-4", "form-3", "form-2", "form-1"}) || more {
		t.Fatalf("all forms = %v more=%v, want newest first", refs, more)
	}
	// Keyset paging, newest first.
	refs, more := list(store.ListProjectFormsParams{Limit: 3})
	if !reflect.DeepEqual(refs, []string{"form-4", "form-3", "form-2"}) || !more {
		t.Fatalf("page 1 = %v more=%v", refs, more)
	}
	refs, more = list(store.ListProjectFormsParams{Limit: 3, Cursor: &store.FormsCursor{Number: 2}})
	if !reflect.DeepEqual(refs, []string{"form-1"}) || more {
		t.Fatalf("page 2 = %v more=%v", refs, more)
	}

	if refs, _ := list(store.ListProjectFormsParams{Statuses: []model.FormStatus{model.FormStatusOpen}}); !reflect.DeepEqual(refs, []string{"form-2", "form-1"}) {
		t.Fatalf("open forms = %v", refs)
	}
	if refs, _ := list(store.ListProjectFormsParams{Statuses: []model.FormStatus{model.FormStatusSubmitted, model.FormStatusCancelled}}); !reflect.DeepEqual(refs, []string{"form-4", "form-3"}) {
		t.Fatalf("submitted and cancelled forms = %v", refs)
	}
	if refs, _ := list(store.ListProjectFormsParams{IssueID: &issue.ID}); !reflect.DeepEqual(refs, []string{"form-2"}) {
		t.Fatalf("issue forms = %v", refs)
	}
	if refs, _ := list(store.ListProjectFormsParams{AssigneeID: &member.ID}); !reflect.DeepEqual(refs, []string{"form-2"}) {
		t.Fatalf("member's forms = %v", refs)
	}
	if refs, _ := list(store.ListProjectFormsParams{AssigneeID: &owner, Statuses: []model.FormStatus{model.FormStatusOpen}}); !reflect.DeepEqual(refs, []string{"form-1"}) {
		t.Fatalf("owner's open forms = %v", refs)
	}

	// A row carries the counts a list shows in place of the questions.
	forms, _, err := env.store.ListProjectForms(env.ctx, store.ListProjectFormsParams{ProjectID: env.projectID, Limit: 50})
	if err != nil {
		t.Fatalf("ListProjectForms: %v", err)
	}
	byRef := map[string]model.FormSummary{}
	for _, form := range forms {
		byRef[form.Ref] = form
	}
	if got := byRef[first.Ref]; got.QuestionCount != 3 || got.RequiredCount != 2 || got.RequiredAnswered != 1 || got.Status != model.FormStatusOpen {
		t.Fatalf("form-1 row = %+v, want 3 questions and 1 of 2 required answered", got)
	}
	if got := byRef[second.Ref]; got.IssueIdentifier == nil || *got.IssueIdentifier != issue.Identifier || got.AssigneeID != member.ID || got.CreatedByID != owner || got.OriginLabel != "Claude Code" {
		t.Fatalf("form-2 row = %+v", got)
	}
	if got := byRef[third.Ref]; got.RequiredAnswered != 2 || got.SubmittedAt == nil || got.Status != model.FormStatusSubmitted {
		t.Fatalf("form-3 row = %+v, want submitted with everything required answered", got)
	}
	if got := byRef[fourth.Ref]; got.CancelledAt == nil || got.Status != model.FormStatusCancelled {
		t.Fatalf("form-4 row = %+v, want cancelled", got)
	}

	if _, _, err := env.store.ListProjectForms(env.ctx, store.ListProjectFormsParams{ProjectID: uuid.New(), Limit: 10}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing project err = %v, want ErrNotFound", err)
	}
}

func TestFormOutlivesItsIssueAndNotItsProject(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	issue := mustCreateIssue(t, env, "Linked")
	form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
		ProjectID: env.projectID, IssueID: &issue.ID, Title: "Linked form", Definition: formsTestDefinition(),
		AssigneeID: owner, CreatedByID: owner,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	// While its issue is deleted the form reads as linked to none, and is not
	// listed under that issue. Restoring the issue brings the link back.
	if err := env.store.DeleteIssue(env.ctx, issue.ID); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	got, err := env.store.GetForm(env.ctx, form.ID)
	if err != nil || got.IssueID != nil || got.IssueIdentifier != nil {
		t.Fatalf("form of a deleted issue = %v %v, %v; want no issue", got.IssueID, got.IssueIdentifier, err)
	}
	if forms, _, err := env.store.ListProjectForms(env.ctx, store.ListProjectFormsParams{ProjectID: env.projectID, IssueID: &issue.ID, Limit: 10}); err != nil || len(forms) != 0 {
		t.Fatalf("forms of a deleted issue = %+v, %v; want none", forms, err)
	}
	if _, err := env.store.RestoreIssue(env.ctx, issue.ID); err != nil {
		t.Fatalf("RestoreIssue: %v", err)
	}
	if got, err := env.store.GetForm(env.ctx, form.ID); err != nil || got.IssueID == nil || *got.IssueID != issue.ID {
		t.Fatalf("form of a restored issue = %v, %v; want the link back", got.IssueID, err)
	}

	// An issue removed for good clears the link and keeps the form.
	if _, err := env.pool.Exec(env.ctx, `DELETE FROM issues WHERE id = $1`, issue.ID); err != nil {
		t.Fatalf("hard-delete issue: %v", err)
	}
	got, err = env.store.GetForm(env.ctx, form.ID)
	if err != nil || got.IssueID != nil || got.Title != "Linked form" {
		t.Fatalf("form after its issue is gone = %+v, %v", got, err)
	}

	// A deleted project's forms are not found, by id or by number, and can't change.
	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := env.store.GetForm(env.ctx, form.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("form of a deleted project err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.GetFormByProjectNumber(env.ctx, env.projectID, form.Number); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("form number of a deleted project err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.CancelForm(env.ctx, form.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cancel in a deleted project err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.CreateForm(env.ctx, store.CreateFormParams{ProjectID: env.projectID, Title: "Late", Definition: formsTestDefinition(), AssigneeID: owner, CreatedByID: owner}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("create in a deleted project err = %v, want ErrNotFound", err)
	}
}

func inboxRefs(forms []model.FormSummary) []string {
	out := make([]string, len(forms))
	for i, f := range forms {
		out[i] = f.ProjectKey + "/" + f.Title
	}
	return out
}

func listInbox(t *testing.T, env *sprintsTestEnv, user uuid.UUID, state store.InboxState, limit int, cursor *store.InboxCursor) ([]model.FormSummary, bool) {
	t.Helper()
	forms, more, err := env.store.ListInboxForms(env.ctx, store.ListInboxFormsParams{UserID: user, State: state, Cursor: cursor, Limit: limit})
	if err != nil {
		t.Fatalf("ListInboxForms: %v", err)
	}
	return forms, more
}

func TestListInboxFormsStatesOrderAndPaging(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	me := mustCreateUser(t, env, "inbox-me@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, me.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	other, err := env.store.CreateProjectForUser(env.ctx, me.ID, "INBX", "Mine", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	create := func(projectID uuid.UUID, title string) model.Form {
		t.Helper()
		form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{ProjectID: projectID, Title: title, Definition: formsTestDefinition(), AssigneeID: me.ID, CreatedByID: me.ID})
		if err != nil {
			t.Fatalf("CreateForm %q: %v", title, err)
		}
		return form
	}
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	key := project.Key
	create(env.projectID, "A")
	b := create(other.ID, "B")
	create(env.projectID, "C")
	d := create(other.ID, "D")
	create(env.projectID, "E")
	mustCreateForm(t, env, owner, "Not mine")

	mustSubmitForm(t, env, d)
	if _, err := env.store.CancelForm(env.ctx, b.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	if _, err := env.store.AcknowledgeForm(env.ctx, d.ID); err != nil {
		t.Fatalf("AcknowledgeForm: %v", err)
	}

	// To do: open forms, longest-waiting first, across projects.
	todo, more := listInbox(t, env, me.ID, store.InboxTodo, 10, nil)
	if got := inboxRefs(todo); more || !reflect.DeepEqual(got, []string{key + "/A", key + "/C", key + "/E"}) {
		t.Fatalf("todo = %v more=%v", got, more)
	}
	if n, err := env.store.CountInboxForms(env.ctx, me.ID); err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}

	// Done: everything else, most recently changed first.
	done, more := listInbox(t, env, me.ID, store.InboxDone, 10, nil)
	if got := inboxRefs(done); more || !reflect.DeepEqual(got, []string{"INBX/D", "INBX/B"}) {
		t.Fatalf("done = %v more=%v", got, more)
	}

	// Paging follows the cursor in both states, one form at a time.
	for _, tc := range []struct {
		state store.InboxState
		want  []string
	}{
		{store.InboxTodo, []string{"A", "C", "E"}},
		{store.InboxDone, []string{"D", "B"}},
	} {
		var got []string
		var cursor *store.InboxCursor
		for pages := 0; pages < 10; pages++ {
			page, more := listInbox(t, env, me.ID, tc.state, 1, cursor)
			if len(page) != 1 {
				t.Fatalf("%s page %d has %d forms", tc.state, pages, len(page))
			}
			got = append(got, page[0].Title)
			if !more {
				break
			}
			next := store.NextInboxCursor(tc.state, page[0])
			cursor = &next
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s paged = %v, want %v", tc.state, got, tc.want)
		}
	}

	// Someone else's inbox holds only their own forms.
	if forms, _ := listInbox(t, env, owner, store.InboxTodo, 10, nil); len(forms) != 1 || forms[0].Title != "Not mine" {
		t.Fatalf("owner todo = %v", inboxRefs(forms))
	}
}

func TestInboxDropsFormsOfProjectsTheUserCannotReadAsAMember(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	member := mustCreateUser(t, env, "inbox-member@example.com")
	blocked := mustCreateUser(t, env, "inbox-blocked@example.com")
	gone := mustCreateUser(t, env, "inbox-gone@example.com")
	for _, u := range []model.User{member, blocked, gone} {
		if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, u.ID); err != nil {
			t.Fatalf("GrantProjectAccess: %v", err)
		}
	}
	for _, u := range []model.User{member, blocked, gone} {
		for _, title := range []string{"One", "Two"} {
			f := mustCreateForm(t, env, u.ID, title)
			if title == "Two" {
				mustSubmitForm(t, env, f)
			}
		}
	}
	counts := func(u model.User) (int, int, int) {
		t.Helper()
		todo, _ := listInbox(t, env, u.ID, store.InboxTodo, 10, nil)
		done, _ := listInbox(t, env, u.ID, store.InboxDone, 10, nil)
		n, err := env.store.CountInboxForms(env.ctx, u.ID)
		if err != nil {
			t.Fatalf("CountInboxForms: %v", err)
		}
		return len(todo), len(done), n
	}
	for _, u := range []model.User{member, blocked, gone} {
		if todo, done, n := counts(u); todo != 1 || done != 1 || n != 1 {
			t.Fatalf("%s before = %d/%d/%d, want 1/1/1", u.Email, todo, done, n)
		}
	}

	if _, err := env.store.BlockProjectUser(env.ctx, env.projectID, blocked.ID, owner); err != nil {
		t.Fatalf("BlockProjectUser: %v", err)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, gone.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	for _, u := range []model.User{blocked, gone} {
		if todo, done, n := counts(u); todo != 0 || done != 0 || n != 0 {
			t.Fatalf("%s after = %d/%d/%d, want 0/0/0", u.Email, todo, done, n)
		}
	}
	if todo, done, n := counts(member); todo != 1 || done != 1 || n != 1 {
		t.Fatalf("member after = %d/%d/%d, want 1/1/1", todo, done, n)
	}

	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if todo, done, n := counts(member); todo != 0 || done != 0 || n != 0 {
		t.Fatalf("member after delete = %d/%d/%d, want 0/0/0", todo, done, n)
	}
}
