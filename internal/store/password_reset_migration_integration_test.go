package store_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0063 adds password reset links, which keep only a 32-byte token
// hash and go with their user. Rolling it back drops them.
func TestPasswordResetMigrationRollback(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 63); err != nil {
		t.Fatalf("goose.UpTo(63): %v", err)
	}
	var userID string
	if err := db.SQL.QueryRow(`INSERT INTO users (username, email, name, email_verified_at) VALUES ('resetter', 'resetter@example.com', 'Resetter', now()) RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	insert := `INSERT INTO password_resets (user_id, email, token_hash, expires_at) VALUES ($1, 'resetter@example.com', $2, now() + interval '30 minutes')`
	if _, err := db.SQL.Exec(insert, userID, []byte("a raw token, not a hash")); err == nil {
		t.Fatal("a token_hash that is not 32 bytes was accepted")
	}
	hash := make([]byte, 32)
	if _, err := db.SQL.Exec(insert, userID, hash); err != nil {
		t.Fatalf("insert link: %v", err)
	}
	if _, err := db.SQL.Exec(insert, userID, hash); err == nil {
		t.Fatal("a second link with the same token hash was accepted")
	}
	if _, err := db.SQL.Exec(`DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var links int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM password_resets`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("links after their user went = %d, %v", links, err)
	}

	if err := goose.Down(db.SQL, "."); err != nil {
		t.Fatalf("goose.Down: %v", err)
	}
	var tables int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM pg_tables WHERE tablename = 'password_resets'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("password_resets after rollback = %d, %v", tables, err)
	}
	if err := goose.UpTo(db.SQL, ".", 63); err != nil {
		t.Fatalf("goose.UpTo(63) again: %v", err)
	}
}
