package store_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

func formChangelog(t *testing.T, env *sprintsTestEnv, includeMembersOnly bool) []model.ProjectChangelogEntry {
	t.Helper()
	entries, _, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{
		ProjectID: env.projectID, Limit: 100, IncludeMembersOnly: includeMembersOnly,
	})
	if err != nil {
		t.Fatalf("ListProjectChangelog: %v", err)
	}
	var out []model.ProjectChangelogEntry
	for _, entry := range entries {
		if entry.Entity == "form" {
			out = append(out, entry)
		}
	}
	return out
}

func TestFormChangesWriteMembersOnlyChangelogEntries(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	actorCtx := store.WithActor(env.ctx, owner)
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "linked issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	form, err := env.store.CreateForm(actorCtx, store.CreateFormParams{
		ProjectID: env.projectID, IssueID: &issue.ID, Title: "Release choices", Description: "SECRET-DESCRIPTION",
		Definition: formsTestDefinition(), AssigneeID: owner, CreatedByID: owner,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if got := formChangelog(t, env, true); len(got) != 1 || got[0].Op != "insert" || got[0].Summary != "Asked form Release choices" {
		t.Fatalf("after create = %+v", got)
	}
	comment := "SECRET-COMMENT"
	if _, err := env.store.SaveFormAnswers(actorCtx, store.SaveFormAnswersParams{
		FormID: form.ID, AssigneeID: owner, Comment: &comment,
		Answers: model.FormAnswers{
			"rotated": {FormValue: model.FormValue{Checked: true}},
			"notes":   {FormValue: model.FormValue{Text: "SECRET-ANSWER"}},
		},
	}); err != nil {
		t.Fatalf("SaveFormAnswers: %v", err)
	}
	if got := formChangelog(t, env, true); len(got) != 1 {
		t.Fatalf("saving answers wrote an entry: %+v", got)
	}
	steps := []struct {
		summary string
		run     func() error
	}{
		{"Submitted form Release choices", func() error {
			_, err := env.store.SubmitForm(actorCtx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb})
			return err
		}},
		{"Reopened form Release choices", func() error { _, err := env.store.ReopenForm(actorCtx, form.ID, owner); return err }},
		{"Submitted form Release choices", func() error {
			_, err := env.store.SubmitForm(actorCtx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb})
			return err
		}},
		{"Picked up form Release choices", func() error { _, err := env.store.AcknowledgeForm(actorCtx, form.ID); return err }},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.summary, err)
		}
	}
	cancelled := mustCreateForm(t, env, owner, "Dropped form")
	if _, err := env.store.CancelForm(actorCtx, cancelled.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	entries := formChangelog(t, env, true)
	if len(entries) != 7 {
		t.Fatalf("entries = %d, want 7: %+v", len(entries), entries)
	}
	summaries := map[string]int{}
	for _, entry := range entries {
		summaries[entry.Summary]++
		if !entry.MembersOnly || entry.IssueID != nil || entry.ParentIssueID != nil {
			t.Fatalf("entry %+v must be members-only and name no issue", entry)
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		for _, secret := range []string{"SECRET-DESCRIPTION", "SECRET-COMMENT", "SECRET-ANSWER", "Which channel?"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("entry carries %q: %s", secret, raw)
			}
		}
	}
	for _, want := range []string{
		"Asked form Release choices", "Submitted form Release choices", "Reopened form Release choices",
		"Picked up form Release choices", "Asked form Dropped form", "Cancelled form Dropped form",
	} {
		if summaries[want] == 0 {
			t.Fatalf("missing entry %q in %v", want, summaries)
		}
	}
	if entries[0].ActorID == nil || *entries[0].ActorID != owner || entries[0].TargetRef == "" {
		t.Fatalf("entry actor/ref = %+v", entries[0])
	}
	if got := formChangelog(t, env, false); len(got) != 0 {
		t.Fatalf("a non-member's changelog shows form entries: %+v", got)
	}
}

// formPushFixture is a project member with a browser subscribed, and a way to
// ask them a form.
type formPushFixture struct {
	env          *sprintsTestEnv
	assignee     model.User
	subscription model.PushSubscription
}

func newFormPushFixture(t *testing.T) formPushFixture {
	t.Helper()
	env := newSprintsEnv(t)
	assignee := mustPushProjectMember(t, env, "formpush")
	subscription := mustUpsertPushSubscription(t, env, assignee.ID, "https://push.example.test/"+uuid.NewString())
	return formPushFixture{env: env, assignee: assignee, subscription: subscription}
}

