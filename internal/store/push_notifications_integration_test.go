package store_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

func TestPushNotificationPreferencesAndBrowserSubscriptions(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	user, err := env.store.CreateUserProfile(env.ctx, "pushprefs"+strings.ToLower(uniqueProjectKey(t)), "push-prefs@example.com", "Push Prefs")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	preferences, err := env.store.GetPushNotificationPreferences(env.ctx, user.ID)
	if err != nil || preferences != store.DefaultPushNotificationPreferences() {
		t.Fatalf("default preferences = %+v, %v", preferences, err)
	}
	want := model.PushNotificationPreferences{Comments: true, StatusChanges: true, DueDateChanges: true}
	if got, err := env.store.UpdatePushNotificationPreferences(env.ctx, user.ID, want); err != nil || got != want {
		t.Fatalf("updated preferences = %+v, %v", got, err)
	}
	if _, err := env.store.GetPushNotificationPreferences(env.ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing preferences err = %v", err)
	}
	if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, uuid.New(), want); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing preference update err = %v", err)
	}

	endpoint := "https://push.example.test/" + uuid.NewString()
	subscription := mustUpsertPushSubscription(t, env, user.ID, endpoint)
	if subscription.UserID != user.ID || subscription.Endpoint != endpoint || subscription.DisabledAt != nil {
		t.Fatalf("subscription = %+v", subscription)
	}
	if active, err := env.store.PushSubscriptionActive(env.ctx, user.ID, endpoint); err != nil || !active {
		t.Fatalf("active subscription = %v, %v", active, err)
	}
	if count, err := env.store.CountActivePushSubscriptions(env.ctx, user.ID); err != nil || count != 1 {
		t.Fatalf("subscription count = %d, %v", count, err)
	}

	other, err := env.store.CreateUserProfile(env.ctx, "pushother"+strings.ToLower(uniqueProjectKey(t)), "push-other@example.com", "Push Other")
	if err != nil {
		t.Fatalf("CreateUserProfile other: %v", err)
	}
	transferred := mustUpsertPushSubscription(t, env, other.ID, endpoint)
	if transferred.ID != subscription.ID || transferred.UserID != other.ID {
		t.Fatalf("transferred subscription = %+v, want id %s user %s", transferred, subscription.ID, other.ID)
	}
	if active, err := env.store.PushSubscriptionActive(env.ctx, user.ID, endpoint); err != nil || active {
		t.Fatalf("old owner active = %v, %v", active, err)
	}
	if err := env.store.DisablePushSubscription(env.ctx, other.ID, endpoint); err != nil {
		t.Fatalf("DisablePushSubscription: %v", err)
	}
	if active, err := env.store.PushSubscriptionActive(env.ctx, other.ID, endpoint); err != nil || active {
		t.Fatalf("disabled active = %v, %v", active, err)
	}
	if err := env.store.DisablePushSubscription(env.ctx, other.ID, endpoint); err != nil {
		t.Fatalf("idempotent DisablePushSubscription: %v", err)
	}
	if err := env.store.DisablePushSubscription(env.ctx, user.ID, "https://push.example.test/missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing disable err = %v", err)
	}
}

