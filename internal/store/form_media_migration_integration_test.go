package store_test

import (
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/bradleymackey/track-slash/internal/migrations"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

// Migration 0060 adds the tables a form's files live in. It keeps every form
// and file that existed, holds each rule the store relies on, and rolls back
// to a database with no trace of them.
func TestFormMediaMigration(t *testing.T) {
	db := testutil.NewEmptyDatabase(t)
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("goose.SetDialect: %v", err)
	}
	if err := goose.UpTo(db.SQL, ".", 59); err != nil {
		t.Fatalf("goose.UpTo(59): %v", err)
	}
	var userID, projectID, otherProjectID, formID string
	if err := db.SQL.QueryRow(`INSERT INTO users (email, name, username) VALUES ('fm@example.com', 'FM', 'fmuser') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO projects (key, name, owner_id) VALUES ('FMEDIA', 'Fm', $1) RETURNING id`, userID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := db.SQL.QueryRow(`INSERT INTO projects (key, name, owner_id) VALUES ('FMOTHER', 'Other', $1) RETURNING id`, userID).Scan(&otherProjectID); err != nil {
		t.Fatalf("insert other project: %v", err)
	}
	if err := db.SQL.QueryRow(`
		INSERT INTO forms (project_id, number, title, definition, assignee_id, created_by_id)
		VALUES ($1, 1, 'Ask', '{"sections":[]}', $2, $2) RETURNING id
	`, projectID, userID).Scan(&formID); err != nil {
		t.Fatalf("insert form before the migration: %v", err)
	}

	if err := goose.UpTo(db.SQL, ".", 60); err != nil {
		t.Fatalf("goose.UpTo(60): %v", err)
	}
	insertObject := func(project string, number int) (id string) {
		t.Helper()
		if err := db.SQL.QueryRow(`
			INSERT INTO storage_objects (project_id, number, backend, bucket, object_key, filename, content_type, byte_size, sha256, created_by_id)
			VALUES ($1, $2, 'local', 'local', $3, 'a.png', 'image/png', 1, $4, $5) RETURNING id
		`, project, number, project+"/"+string(rune('a'+number)), strings.Repeat("a", 64), userID).Scan(&id); err != nil {
			t.Fatalf("insert object: %v", err)
		}
		return id
	}
	expectErr := func(what string, err error, contains string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), contains) {
			t.Fatalf("%s: err = %v, want it to mention %s", what, err, contains)
		}
	}

	hash := sha256.Sum256([]byte("an upload key"))
	var grantID string
	if err := db.SQL.QueryRow(`
		INSERT INTO form_upload_grants (project_id, created_by_id, key_hash, expires_at, max_files)
		VALUES ($1, $2, $3, now() + interval '1 hour', 2) RETURNING id
	`, projectID, userID, hash[:]).Scan(&grantID); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	_, err := db.SQL.Exec(`INSERT INTO form_upload_grants (project_id, created_by_id, key_hash, expires_at, max_files) VALUES ($1, $2, $3, now(), 1)`, projectID, userID, hash[:])
	expectErr("a second grant with the same key hash", err, "form_upload_grants_key_hash_key")
	_, err = db.SQL.Exec(`INSERT INTO form_upload_grants (project_id, created_by_id, key_hash, expires_at, max_files) VALUES ($1, $2, $3, now(), 1)`, projectID, userID, []byte("short"))
	expectErr("a key hash that is not 32 bytes", err, "form_upload_grants_key_hash_length_check")
	_, err = db.SQL.Exec(`UPDATE form_upload_grants SET files_used = 3 WHERE id = $1`, grantID)
	expectErr("more files used than allowed", err, "form_upload_grants_files_within_max_check")
	_, err = db.SQL.Exec(`UPDATE form_upload_grants SET files_used = -1 WHERE id = $1`, grantID)
	expectErr("a negative count", err, "form_upload_grants_files_used_check")

	pending := insertObject(projectID, 1)
	if _, err := db.SQL.Exec(`INSERT INTO form_uploads (storage_object_id, project_id, created_by_id, grant_id) VALUES ($1, $2, $3, $4)`, pending, projectID, userID, grantID); err != nil {
		t.Fatalf("insert pending upload: %v", err)
	}
	_, err = db.SQL.Exec(`INSERT INTO form_uploads (storage_object_id, project_id, created_by_id) VALUES ($1, $2, $3)`, pending, projectID, userID)
	expectErr("the same object pending twice", err, "form_uploads_pkey")
	foreign := insertObject(otherProjectID, 1)
	_, err = db.SQL.Exec(`INSERT INTO form_uploads (storage_object_id, project_id, created_by_id) VALUES ($1, $2, $3)`, foreign, projectID, userID)
	expectErr("a pending upload in another project's object", err, "form_uploads_object_project_fk")

	attached := insertObject(projectID, 2)
	if _, err := db.SQL.Exec(`INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id) VALUES ($1, $2, $3, $4)`, projectID, formID, attached, userID); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	_, err = db.SQL.Exec(`INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id) VALUES ($1, $2, $3, $4)`, projectID, formID, attached, userID)
	expectErr("the same object attached twice", err, "form_attachments_form_object_key")
	_, err = db.SQL.Exec(`INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id) VALUES ($1, $2, $3, $4)`, projectID, formID, foreign, userID)
	expectErr("another project's object on the form", err, "form_attachments_object_project_fk")
	_, err = db.SQL.Exec(`INSERT INTO form_attachments (project_id, form_id, storage_object_id, created_by_id) VALUES ($1, $2, $3, $4)`, otherProjectID, formID, foreign, userID)
	expectErr("a form of another project", err, "form_attachments_form_project_fk")

	// The files follow the form and the project that own them.
	if _, err := db.SQL.Exec(`DELETE FROM forms WHERE id = $1`, formID); err != nil {
		t.Fatalf("delete form: %v", err)
	}
	var attachments int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM form_attachments`).Scan(&attachments); err != nil || attachments != 0 {
		t.Fatalf("attachments after deleting the form = %d, %v", attachments, err)
	}
	if _, err := db.SQL.Exec(`DELETE FROM projects WHERE id = $1`, projectID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	var left int
	if err := db.SQL.QueryRow(`SELECT (SELECT count(*) FROM form_uploads) + (SELECT count(*) FROM form_upload_grants)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("uploads and grants after deleting the project = %d, %v", left, err)
	}

	// Rolling back removes the tables and keeps the forms.
	if err := db.SQL.QueryRow(`
		INSERT INTO forms (project_id, number, title, definition, assignee_id, created_by_id)
		VALUES ($1, 1, 'Kept', '{"sections":[]}', $2, $2) RETURNING id
	`, otherProjectID, userID).Scan(&formID); err != nil {
		t.Fatalf("insert form: %v", err)
	}
	if err := goose.DownTo(db.SQL, ".", 59); err != nil {
		t.Fatalf("goose.DownTo(59): %v", err)
	}
	var tables, forms int
	if err := db.SQL.QueryRow(`
		SELECT count(*) FROM information_schema.tables
		WHERE table_name IN ('form_upload_grants', 'form_uploads', 'form_attachments')
	`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("form media tables after rolling back = %d, %v", tables, err)
	}
	if err := db.SQL.QueryRow(`SELECT count(*) FROM forms`).Scan(&forms); err != nil || forms != 1 {
		t.Fatalf("forms after rolling back = %d, %v; want the one kept", forms, err)
	}

	if err := goose.UpTo(db.SQL, ".", 60); err != nil {
		t.Fatalf("goose.UpTo(60) again: %v", err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO form_upload_grants (project_id, created_by_id, key_hash, expires_at, max_files) VALUES ($1, $2, $3, now(), 1)`, otherProjectID, userID, hash[:]); err != nil {
		t.Fatalf("insert grant after migrating up again: %v", err)
	}
}
