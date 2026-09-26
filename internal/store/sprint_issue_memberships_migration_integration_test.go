package store_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

const (
	membershipsMigrationBefore = 45
	membershipsMigration       = 46
)

func TestSprintIssueMembershipsMigrationBackfillTriggerAndRollback(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", membershipsMigrationBefore); err != nil {
		t.Fatalf("goose.UpTo(before): %v", err)
	}

	t0 := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	var userID, projectID string
	if err := db.SQL.QueryRow(`
		INSERT INTO users (email, name, username)
		VALUES ('membership-migration@example.com', 'Membership Migration', 'membership-migration')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.SQL.QueryRow(`
		INSERT INTO projects (key, name, owner_id) VALUES ('MEMMIG', 'Membership Migration', $1) RETURNING id
	`, userID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	insertSprint := func(number int, status string, created time.Time, completed *time.Time) string {
		t.Helper()
		var id string
		if err := db.SQL.QueryRow(`
			INSERT INTO sprints (project_id, number, name, status, created_at, completed_at)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id
		`, projectID, number, "Sprint", status, created, completed).Scan(&id); err != nil {
			t.Fatalf("insert sprint %d: %v", number, err)
		}
		return id
	}
	completedAt := t0.AddDate(0, 0, 14)
	doneSprint := insertSprint(1, "completed", t0, &completedAt)
	activeSprint := insertSprint(2, "active", t0.AddDate(0, 0, 10), nil)
	insertIssue := func(number int, status string, created time.Time, sprintID any) string {
		t.Helper()
		var id string
		if err := db.SQL.QueryRow(`
			INSERT INTO issues (project_id, number, title, status, created_at, sprint_id)
			VALUES ($1, $2, 'Issue', $3, $4, $5) RETURNING id
		`, projectID, number, status, created, sprintID).Scan(&id); err != nil {
			t.Fatalf("insert issue %d: %v", number, err)
		}
		return id
	}
	finished := insertIssue(1, "done", t0.AddDate(0, 0, -3), doneSprint)
	carried := insertIssue(2, "todo", t0.AddDate(0, 0, 2), activeSprint)
	fresh := insertIssue(3, "todo", t0.AddDate(0, 0, 12), activeSprint)
	insertIssue(4, "todo", t0, nil)
	for _, snapshot := range []struct{ issueID, status string }{{finished, "done"}, {carried, "todo"}} {
		if _, err := db.SQL.Exec(`
			INSERT INTO sprint_issue_snapshots (project_id, sprint_id, issue_id, status, snapshotted_at)
			VALUES ($1, $2, $3, $4, $5)
		`, projectID, doneSprint, snapshot.issueID, snapshot.status, completedAt); err != nil {
			t.Fatalf("insert snapshot: %v", err)
		}
	}

	if err := goose.UpTo(db.SQL, ".", membershipsMigration); err != nil {
		t.Fatalf("goose.UpTo(memberships): %v", err)
	}

	type membership struct {
		sprintID, issueID string
		added             time.Time
		removed           *time.Time
	}
	want := []membership{
		{sprintID: doneSprint, issueID: finished, added: t0},
		{sprintID: doneSprint, issueID: carried, added: t0.AddDate(0, 0, 2), removed: &completedAt},
		{sprintID: activeSprint, issueID: carried, added: t0.AddDate(0, 0, 10)},
		{sprintID: activeSprint, issueID: fresh, added: t0.AddDate(0, 0, 12)},
	}
	rows, err := db.SQL.Query(`
		SELECT sprint_id, issue_id, added_at, removed_at, backfilled
		FROM sprint_issue_memberships
		ORDER BY added_at, sprint_id
	`)
	if err != nil {
		t.Fatalf("list memberships: %v", err)
	}
	var got []membership
	for rows.Next() {
		var m membership
		var removed sql.NullTime
		var backfilled bool
		if err := rows.Scan(&m.sprintID, &m.issueID, &m.added, &removed, &backfilled); err != nil {
			t.Fatalf("scan membership: %v", err)
		}
		if !backfilled {
			t.Fatalf("migrated membership %+v not marked backfilled", m)
		}
		if removed.Valid {
			r := removed.Time
			m.removed = &r
		}
		got = append(got, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate memberships: %v", err)
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("memberships = %+v, want %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.sprintID != w.sprintID || g.issueID != w.issueID || !g.added.Equal(w.added) || (g.removed == nil) != (w.removed == nil) || (w.removed != nil && !g.removed.Equal(*w.removed)) {
			t.Fatalf("membership %d = %+v, want %+v", i, g, w)
		}
	}

	// The trigger closes the open row and opens a new one on every move, and
	// ignores updates that leave sprint_id alone.
	if _, err := db.SQL.Exec(`UPDATE issues SET title = 'Renamed' WHERE id = $1`, fresh); err != nil {
		t.Fatalf("rename issue: %v", err)
	}
	if _, err := db.SQL.Exec(`UPDATE issues SET sprint_id = $2 WHERE id = $1`, fresh, doneSprint); err != nil {
		t.Fatalf("move issue: %v", err)
	}
	if _, err := db.SQL.Exec(`UPDATE issues SET sprint_id = NULL WHERE id = $1`, fresh); err != nil {
		t.Fatalf("clear issue sprint: %v", err)
	}
	var open, closed, total int
	if err := db.SQL.QueryRow(`
		SELECT count(*) FILTER (WHERE removed_at IS NULL), count(*) FILTER (WHERE removed_at IS NOT NULL AND NOT backfilled), count(*)
		FROM sprint_issue_memberships WHERE issue_id = $1
	`, fresh).Scan(&open, &closed, &total); err != nil {
		t.Fatalf("count moved memberships: %v", err)
	}
	if open != 0 || closed != 1 || total != 2 {
		t.Fatalf("moved memberships open=%d closed=%d total=%d", open, closed, total)
	}
	if _, err := db.SQL.Exec(`
		INSERT INTO issues (project_id, number, title, sprint_id) VALUES ($1, 5, 'Created in sprint', $2)
	`, projectID, activeSprint); err != nil {
		t.Fatalf("insert issue in sprint: %v", err)
	}
	if err := db.SQL.QueryRow(`
		SELECT count(*) FROM sprint_issue_memberships m JOIN issues i ON i.id = m.issue_id
		WHERE i.number = 5 AND m.removed_at IS NULL AND NOT m.backfilled
	`).Scan(&open); err != nil || open != 1 {
		t.Fatalf("inserted issue memberships = %d err %v", open, err)
	}

	if err := goose.DownTo(db.SQL, ".", membershipsMigrationBefore); err != nil {
		t.Fatalf("goose.DownTo(before): %v", err)
	}
	var exists bool
	if err := db.SQL.QueryRow(`SELECT to_regclass('sprint_issue_memberships') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("memberships table after rollback exists=%v err=%v", exists, err)
	}
	if _, err := db.SQL.Exec(`UPDATE issues SET sprint_id = $2 WHERE id = $1`, fresh, activeSprint); err != nil {
		t.Fatalf("move issue after rollback: %v", err)
	}
}
