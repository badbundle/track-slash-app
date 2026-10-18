package store_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0057 keeps the earlier changelog entries of comments deleted
// before it to members, and rolling it back leaves them that way.
func TestMembersOnlyFlagsMigration(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 56); err != nil {
		t.Fatalf("goose.UpTo(56): %v", err)
	}
	var userID, projectID, issueID, commentID string
	if err := db.SQL.QueryRow(`
		INSERT INTO users (email, name, username) VALUES ('flags@example.com', 'Flags', 'flags') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO projects (key, name, owner_id) VALUES ('FLAG', 'Flags', $1) RETURNING id`, userID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO issues (project_id, number, title) VALUES ($1, 1, 'Issue') RETURNING id`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO comments (issue_id, number, author_id, body) VALUES ($1, 1, $2, 'Still here') RETURNING id`, issueID, userID).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}
	deletedID := uuid.NewString()
	entry := func(entityID, op string) string {
		t.Helper()
		var id string
		if err := db.SQL.QueryRow(`
			INSERT INTO project_changelog_entries (project_id, entity, op, entity_id, issue_id, summary, details)
			VALUES ($1, 'comment', $2, $3, $4, 'Commented', '{"preview": "words"}')
			RETURNING id
		`, projectID, op, entityID, issueID).Scan(&id); err != nil {
			t.Fatalf("insert %s entry: %v", op, err)
		}
		return id
	}
	live := entry(commentID, "insert")
	deletedInsert := entry(deletedID, "insert")
	deletedUpdate := entry(deletedID, "update")
	deletedDelete := entry(deletedID, "delete")
	want := map[string]bool{live: false, deletedInsert: true, deletedUpdate: true, deletedDelete: false}
	check := func(when string) {
		t.Helper()
		for id, membersOnly := range want {
			var got bool
			if err := db.SQL.QueryRow(`SELECT members_only FROM project_changelog_entries WHERE id = $1`, id).Scan(&got); err != nil || got != membersOnly {
				t.Fatalf("%s: entry %s members_only = %v, %v; want %v", when, id, got, err, membersOnly)
			}
		}
	}

	if err := goose.UpTo(db.SQL, ".", 57); err != nil {
		t.Fatalf("goose.UpTo(57): %v", err)
	}
	check("after migrating")
	if err := goose.DownTo(db.SQL, ".", 56); err != nil {
		t.Fatalf("goose.DownTo(56): %v", err)
	}
	check("after rolling back")
	if err := goose.UpTo(db.SQL, ".", 57); err != nil {
		t.Fatalf("goose.UpTo(57) again: %v", err)
	}
}
