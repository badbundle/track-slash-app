package store_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0061 adds forms.media_removed_at. Forms that existed keep their
// data and have no marker, the column goes on a rollback, and it comes back.
func TestFormMediaRemovedMigration(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 60); err != nil {
		t.Fatalf("goose.UpTo(60): %v", err)
	}
	var userID, projectID string
	if err := db.SQL.QueryRow(`INSERT INTO users (email, name, username) VALUES ('mr@example.com', 'MR', 'mruser') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO projects (key, name, owner_id) VALUES ('MREMOVED', 'Mr', $1) RETURNING id`, userID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.SQL.Exec(`
		INSERT INTO forms (project_id, number, title, definition, assignee_id, created_by_id)
		VALUES ($1, 1, 'Ask', '{"sections":[]}', $2, $2)
	`, projectID, userID); err != nil {
		t.Fatalf("insert form before the migration: %v", err)
	}
	hasColumn := func() bool {
		t.Helper()
		var n int
		if err := db.SQL.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'forms' AND column_name = 'media_removed_at'`).Scan(&n); err != nil {
			t.Fatalf("look for the column: %v", err)
		}
		return n == 1
	}
	if hasColumn() {
		t.Fatal("the column exists before the migration")
	}

	if err := goose.UpTo(db.SQL, ".", 61); err != nil {
		t.Fatalf("goose.UpTo(61): %v", err)
	}
	var removedAt *string
	var title string
	if err := db.SQL.QueryRow(`SELECT title, media_removed_at::text FROM forms WHERE number = 1`).Scan(&title, &removedAt); err != nil || title != "Ask" || removedAt != nil {
		t.Fatalf("existing form = %q, %v, %v, want it kept with no marker", title, removedAt, err)
	}
	if _, err := db.SQL.Exec(`UPDATE forms SET media_removed_at = now() WHERE number = 1`); err != nil {
		t.Fatalf("set the marker: %v", err)
	}

	if err := goose.DownTo(db.SQL, ".", 60); err != nil {
		t.Fatalf("goose.DownTo(60): %v", err)
	}
	if hasColumn() {
		t.Fatal("the column survived the rollback")
	}
	if err := db.SQL.QueryRow(`SELECT title FROM forms WHERE number = 1`).Scan(&title); err != nil || title != "Ask" {
		t.Fatalf("form after the rollback = %q, %v", title, err)
	}

	if err := goose.UpTo(db.SQL, ".", 61); err != nil {
		t.Fatalf("goose.UpTo(61) again: %v", err)
	}
	if !hasColumn() {
		t.Fatal("the column is missing after migrating up again")
	}
}