func TestPushNotificationAssignmentDefaultsAndSelfSuppression(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	assignee := mustPushProjectMember(t, env, "pushassign")
	assigneeSubscription := mustUpsertPushSubscription(t, env, assignee.ID, "https://push.example.test/"+uuid.NewString())
	mustUpsertPushSubscription(t, env, project.OwnerID, "https://push.example.test/"+uuid.NewString())
	issue, err := env.store.CreateIssue(store.WithActor(env.ctx, project.OwnerID), store.CreateIssueParams{
		ProjectID: env.projectID, Title: "push assignment", AssigneeID: &assignee.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue assigned: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries := mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 || deliveries[0].UserID != assignee.ID || deliveries[0].Category != store.PushNotificationAssignments {
		t.Fatalf("assignment deliveries = %+v", deliveries)
	}
	if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, assignee.ID, model.PushNotificationPreferences{}); err != nil {
		t.Fatalf("disable assignment preference: %v", err)
	}
	if _, authorized, err := env.store.PreparePushNotificationDelivery(env.ctx, deliveries[0]); err != nil || authorized {
		t.Fatalf("disabled assignment preference authorized = %v, err=%v", authorized, err)
	}
	if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, assignee.ID, store.DefaultPushNotificationPreferences()); err != nil {
		t.Fatalf("restore assignment preference: %v", err)
	}
	payload, authorized, err := env.store.PreparePushNotificationDelivery(env.ctx, deliveries[0])
	if err != nil || !authorized || !strings.Contains(payload.Title, issue.Identifier) ||
		!strings.Contains(payload.Body, "assigned this issue to you") || !strings.Contains(payload.URL, issue.Identifier) {
		t.Fatalf("assignment payload = %+v, authorized=%v err=%v", payload, authorized, err)
	}
	if err := env.store.CompletePushNotificationDelivery(env.ctx, deliveries[0]); err != nil {
		t.Fatalf("CompletePushNotificationDelivery: %v", err)
	}

	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{ClearAssignee: true}); err != nil {
		t.Fatalf("clear assignee: %v", err)
	}
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{AssigneeID: &assignee.ID}); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries = mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 {
		t.Fatalf("reassignment deliveries = %+v", deliveries)
	}
	if err := env.store.DisablePushSubscription(env.ctx, assignee.ID, assigneeSubscription.Endpoint); err != nil {
		t.Fatalf("DisablePushSubscription after claim: %v", err)
	}
	if _, authorized, err := env.store.PreparePushNotificationDelivery(env.ctx, deliveries[0]); err != nil || authorized {
		t.Fatalf("disabled browser authorized = %v, err=%v", authorized, err)
	}

	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{ClearAssignee: true}); err != nil {
		t.Fatalf("clear reassigned assignee: %v", err)
	}
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{AssigneeID: &project.OwnerID}); err != nil {
		t.Fatalf("self assign: %v", err)
	}
	mustMaterializePushEvents(t, env)
	if deliveries := mustClaimPushDeliveries(t, env); len(deliveries) != 0 {
		t.Fatalf("self assignment deliveries = %+v", deliveries)
	}
}

