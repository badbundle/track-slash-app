package store_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0061 makes only verified addresses unique. Rolling it back
// restores the old constraint, so where several accounts share an address one
// of them keeps it: a live account before a deleted one, then a verified one,
// then the oldest.
func TestEmailVerificationMigrationRollback(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 61); err != nil {
		t.Fatalf("goose.UpTo(61): %v", err)
	}
	insert := func(username, email string, verified, deleted bool) string {
		t.Helper()
		var id string
		if err := db.SQL.QueryRow(`
			INSERT INTO users (username, email, name, email_verified_at, deleted_at, created_at)
			VALUES ($1, NULLIF($2, ''), $1,
			        CASE WHEN $3 THEN now() END,
			        CASE WHEN $4 THEN now() END,
			        now() + (SELECT count(*) FROM users) * interval '1 minute')
			RETURNING id
		`, username, email, verified, deleted).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", username, err)
		}
		return id
	}
	// Created in this order, so each is younger than the one before.
	oldestShared := insert("oldestshared", "shared@example.com", false, false)
	verifiedShared := insert("verifiedshared", "shared@example.com", true, false)
	deletedVerified := insert("deletedverified", "gone@example.com", true, true)
	liveGone := insert("livegone", "gone@example.com", false, false)
	first := insert("firstplain", "plain@example.com", false, false)
	second := insert("secondplain", "plain@example.com", false, false)
	alone := insert("alone", "alone@example.com", true, false)
	insert("noemail", "", false, false)

	// Up: only verified, live addresses are unique, ignoring case.
	if _, err := db.SQL.Exec(`INSERT INTO users (username, email, name, email_verified_at) VALUES ('clash', 'ALONE@example.com', 'Clash', now())`); err == nil {
		t.Fatal("a second verified ALONE@example.com was accepted")
	}
	if _, err := db.SQL.Exec(`INSERT INTO users (username, email, name, email_verified_at) VALUES ('noaddress', NULL, 'No address', now())`); err == nil {
		t.Fatal("a verified account without an address was accepted")
	}

	if err := goose.Down(db.SQL, "."); err != nil {
		t.Fatalf("goose.Down: %v", err)
	}
	emailOf := func(id string) string {
		t.Helper()
		var email sql.NullString
		if err := db.SQL.QueryRow(`SELECT email FROM users WHERE id = $1`, id).Scan(&email); err != nil {
			t.Fatalf("read email: %v", err)
		}
		return email.String
	}
	for _, tt := range []struct {
		name, id, want string
	}{
		{"verified beats older", verifiedShared, "shared@example.com"},
		{"older loses to verified", oldestShared, ""},
		{"live beats deleted", liveGone, "gone@example.com"},
		{"deleted loses", deletedVerified, ""},
		{"oldest keeps it", first, "plain@example.com"},
		{"younger loses", second, ""},
		{"sole holder keeps it", alone, "alone@example.com"},
	} {
		if got := emailOf(tt.id); got != tt.want {
			t.Errorf("%s: email = %q, want %q", tt.name, got, tt.want)
		}
	}
	if _, err := db.SQL.Exec(`INSERT INTO users (username, email, name) VALUES ('again', 'plain@example.com', 'Again')`); err == nil {
		t.Fatal("the restored unique constraint let plain@example.com in twice")
	}
	var tables int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM pg_tables WHERE tablename = 'email_verifications'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("email_verifications after rollback = %d, %v", tables, err)
	}
	if err := goose.UpTo(db.SQL, ".", 61); err != nil {
		t.Fatalf("goose.UpTo(61) again: %v", err)
	}
}
