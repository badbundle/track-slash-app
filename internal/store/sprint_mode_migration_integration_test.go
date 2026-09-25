package store_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0044 keeps every project that already used sprints in sprint mode
// and leaves the rest on the new default.
func TestSprintModeMigrationBackfillAndRollback(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 43); err != nil {
		t.Fatalf("goose.UpTo(43): %v", err)
	}

	var userID string
	if err := db.SQL.QueryRow(`
		INSERT INTO users (email, name, username)
		VALUES ('sprint-mode-migration@example.com', 'Sprint Mode Migration', 'sprint-mode-migration')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	projectIDs := map[string]string{}
	for _, key := range []string{"NONE", "PLANNED", "ACTIVE", "DONE", "DELETED"} {
		var id string
		if err := db.SQL.QueryRow(`
			INSERT INTO projects (key, name, owner_id) VALUES ($1, $1, $2) RETURNING id
		`, key, userID).Scan(&id); err != nil {
			t.Fatalf("insert project %s: %v", key, err)
		}
		projectIDs[key] = id
	}
	for _, sprint := range []struct {
		project string
		status  string
		deleted bool
	}{
		{project: "PLANNED", status: "planned"},
		{project: "ACTIVE", status: "active"},
		{project: "DONE", status: "completed"},
		{project: "DELETED", status: "planned", deleted: true},
	} {
		if _, err := db.SQL.Exec(`
			INSERT INTO sprints (project_id, number, name, status, completed_at, deleted_at)
			VALUES ($1, 1, 'Sprint', $2::sprint_status,
			        CASE WHEN $3::boolean THEN now() END,
			        CASE WHEN $4::boolean THEN now() END)
		`, projectIDs[sprint.project], sprint.status, sprint.status == "completed", sprint.deleted); err != nil {
			t.Fatalf("insert %s sprint for %s: %v", sprint.status, sprint.project, err)
		}
	}

	if err := goose.UpTo(db.SQL, ".", 44); err != nil {
		t.Fatalf("goose.UpTo(44): %v", err)
	}
	for key, want := range map[string]bool{
		"NONE":    false,
		"PLANNED": true,
		"ACTIVE":  true,
		"DONE":    true,
		"DELETED": false,
	} {
		var got bool
		if err := db.SQL.QueryRow(`SELECT sprints_enabled FROM projects WHERE id = $1`, projectIDs[key]).Scan(&got); err != nil {
			t.Fatalf("read %s sprints_enabled: %v", key, err)
		}
		if got != want {
			t.Fatalf("%s sprints_enabled = %t, want %t", key, got, want)
		}
	}
	var fresh bool
	if err := db.SQL.QueryRow(`
		INSERT INTO projects (key, name, owner_id) VALUES ('FRESH', 'Fresh', $1) RETURNING sprints_enabled
	`, userID).Scan(&fresh); err != nil {
		t.Fatalf("insert fresh project: %v", err)
	}
	if fresh {
		t.Fatal("new project sprints_enabled = true, want the false default")
	}

	if err := goose.DownTo(db.SQL, ".", 43); err != nil {
		t.Fatalf("goose.DownTo(43): %v", err)
	}
	var columns int
	if err := db.SQL.QueryRow(`
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'projects' AND column_name = 'sprints_enabled'
	`).Scan(&columns); err != nil {
		t.Fatalf("count sprints_enabled columns: %v", err)
	}
	if columns != 0 {
		t.Fatalf("sprints_enabled columns after rollback = %d, want 0", columns)
	}
}