func TestPushNotificationMentionsCommentsAndDuplicateCoalescing(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	mentioned := mustPushProjectMember(t, env, "pushmention")
	reporter := mustPushProjectMember(t, env, "pushreport")
	mustUpsertPushSubscription(t, env, mentioned.ID, "https://push.example.test/"+uuid.NewString())
	mustUpsertPushSubscription(t, env, reporter.ID, "https://push.example.test/"+uuid.NewString())
	issue, err := env.store.CreateIssue(store.WithActor(env.ctx, project.OwnerID), store.CreateIssueParams{
		ProjectID: env.projectID, Title: "push comments", Description: "Please review @" + mentioned.Username,
		AssigneeID: &mentioned.ID, ReporterID: &reporter.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries := mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 || deliveries[0].UserID != mentioned.ID || deliveries[0].Category != store.PushNotificationMentions {
		t.Fatalf("create mention deliveries = %+v", deliveries)
	}
	mustCompletePushDeliveries(t, env, deliveries)

	description := "Still waiting for @" + mentioned.Username
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{Description: &description}); err != nil {
		t.Fatalf("UpdateIssue existing mention: %v", err)
	}
	mustMaterializePushEvents(t, env)
	if deliveries := mustClaimPushDeliveries(t, env); len(deliveries) != 0 {
		t.Fatalf("existing mention deliveries = %+v", deliveries)
	}
	description += " and @" + reporter.Username
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{Description: &description}); err != nil {
		t.Fatalf("UpdateIssue new mention: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries = mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 || deliveries[0].UserID != reporter.ID || deliveries[0].Category != store.PushNotificationMentions {
		t.Fatalf("new mention deliveries = %+v", deliveries)
	}
	mustCompletePushDeliveries(t, env, deliveries)

	if _, err := env.store.CreateComment(store.WithActor(env.ctx, project.OwnerID), store.CreateCommentParams{
		IssueID: issue.ID, AuthorID: project.OwnerID, Body: "Please review @" + mentioned.Username,
	}); err != nil {
		t.Fatalf("CreateComment mention: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries = mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 || deliveries[0].UserID != mentioned.ID || deliveries[0].Category != store.PushNotificationMentions {
		t.Fatalf("default mention deliveries = %+v", deliveries)
	}
	mustCompletePushDeliveries(t, env, deliveries)

	if _, err := env.store.CreateComment(store.WithActor(env.ctx, mentioned.ID), store.CreateCommentParams{
		IssueID: issue.ID, AuthorID: mentioned.ID, Body: "A self mention @" + mentioned.Username,
	}); err != nil {
		t.Fatalf("CreateComment self mention: %v", err)
	}
	mustMaterializePushEvents(t, env)
	if deliveries := mustClaimPushDeliveries(t, env); len(deliveries) != 0 {
		t.Fatalf("self mention deliveries = %+v", deliveries)
	}

	commentPreferences := model.PushNotificationPreferences{Mentions: true, Assignments: true, Comments: true}
	for _, userID := range []uuid.UUID{mentioned.ID, reporter.ID} {
		if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, userID, commentPreferences); err != nil {
			t.Fatalf("UpdatePushNotificationPreferences: %v", err)
		}
	}
	if _, err := env.store.CreateComment(store.WithActor(env.ctx, project.OwnerID), store.CreateCommentParams{
		IssueID: issue.ID, AuthorID: project.OwnerID, Body: "Another update for @" + mentioned.Username,
	}); err != nil {
		t.Fatalf("CreateComment coalesced: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries = mustClaimPushDeliveries(t, env)
	if len(deliveries) != 2 {
		t.Fatalf("coalesced deliveries = %+v", deliveries)
	}
	categories := map[uuid.UUID]store.PushNotificationCategory{}
	for _, delivery := range deliveries {
		categories[delivery.UserID] = delivery.Category
	}
	if categories[mentioned.ID] != store.PushNotificationMentions || categories[reporter.ID] != store.PushNotificationComments {
		t.Fatalf("coalesced categories = %+v", categories)
	}
	mustCompletePushDeliveries(t, env, deliveries)
}

func TestPushNotificationChangeCategoriesAndDeliveryAuthorization(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	member := mustPushProjectMember(t, env, "pushchange")
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{
		ProjectID: env.projectID, Title: "push changes", AssigneeID: &member.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustMaterializePushEvents(t, env)
	mustUpsertPushSubscription(t, env, member.ID, "https://push.example.test/"+uuid.NewString())
	if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, member.ID, model.PushNotificationPreferences{
		Mentions: true, Assignments: true, StatusChanges: true, DueDateChanges: true,
	}); err != nil {
		t.Fatalf("UpdatePushNotificationPreferences: %v", err)
	}

	status := model.StatusInProgress
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{Status: &status}); err != nil {
		t.Fatalf("UpdateIssue status: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries := mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 || deliveries[0].Category != store.PushNotificationStatusChanges {
		t.Fatalf("status deliveries = %+v", deliveries)
	}
	mustCompletePushDeliveries(t, env, deliveries)

	dueDate := model.DateFromTime(time.Now().UTC().AddDate(0, 0, 3))
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{DueDate: &dueDate}); err != nil {
		t.Fatalf("UpdateIssue due date: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries = mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 || deliveries[0].Category != store.PushNotificationDueDateChanges {
		t.Fatalf("due date deliveries = %+v", deliveries)
	}
	mustCompletePushDeliveries(t, env, deliveries)

	status = model.StatusTodo
	if _, err := env.store.UpdateIssue(store.WithActor(env.ctx, project.OwnerID), issue.ID, store.UpdateIssueParams{Status: &status}); err != nil {
		t.Fatalf("UpdateIssue second status: %v", err)
	}
	mustMaterializePushEvents(t, env)
	deliveries = mustClaimPushDeliveries(t, env)
	if len(deliveries) != 1 {
		t.Fatalf("authorization deliveries = %+v", deliveries)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, member.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	if _, authorized, err := env.store.PreparePushNotificationDelivery(env.ctx, deliveries[0]); err != nil || authorized {
		t.Fatalf("revoked delivery authorized = %v, err=%v", authorized, err)
	}
	if err := env.store.SuppressPushNotificationDelivery(env.ctx, deliveries[0], "revoked in test"); err != nil {
		t.Fatalf("SuppressPushNotificationDelivery: %v", err)
	}
}

// A browser that is signed out must stop showing the account's notifications,
// and nothing records which session a subscription was made in, so ending
// sessions turns push off on every browser of the account.
func TestEndingSessionsDisablesEveryPushSubscription(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	member := mustPushProjectMember(t, env, "pushsignout")
	bystander := mustPushProjectMember(t, env, "pushstays")
	laptop := "https://push.example.test/" + uuid.NewString()
	phone := "https://push.example.test/" + uuid.NewString()
	bystanderEndpoint := "https://push.example.test/" + uuid.NewString()
	mustUpsertPushSubscription(t, env, bystander.ID, bystanderEndpoint)

	for _, revoke := range []struct {
		name string
		call func() (int64, error)
	}{
		{name: "sign out everywhere", call: func() (int64, error) {
			return env.store.RevokeSessionAuthTokensForUser(env.ctx, member.ID)
		}},
		{name: "password change", call: func() (int64, error) {
			return env.store.RevokeSessionAuthTokensForUserExcept(env.ctx, member.ID, uuid.New())
		}},
	} {
		mustUpsertPushSubscription(t, env, member.ID, laptop)
		mustUpsertPushSubscription(t, env, member.ID, phone)
		if _, err := env.store.CreateIssue(store.WithActor(env.ctx, project.OwnerID), store.CreateIssueParams{
			ProjectID: env.projectID, Title: "push " + revoke.name, AssigneeID: &member.ID,
		}); err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
		if created := mustMaterializePushEvents(t, env); created != 2 {
			t.Fatalf("%s: queued deliveries = %d, want 2", revoke.name, created)
		}

		if _, err := revoke.call(); err != nil {
			t.Fatalf("%s: %v", revoke.name, err)
		}
		for _, endpoint := range []string{laptop, phone} {
			if active, err := env.store.PushSubscriptionActive(env.ctx, member.ID, endpoint); err != nil || active {
				t.Fatalf("%s: subscription active = %v, %v", revoke.name, active, err)
			}
		}
		if active, err := env.store.PushSubscriptionActive(env.ctx, bystander.ID, bystanderEndpoint); err != nil || !active {
			t.Fatalf("%s: another user's subscription active = %v, %v", revoke.name, active, err)
		}
		var pending int
		if err := env.pool.QueryRow(env.ctx, `
			SELECT count(*) FROM push_notification_deliveries
			WHERE user_id = $1 AND delivered_at IS NULL AND suppressed_at IS NULL AND failed_at IS NULL
		`, member.ID).Scan(&pending); err != nil || pending != 0 {
			t.Fatalf("%s: pending deliveries = %d, %v", revoke.name, pending, err)
		}
	}
}

func mustPushProjectMember(t *testing.T, env *sprintsTestEnv, prefix string) model.User {
	t.Helper()
	username := prefix + strings.ToLower(uniqueProjectKey(t))
	user, err := env.store.CreateUserProfile(env.ctx, username, username+"@example.com", username)
	if err != nil {
		t.Fatalf("CreateUserProfile %s: %v", prefix, err)
	}
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, user.ID); err != nil {
		t.Fatalf("GrantProjectAccess %s: %v", prefix, err)
	}
	return user
}

func mustUpsertPushSubscription(t *testing.T, env *sprintsTestEnv, userID uuid.UUID, endpoint string) model.PushSubscription {
	t.Helper()
	subscription, err := env.store.UpsertPushSubscription(env.ctx, store.UpsertPushSubscriptionParams{
		UserID: userID, Endpoint: endpoint,
		P256DH:     base64.RawURLEncoding.EncodeToString(make([]byte, 65)),
		AuthSecret: base64.RawURLEncoding.EncodeToString(make([]byte, 16)), UserAgent: "integration browser",
	})
	if err != nil {
		t.Fatalf("UpsertPushSubscription: %v", err)
	}
	return subscription
}

func mustMaterializePushEvents(t *testing.T, env *sprintsTestEnv) int {
	t.Helper()
	events, err := env.store.ClaimPushNotificationEvents(env.ctx, 100, time.Minute)
	if err != nil {
		t.Fatalf("ClaimPushNotificationEvents: %v", err)
	}
	created := 0
	for _, event := range events {
		count, err := env.store.MaterializePushNotificationEvent(env.ctx, event, 24*time.Hour)
		if err != nil {
			t.Fatalf("MaterializePushNotificationEvent: %v", err)
		}
		created += count
	}
	return created
}

func mustClaimPushDeliveries(t *testing.T, env *sprintsTestEnv) []store.PushNotificationDelivery {
	t.Helper()
	deliveries, err := env.store.ClaimPushNotificationDeliveries(env.ctx, 100, time.Minute)
	if err != nil {
		t.Fatalf("ClaimPushNotificationDeliveries: %v", err)
	}
	return deliveries
}

func mustCompletePushDeliveries(t *testing.T, env *sprintsTestEnv, deliveries []store.PushNotificationDelivery) {
	t.Helper()
	for _, delivery := range deliveries {
		if err := env.store.CompletePushNotificationDelivery(env.ctx, delivery); err != nil {
			t.Fatalf("CompletePushNotificationDelivery: %v", err)
		}
	}
}
