package store_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0059 gives a form an optional wait key hash. Forms that existed
// before have none, a hash is a SHA-256 that only one form holds, and rolling
// the migration back keeps every form.
func TestFormWaitKeysMigration(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 58); err != nil {
		t.Fatalf("goose.UpTo(58): %v", err)
	}
	var userID, projectID string
	if err := db.SQL.QueryRow(`INSERT INTO users (email, name, username) VALUES ('fw@example.com', 'FW', 'fwuser') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO projects (key, name, owner_id) VALUES ('FWAIT', 'Fw', $1) RETURNING id`, userID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	insertForm := func(number int, withHash bool, hash []byte) error {
		if !withHash {
			_, err := db.SQL.Exec(`
				INSERT INTO forms (project_id, number, title, definition, assignee_id, created_by_id)
				VALUES ($1, $2, 'Ask', '{"sections":[]}', $3, $3)
			`, projectID, number, userID)
			return err
		}
		_, err := db.SQL.Exec(`
			INSERT INTO forms (project_id, number, title, definition, assignee_id, created_by_id, wait_key_hash)
			VALUES ($1, $2, 'Ask', '{"sections":[]}', $3, $3, $4)
		`, projectID, number, userID, hash)
		return err
	}
	if err := insertForm(1, false, nil); err != nil {
		t.Fatalf("insert a form before the migration: %v", err)
	}

	if err := goose.UpTo(db.SQL, ".", 59); err != nil {
		t.Fatalf("goose.UpTo(59): %v", err)
	}
	var withoutKey int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM forms WHERE wait_key_hash IS NULL`).Scan(&withoutKey); err != nil || withoutKey != 1 {
		t.Fatalf("forms without a key after the migration = %d, %v", withoutKey, err)
	}
	// Any number of forms may have no key.
	if err := insertForm(2, false, nil); err != nil {
		t.Fatalf("insert a second form without a key: %v", err)
	}
	hash := sha256.Sum256([]byte("a wait key"))
	if err := insertForm(3, true, hash[:]); err != nil {
		t.Fatalf("insert a form with a key hash: %v", err)
	}
	if err := insertForm(4, true, hash[:]); err == nil || !strings.Contains(err.Error(), "forms_wait_key_hash") {
		t.Fatalf("a second form with the same key hash: err = %v, want the unique index", err)
	}
	if err := insertForm(4, true, []byte("not a hash")); err == nil || !strings.Contains(err.Error(), "forms_wait_key_hash_length_check") {
		t.Fatalf("a key hash that is not 32 bytes: err = %v, want the length check", err)
	}

	if err := goose.DownTo(db.SQL, ".", 58); err != nil {
		t.Fatalf("goose.DownTo(58): %v", err)
	}
	var forms, columns int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM forms`).Scan(&forms); err != nil || forms != 3 {
		t.Fatalf("forms after rolling back = %d, %v; want all 3 kept", forms, err)
	}
	if err := db.SQL.QueryRow(`
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'forms' AND column_name = 'wait_key_hash'
	`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("wait_key_hash columns after rolling back = %d, %v", columns, err)
	}

	if err := goose.UpTo(db.SQL, ".", 59); err != nil {
		t.Fatalf("goose.UpTo(59) again: %v", err)
	}
	if err := insertForm(4, true, hash[:]); err != nil {
		t.Fatalf("insert a form with a key hash after migrating up again: %v", err)
	}
}
