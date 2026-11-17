package store_test

import (
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0062 adds form_attachments.answer_question_id and the download
// grants. Rows that existed stay the asker's media, a rollback removes the
// answer files (and soft-deletes their objects) and nothing else, and it all
// comes back.
func TestFormAnswerFilesMigration(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 61); err != nil {
		t.Fatalf("goose.UpTo(61): %v", err)
	}
	var userID, projectID, formID, objectID string
	must := func(err error, what string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	must(db.SQL.QueryRow(`INSERT INTO users (email, name, username) VALUES ('af@example.com', 'AF', 'afuser') RETURNING id`).Scan(&userID), "insert user")
	must(db.SQL.QueryRow(`INSERT INTO projects (key, name, owner_id) VALUES ('ANSFILES', 'Af', $1) RETURNING id`, userID).Scan(&projectID), "insert project")
	must(db.SQL.QueryRow(`INSERT INTO forms (project_id, number, title, definition, assignee_id, created_by_id) VALUES ($1, 1, 'Ask', '{"sections":[]}', $2, $2) RETURNING id`, projectID, userID).Scan(&formID), "insert form")
	newObject := func(number int) string {
		var id string
		must(db.SQL.QueryRow(`
			INSERT INTO storage_objects (project_id, number, backend, bucket, object_key, filename, content_type, byte_size, sha256, created_by_id)
			VALUES ($1, $2, 'local', 'local', $3, 'a.png', 'image/png', 1, repeat('a', 64), $4) RETURNING id`,
			projectID, number, "k/"+string(rune('a'+number)), userID).Scan(&id), "insert object")
		return id
	}
	objectID = newObject(1)
	_, err := db.SQL.Exec(`INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id) VALUES ($1, $2, $3, $4)`, projectID, formID, objectID, userID)
	must(err, "insert asker's attachment before the migration")

	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		must(db.SQL.QueryRow(query, args...).Scan(&n), query)
		return n
	}
	if count(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'form_file_grants'`) != 0 {
		t.Fatal("the grants table exists before the migration")
	}

	must(goose.UpTo(db.SQL, ".", 62), "goose.UpTo(62)")
	if count(`SELECT count(*) FROM form_attachments WHERE storage_object_id = $1 AND answer_question_id IS NULL`, objectID) != 1 {
		t.Fatal("the existing attachment did not stay the asker's")
	}
	answerObject := newObject(2)
	_, err = db.SQL.Exec(`INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id, answer_question_id) VALUES ($1, $2, $3, $4, 'shot')`, projectID, formID, answerObject, userID)
	must(err, "insert an answer file")
	_, err = db.SQL.Exec(`INSERT INTO form_file_grants (project_id, form_id, storage_object_id, created_by_id, key_hash, expires_at) VALUES ($1, $2, $3, $4, decode(repeat('ab', 32), 'hex'), now() + interval '1 minute')`, projectID, formID, objectID, userID)
	must(err, "insert a grant")
	if _, err := db.SQL.Exec(`INSERT INTO form_file_grants (project_id, form_id, storage_object_id, created_by_id, key_hash, expires_at) VALUES ($1, $2, $3, $4, 'short', now())`, projectID, formID, objectID, userID); err == nil {
		t.Fatal("a key hash that is not 32 bytes was accepted")
	}

	must(goose.DownTo(db.SQL, ".", 61), "goose.DownTo(61)")
	if count(`SELECT count(*) FROM form_attachments WHERE form_id = $1`, formID) != 1 || count(`SELECT count(*) FROM form_attachments WHERE storage_object_id = $1`, objectID) != 1 {
		t.Fatal("the rollback kept something other than the asker's media")
	}
	if count(`SELECT count(*) FROM storage_objects WHERE id = $1 AND deleted_at IS NOT NULL`, answerObject) != 1 {
		t.Fatal("the answer file's object was not soft-deleted")
	}
	if count(`SELECT count(*) FROM storage_objects WHERE id = $1 AND deleted_at IS NULL`, objectID) != 1 {
		t.Fatal("the asker's object was touched")
	}
	if count(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'form_attachments' AND column_name = 'answer_question_id'`) != 0 {
		t.Fatal("the column survived the rollback")
	}

	must(goose.UpTo(db.SQL, ".", 62), "goose.UpTo(62) again")
	if count(`SELECT count(*) FROM form_attachments WHERE storage_object_id = $1 AND answer_question_id IS NULL`, objectID) != 1 {
		t.Fatal("the asker's attachment changed across the round trip")
	}
}