func (f formPushFixture) formDeliveries(t *testing.T, formID uuid.UUID) []store.PushNotificationDelivery {
	t.Helper()
	mustMaterializePushEvents(t, f.env)
	var out []store.PushNotificationDelivery
	for _, delivery := range mustClaimPushDeliveries(t, f.env) {
		if delivery.FormID != nil && *delivery.FormID == formID {
			out = append(out, delivery)
		}
	}
	return out
}

func TestNewFormNotifiesItsAssigneeEvenWhenTheyAskedIt(t *testing.T) {
	t.Parallel()
	f := newFormPushFixture(t)
	env := f.env
	second := mustUpsertPushSubscription(t, env, f.assignee.ID, "https://push.example.test/"+uuid.NewString())
	other := mustPushProjectMember(t, env, "formother")
	mustUpsertPushSubscription(t, env, other.ID, "https://push.example.test/"+uuid.NewString())
	form, err := env.store.CreateForm(store.WithActor(env.ctx, f.assignee.ID), store.CreateFormParams{
		ProjectID: env.projectID, Title: "Pick a channel", Description: "SECRET-DESCRIPTION",
		Definition: formsTestDefinition(), AssigneeID: f.assignee.ID, CreatedByID: f.assignee.ID,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	deliveries := f.formDeliveries(t, form.ID)
	if len(deliveries) != 2 {
		t.Fatalf("deliveries = %+v, want one per subscription", deliveries)
	}
	for _, delivery := range deliveries {
		if delivery.UserID != f.assignee.ID || delivery.Category != store.PushNotificationForms || delivery.IssueID != uuid.Nil || delivery.ProjectID != env.projectID {
			t.Fatalf("delivery = %+v", delivery)
		}
	}
	if deliveries[0].SubscriptionID == deliveries[1].SubscriptionID || (deliveries[0].SubscriptionID != f.subscription.ID && deliveries[0].SubscriptionID != second.ID) {
		t.Fatalf("subscriptions = %+v", deliveries)
	}
	payload, ok, err := env.store.PreparePushNotificationDelivery(env.ctx, deliveries[0])
	if err != nil || !ok {
		t.Fatalf("prepare = %+v, %v, %v", payload, ok, err)
	}
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	want := store.PushNotificationPayload{
		Title: form.Ref + " · " + project.Name,
		Body:  "Waiting on your answers — Pick a channel",
		URL:   "/" + project.OwnerUsername + "/projects/" + project.Key + "/forms/" + form.Ref,
		Tag:   strings.ReplaceAll(deliveries[0].ChangelogID.String(), "-", ""),
	}
	if payload != want {
		t.Fatalf("payload = %+v, want %+v", payload, want)
	}
	raw, _ := json.Marshal(payload)
	for _, secret := range []string{"SECRET-DESCRIPTION", "Which channel?", "TestFlight"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("payload carries %q: %s", secret, raw)
		}
	}
}

func TestFormPushIsSkippedWhenNothingIsWaiting(t *testing.T) {
	t.Parallel()
	f := newFormPushFixture(t)
	env := f.env
	owner := formsEnvOwner(t, env)
	ctx := store.WithActor(env.ctx, owner)

	// The preference is off.
	if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, f.assignee.ID, model.PushNotificationPreferences{Mentions: true, Assignments: true}); err != nil {
		t.Fatalf("UpdatePushNotificationPreferences: %v", err)
	}
	off := mustCreateForm(t, env, f.assignee.ID, "Preference off")
	if got := f.formDeliveries(t, off.ID); len(got) != 0 {
		t.Fatalf("deliveries with forms off = %+v", got)
	}
	if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, f.assignee.ID, store.DefaultPushNotificationPreferences()); err != nil {
		t.Fatalf("restore preferences: %v", err)
	}

	// The form is answered or cancelled before the event is read.
	for name, finish := range map[string]func(model.Form){
		"submitted": func(form model.Form) { mustSubmitForm(t, env, form) },
		"cancelled": func(form model.Form) {
			if _, err := env.store.CancelForm(ctx, form.ID); err != nil {
				t.Fatalf("CancelForm: %v", err)
			}
		},
	} {
		form := mustCreateForm(t, env, f.assignee.ID, "Finished "+name)
		finish(form)
		if got := f.formDeliveries(t, form.ID); len(got) != 0 {
			t.Fatalf("deliveries for a %s form = %+v", name, got)
		}
	}

	// The project goes away before the event is read.
	gone := mustCreateForm(t, env, f.assignee.ID, "Project deleted")
	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if got := f.formDeliveries(t, gone.ID); len(got) != 0 {
		t.Fatalf("deliveries for a deleted project = %+v", got)
	}
}

