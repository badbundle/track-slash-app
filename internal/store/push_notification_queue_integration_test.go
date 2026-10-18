package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

func TestExpirePushNotificationEventsClosesOnlyStaleQueuedEvents(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	if _, err := env.store.ExpirePushNotificationEvents(env.ctx, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("zero max age err = %v", err)
	}
	stale := mustQueuePushEvent(t, env, "stale")
	fresh := mustQueuePushEvent(t, env, "fresh")
	failed := mustQueuePushEvent(t, env, "failed")
	if _, err := env.pool.Exec(env.ctx, `
		UPDATE push_notification_events SET created_at = now() - interval '2 days'
		WHERE changelog_id = ANY($1)
	`, []uuid.UUID{stale, failed}); err != nil {
		t.Fatalf("backdate events: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx, `UPDATE push_notification_events SET failed_at = now() WHERE changelog_id = $1`, failed); err != nil {
		t.Fatalf("fail event: %v", err)
	}

	expired, err := env.store.ExpirePushNotificationEvents(env.ctx, 24*time.Hour)
	if err != nil || expired != 1 {
		t.Fatalf("ExpirePushNotificationEvents = %d, %v; want 1", expired, err)
	}
	var note string
	var processed bool
	if err := env.pool.QueryRow(env.ctx, `
		SELECT processed_at IS NOT NULL, last_error FROM push_notification_events WHERE changelog_id = $1
	`, stale).Scan(&processed, &note); err != nil || !processed || note != "event expired before delivery" {
		t.Fatalf("stale event processed=%v note=%q err=%v", processed, note, err)
	}
	events, err := env.store.ClaimPushNotificationEvents(env.ctx, 100, time.Minute)
	if err != nil || len(events) != 1 || events[0].ChangelogID != fresh {
		t.Fatalf("claimable events after expiry = %+v, %v; want only %s", events, err, fresh)
	}
}

func TestPrunePushNotificationsDeletesOnlyOldFinishedRows(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	if _, err := env.store.PrunePushNotifications(env.ctx, 0); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("zero retention err = %v", err)
	}
	member := mustPushProjectMember(t, env, "pushprune")
	mustUpsertPushSubscription(t, env, member.ID, "https://push.example.test/"+uuid.NewString())
	oldDone := mustQueuePushEvent(t, env, "old done")
	oldWaiting := mustQueuePushEvent(t, env, "old waiting")
	newDone := mustQueuePushEvent(t, env, "new done")
	if _, err := env.pool.Exec(env.ctx, `
		UPDATE push_notification_events
		SET processed_at = CASE WHEN changelog_id = $2 THEN NULL ELSE now() END,
		    created_at = CASE WHEN changelog_id = $3 THEN created_at ELSE now() - interval '30 days' END
		WHERE changelog_id = ANY($1)
	`, []uuid.UUID{oldDone, oldWaiting, newDone}, oldWaiting, newDone); err != nil {
		t.Fatalf("age events: %v", err)
	}
	// Deliveries: one old and delivered, one old and still being retried.
	for _, delivery := range []struct {
		changelogID uuid.UUID
		delivered   bool
	}{{oldDone, true}, {oldWaiting, false}} {
		if _, err := env.pool.Exec(env.ctx, `
			INSERT INTO push_notification_deliveries (
				changelog_id, subscription_id, user_id, project_id, issue_id, category, delivered_at, created_at
			)
			SELECT $1, s.id, s.user_id, c.project_id, c.issue_id, 'assignments',
			       CASE WHEN $2 THEN now() END, now() - interval '30 days'
			FROM push_subscriptions s, project_changelog_entries c
			WHERE s.user_id = $3 AND c.id = $1
		`, delivery.changelogID, delivery.delivered, member.ID); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}

	pruned, err := env.store.PrunePushNotifications(env.ctx, 7*24*time.Hour)
	if err != nil || pruned != 2 {
		t.Fatalf("PrunePushNotifications = %d, %v; want 2", pruned, err)
	}
	var events, deliveries int
	if err := env.pool.QueryRow(env.ctx, `
		SELECT (SELECT count(*) FROM push_notification_events WHERE changelog_id = ANY($1)),
		       (SELECT count(*) FROM push_notification_deliveries WHERE changelog_id = $2 AND delivered_at IS NULL)
	`, []uuid.UUID{oldWaiting, newDone}, oldWaiting).Scan(&events, &deliveries); err != nil || events != 2 || deliveries != 1 {
		t.Fatalf("kept events = %d, kept deliveries = %d, err = %v", events, deliveries, err)
	}
}

// mustQueuePushEvent creates an issue, whose changelog entry queues one push
// event, and returns that event's changelog ID.
func mustQueuePushEvent(t *testing.T, env *sprintsTestEnv, title string) uuid.UUID {
	t.Helper()
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: title})
	if err != nil {
		t.Fatalf("CreateIssue %s: %v", title, err)
	}
	var changelogID uuid.UUID
	if err := env.pool.QueryRow(env.ctx, `
		SELECT e.changelog_id
		FROM push_notification_events e
		JOIN project_changelog_entries c ON c.id = e.changelog_id
		WHERE c.issue_id = $1
	`, issue.ID).Scan(&changelogID); err != nil {
		t.Fatalf("queued event for %s: %v", title, err)
	}
	return changelogID
}
