package store_test

import (
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0058 lets a push delivery be about a form instead of an issue.
// Existing issue deliveries survive it, a delivery names exactly one of the
// two, and rolling it back removes the form deliveries and nothing else.
func TestFormNotificationsMigration(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 57); err != nil {
		t.Fatalf("goose.UpTo(57): %v", err)
	}
	var userID, projectID, issueID, subscriptionID, changelogID string
	if err := db.SQL.QueryRow(`INSERT INTO users (email, name, username) VALUES ('fn@example.com', 'FN', 'fnuser') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO projects (key, name, owner_id) VALUES ('FNOT', 'Fn', $1) RETURNING id`, userID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO issues (project_id, number, title, reporter_id) VALUES ($1, 1, 'old', $2) RETURNING id`, projectID, userID).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	if err := db.SQL.QueryRow(`
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth_secret) VALUES ($1, 'https://push.example.test/fn', 'a', 'b') RETURNING id
	`, userID).Scan(&subscriptionID); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	if err := db.SQL.QueryRow(`
		INSERT INTO project_changelog_entries (project_id, entity, op, entity_id, issue_id, summary)
		VALUES ($1, 'issue', 'insert', $2, $2, 'Created old') RETURNING id
	`, projectID, issueID).Scan(&changelogID); err != nil {
		t.Fatalf("insert the issue's changelog entry: %v", err)
	}
	if _, err := db.SQL.Exec(`
		INSERT INTO push_notification_deliveries (changelog_id, subscription_id, user_id, project_id, issue_id, category)
		VALUES ($1, $2, $3, $4, $5, 'assignments')
	`, changelogID, subscriptionID, userID, projectID, issueID); err != nil {
		t.Fatalf("insert issue delivery: %v", err)
	}

	if err := goose.UpTo(db.SQL, ".", 58); err != nil {
		t.Fatalf("goose.UpTo(58): %v", err)
	}
	var kept int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM push_notification_deliveries WHERE issue_id = $1 AND form_id IS NULL`, issueID).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("issue deliveries after the migration = %d, %v", kept, err)
	}
	var formsOn bool
	if err := db.SQL.QueryRow(`
		INSERT INTO user_push_notification_preferences (user_id) VALUES ($1) RETURNING forms
	`, userID).Scan(&formsOn); err != nil || !formsOn {
		t.Fatalf("forms preference default = %v, %v", formsOn, err)
	}
	var formID, formEntryID string
	if err := db.SQL.QueryRow(`
		INSERT INTO forms (project_id, number, title, definition, assignee_id, created_by_id)
		VALUES ($1, 1, 'Ask', '{"sections":[]}', $2, $2) RETURNING id
	`, projectID, userID).Scan(&formID); err != nil {
		t.Fatalf("insert form: %v", err)
	}
	if err := db.SQL.QueryRow(`
		INSERT INTO project_changelog_entries (project_id, entity, op, entity_id, summary, members_only)
		VALUES ($1, 'form', 'insert', $2, 'Asked form Ask', true) RETURNING id
	`, projectID, formID).Scan(&formEntryID); err != nil {
		t.Fatalf("insert form changelog entry: %v", err)
	}
	var queued int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM push_notification_events WHERE changelog_id = $1`, formEntryID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("form entry queued events = %d, %v", queued, err)
	}
	insertDelivery := func(issue, form any, category string) error {
		_, err := db.SQL.Exec(`
			INSERT INTO push_notification_deliveries (changelog_id, subscription_id, user_id, project_id, issue_id, form_id, category)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, formEntryID, subscriptionID, userID, projectID, issue, form, category)
		return err
	}
	if err := insertDelivery(issueID, formID, "forms"); err == nil || !strings.Contains(err.Error(), "one_target") {
		t.Fatalf("a delivery naming both an issue and a form: %v", err)
	}
	if err := insertDelivery(nil, nil, "forms"); err == nil || !strings.Contains(err.Error(), "one_target") {
		t.Fatalf("a delivery naming neither: %v", err)
	}
	if err := insertDelivery(nil, formID, "mentions_typo"); err == nil || !strings.Contains(err.Error(), "category_check") {
		t.Fatalf("a delivery with an unknown category: %v", err)
	}
	if err := insertDelivery(nil, formID, "forms"); err != nil {
		t.Fatalf("insert form delivery: %v", err)
	}

	if err := goose.DownTo(db.SQL, ".", 57); err != nil {
		t.Fatalf("goose.DownTo(57): %v", err)
	}
	var total, withIssue int
	if err := db.SQL.QueryRow(`SELECT count(*), count(issue_id) FROM push_notification_deliveries`).Scan(&total, &withIssue); err != nil || total != 1 || withIssue != 1 {
		t.Fatalf("deliveries after rollback = %d (%d with an issue), %v", total, withIssue, err)
	}
	if err := db.SQL.QueryRow(`SELECT count(*) FROM push_notification_events WHERE changelog_id = $1`, formEntryID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("form events after rollback = %d, %v", queued, err)
	}
	if err := goose.UpTo(db.SQL, ".", 58); err != nil {
		t.Fatalf("goose.UpTo(58) again: %v", err)
	}
}