func TestFormPushIsSuppressedWhenSendTimeChecksFail(t *testing.T) {
	t.Parallel()
	prepare := func(t *testing.T, f formPushFixture, d store.PushNotificationDelivery) bool {
		t.Helper()
		_, ok, err := f.env.store.PreparePushNotificationDelivery(f.env.ctx, d)
		if err != nil {
			t.Fatalf("PreparePushNotificationDelivery: %v", err)
		}
		return ok
	}
	owner := func(f formPushFixture) uuid.UUID { return formsEnvOwner(t, f.env) }
	cases := map[string]func(t *testing.T, f formPushFixture, form model.Form){
		"submitted": func(t *testing.T, f formPushFixture, form model.Form) { mustSubmitForm(t, f.env, form) },
		"cancelled": func(t *testing.T, f formPushFixture, form model.Form) {
			if _, err := f.env.store.CancelForm(f.env.ctx, form.ID); err != nil {
				t.Fatal(err)
			}
		},
		"removed from the project": func(t *testing.T, f formPushFixture, form model.Form) {
			if err := f.env.store.RevokeProjectAccess(f.env.ctx, f.env.projectID, f.assignee.ID); err != nil {
				t.Fatal(err)
			}
		},
		"blocked": func(t *testing.T, f formPushFixture, form model.Form) {
			if _, err := f.env.store.BlockProjectUser(f.env.ctx, f.env.projectID, f.assignee.ID, owner(f)); err != nil {
				t.Fatal(err)
			}
		},
		"project deleted": func(t *testing.T, f formPushFixture, form model.Form) {
			if err := f.env.store.DeleteProject(f.env.ctx, f.env.projectID); err != nil {
				t.Fatal(err)
			}
		},
		"subscription disabled": func(t *testing.T, f formPushFixture, form model.Form) {
			if err := f.env.store.DisablePushSubscription(f.env.ctx, f.assignee.ID, f.subscription.Endpoint); err != nil {
				t.Fatal(err)
			}
		},
		"preference turned off": func(t *testing.T, f formPushFixture, form model.Form) {
			if _, err := f.env.store.UpdatePushNotificationPreferences(f.env.ctx, f.assignee.ID, model.PushNotificationPreferences{}); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFormPushFixture(t)
			form := mustCreateForm(t, f.env, f.assignee.ID, "Send time")
			deliveries := f.formDeliveries(t, form.ID)
			if len(deliveries) != 1 || !prepare(t, f, deliveries[0]) {
				t.Fatalf("before the change: deliveries=%+v", deliveries)
			}
			change(t, f, form)
			if prepare(t, f, deliveries[0]) {
				t.Fatalf("a push was prepared after the form was %s", name)
			}
		})
	}
}

func TestFormPushSuppressedWhenAssigneeNoLongerHasTheForm(t *testing.T) {
	t.Parallel()
	f := newFormPushFixture(t)
	env := f.env
	form := mustCreateForm(t, env, f.assignee.ID, "Moved")
	delivery := f.formDeliveries(t, form.ID)[0]
	for name, mutate := range map[string]func(d *store.PushNotificationDelivery){
		"form missing":        func(d *store.PushNotificationDelivery) { id := uuid.New(); d.FormID = &id },
		"another project":     func(d *store.PushNotificationDelivery) { d.ProjectID = uuid.New() },
		"another user":        func(d *store.PushNotificationDelivery) { d.UserID = uuid.New() },
		"form assignee moved": func(d *store.PushNotificationDelivery) { d.UserID = formsEnvOwner(t, env) },
	} {
		changed := delivery
		mutate(&changed)
		if _, ok, err := env.store.PreparePushNotificationDelivery(env.ctx, changed); err != nil || ok {
			t.Fatalf("%s: ok=%v err=%v", name, ok, err)
		}
	}
}

func TestIssueDeliveryHasNoFormID(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	assignee := mustPushProjectMember(t, env, "issuepush")
	mustUpsertPushSubscription(t, env, assignee.ID, "https://push.example.test/"+uuid.NewString())
	owner := formsEnvOwner(t, env)
	issue, err := env.store.CreateIssue(store.WithActor(env.ctx, owner), store.CreateIssueParams{
		ProjectID: env.projectID, Title: "still works", AssigneeID: &assignee.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustMaterializePushEvents(t, env)
	for _, delivery := range mustClaimPushDeliveries(t, env) {
		if delivery.IssueID == issue.ID {
			if delivery.FormID != nil {
				t.Fatalf("issue delivery has a form: %+v", delivery)
			}
			return
		}
	}
	t.Fatal("no delivery for the assigned issue")
}
