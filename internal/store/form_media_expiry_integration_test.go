package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

const formMediaMaxAge = 30 * 24 * time.Hour

// expiryForm is a form made for the expiry tests, with the objects it carries.
type expiryForm struct {
	form    model.Form
	objects []model.StorageObject
}

// closedFormWithFiles makes a form with the given number of files, leaves it
// in the status asked for (open, submitted, acknowledged or cancelled) and
// sets the time it reached that status to the given age.
func closedFormWithFiles(t *testing.T, env *sprintsTestEnv, status model.FormStatus, files int, age time.Duration) expiryForm {
	t.Helper()
	owner := formsEnvOwner(t, env)
	var objects []model.StorageObject
	var numbers []int
	for i := 0; i < files; i++ {
		object := mustUploadForForm(t, env, owner, nil, fmt.Sprintf("projects/expiry/%s/%s", uuid.NewString(), status))
		objects = append(objects, object)
		numbers = append(numbers, object.Number)
	}
	form, err := createFormWithMedia(env, owner, formsTestDefinition(), numbers...)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	set := func(query string) {
		t.Helper()
		if _, err := env.pool.Exec(env.ctx, query, form.ID, age.String()); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	switch status {
	case model.FormStatusOpen:
		set(`UPDATE forms SET created_at = now() - $2::interval WHERE id = $1`)
	case model.FormStatusCancelled:
		if _, err := env.store.CancelForm(env.ctx, form.ID); err != nil {
			t.Fatalf("CancelForm: %v", err)
		}
		set(`UPDATE forms SET cancelled_at = now() - $2::interval WHERE id = $1`)
	default:
		form = mustTickRotated(t, env, form)
		if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitWeb}); err != nil {
			t.Fatalf("SubmitForm: %v", err)
		}
		set(`UPDATE forms SET submitted_at = now() - $2::interval WHERE id = $1`)
		if status == model.FormStatusAcknowledged {
			if _, err := env.store.AcknowledgeForm(env.ctx, form.ID); err != nil {
				t.Fatalf("AcknowledgeForm: %v", err)
			}
			set(`UPDATE forms SET acknowledged_at = now() - $2::interval WHERE id = $1`)
		}
	}
	return expiryForm{form: form, objects: objects}
}

func (f expiryForm) marked(t *testing.T, env *sprintsTestEnv) (removedAt *time.Time, updatedAt time.Time) {
	t.Helper()
	if err := env.pool.QueryRow(env.ctx, `SELECT media_removed_at, updated_at FROM forms WHERE id = $1`, f.form.ID).Scan(&removedAt, &updatedAt); err != nil {
		t.Fatalf("read form: %v", err)
	}
	return removedAt, updatedAt
}

func (f expiryForm) requireKept(t *testing.T, env *sprintsTestEnv, what string) {
	t.Helper()
	if removedAt, _ := f.marked(t, env); removedAt != nil {
		t.Fatalf("%s: the form was marked", what)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_attachments WHERE form_id = $1`, f.form.ID); n != len(f.objects) {
		t.Fatalf("%s: %d attachments left, want %d", what, n, len(f.objects))
	}
	for _, object := range f.objects {
		if n := countRows(t, env, `SELECT count(*) FROM storage_objects WHERE id = $1 AND deleted_at IS NULL`, object.ID); n != 1 {
			t.Fatalf("%s: %s was deleted", what, object.Ref)
		}
	}
}

func (f expiryForm) requireRemoved(t *testing.T, env *sprintsTestEnv, what string) {
	t.Helper()
	removedAt, _ := f.marked(t, env)
	if removedAt == nil || time.Since(*removedAt) > time.Minute {
		t.Fatalf("%s: media_removed_at = %v, want about now", what, removedAt)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_attachments WHERE form_id = $1`, f.form.ID); n != 0 {
		t.Fatalf("%s: %d attachments left", what, n)
	}
	for _, object := range f.objects {
		if n := countRows(t, env, `SELECT count(*) FROM storage_objects WHERE id = $1 AND deleted_at IS NOT NULL`, object.ID); n != 1 {
			t.Fatalf("%s: %s was not soft-deleted", what, object.Ref)
		}
		if n := countRows(t, env, `SELECT count(*) FROM storage_object_deletions WHERE storage_object_id = $1 AND status = 'pending'`, object.ID); n != 1 {
			t.Fatalf("%s: %s has %d queued deletions, want 1", what, object.Ref, n)
		}
	}
}

func TestExpireFormMediaRemovesTheFilesOfOldClosedForms(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	acknowledged := closedFormWithFiles(t, env, model.FormStatusAcknowledged, 2, 31*24*time.Hour)
	cancelled := closedFormWithFiles(t, env, model.FormStatusCancelled, 1, 31*24*time.Hour)
	recent := closedFormWithFiles(t, env, model.FormStatusAcknowledged, 1, 29*24*time.Hour)
	recentCancelled := closedFormWithFiles(t, env, model.FormStatusCancelled, 1, 29*24*time.Hour)
	open := closedFormWithFiles(t, env, model.FormStatusOpen, 1, 400*24*time.Hour)
	submitted := closedFormWithFiles(t, env, model.FormStatusSubmitted, 1, 400*24*time.Hour)
	noFiles := closedFormWithFiles(t, env, model.FormStatusAcknowledged, 0, 31*24*time.Hour)

	_, ackUpdated := acknowledged.marked(t, env)
	_, cancelledUpdated := cancelled.marked(t, env)

	done, err := env.store.ExpireFormMedia(env.ctx, formMediaMaxAge, 10)
	if err != nil {
		t.Fatalf("ExpireFormMedia: %v", err)
	}
	if done != (store.FormMediaExpiry{Forms: 2, Files: 3}) {
		t.Fatalf("expired = %+v, want 2 forms and 3 files", done)
	}
	acknowledged.requireRemoved(t, env, "acknowledged 31 days ago")
	cancelled.requireRemoved(t, env, "cancelled 31 days ago")
	recent.requireKept(t, env, "acknowledged 29 days ago")
	recentCancelled.requireKept(t, env, "cancelled 29 days ago")
	open.requireKept(t, env, "open, any age")
	submitted.requireKept(t, env, "submitted, any age")
	// A form with no files is never marked.
	if removedAt, _ := noFiles.marked(t, env); removedAt != nil {
		t.Fatal("a form with no files was marked")
	}

	// The Inbox orders finished forms by updated_at, so removal leaves it.
	if _, updated := acknowledged.marked(t, env); !updated.Equal(ackUpdated) {
		t.Fatalf("acknowledged form updated_at moved from %v to %v", ackUpdated, updated)
	}
	if _, updated := cancelled.marked(t, env); !updated.Equal(cancelledUpdated) {
		t.Fatalf("cancelled form updated_at moved from %v to %v", cancelledUpdated, updated)
	}

	// The marker is read with the form, and the attachments are gone.
	got, err := env.store.GetForm(env.ctx, acknowledged.form.ID)
	if err != nil || got.MediaRemovedAt == nil || len(got.Attachments) != 0 {
		t.Fatalf("GetForm = removed %v, attachments %d, %v", got.MediaRemovedAt, len(got.Attachments), err)
	}
	if kept, err := env.store.GetForm(env.ctx, recent.form.ID); err != nil || kept.MediaRemovedAt != nil || len(kept.Attachments) != 1 {
		t.Fatalf("kept form = removed %v, attachments %d, %v", kept.MediaRemovedAt, len(kept.Attachments), err)
	}
	listed, _, err := env.store.ListProjectForms(env.ctx, store.ListProjectFormsParams{ProjectID: env.projectID, Limit: 50})
	if err != nil {
		t.Fatalf("ListProjectForms: %v", err)
	}
	marked := 0
	for _, row := range listed {
		if row.MediaRemovedAt != nil {
			marked++
		}
	}
	if marked != 2 {
		t.Fatalf("list rows with media_removed_at = %d, want 2", marked)
	}

	// A form that is already marked is not handled twice.
	if again, err := env.store.ExpireFormMedia(env.ctx, formMediaMaxAge, 10); err != nil || again != (store.FormMediaExpiry{}) {
		t.Fatalf("second run = %+v, %v, want nothing", again, err)
	}
}

func TestExpireFormMediaTakesAtMostTheLimitPerRun(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	first := closedFormWithFiles(t, env, model.FormStatusCancelled, 1, 40*24*time.Hour)
	second := closedFormWithFiles(t, env, model.FormStatusAcknowledged, 2, 35*24*time.Hour)
	third := closedFormWithFiles(t, env, model.FormStatusCancelled, 1, 31*24*time.Hour)

	// The longest-closed forms go first.
	done, err := env.store.ExpireFormMedia(env.ctx, formMediaMaxAge, 2)
	if err != nil || done != (store.FormMediaExpiry{Forms: 2, Files: 3}) {
		t.Fatalf("first run = %+v, %v, want 2 forms and 3 files", done, err)
	}
	first.requireRemoved(t, env, "oldest")
	second.requireRemoved(t, env, "second oldest")
	third.requireKept(t, env, "third, past the limit")

	done, err = env.store.ExpireFormMedia(env.ctx, formMediaMaxAge, 2)
	if err != nil || done != (store.FormMediaExpiry{Forms: 1, Files: 1}) {
		t.Fatalf("second run = %+v, %v, want the rest", done, err)
	}
	third.requireRemoved(t, env, "third")
}

func TestExpireFormMediaSkipsAFormAnotherTransactionHolds(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	held := closedFormWithFiles(t, env, model.FormStatusCancelled, 1, 31*24*time.Hour)
	free := closedFormWithFiles(t, env, model.FormStatusCancelled, 1, 32*24*time.Hour)

	tx, err := env.pool.Begin(env.ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(env.ctx) }()
	if _, err := tx.Exec(env.ctx, `SELECT id FROM forms WHERE id = $1 FOR UPDATE`, held.form.ID); err != nil {
		t.Fatalf("lock form: %v", err)
	}
	done, err := env.store.ExpireFormMedia(env.ctx, formMediaMaxAge, 10)
	if err != nil || done != (store.FormMediaExpiry{Forms: 1, Files: 1}) {
		t.Fatalf("run beside a held form = %+v, %v, want only the free one", done, err)
	}
	held.requireKept(t, env, "held by another transaction")
	free.requireRemoved(t, env, "free")

	if err := tx.Rollback(env.ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if done, err := env.store.ExpireFormMedia(env.ctx, formMediaMaxAge, 10); err != nil || done.Forms != 1 {
		t.Fatalf("run after the lock is gone = %+v, %v", done, err)
	}
	held.requireRemoved(t, env, "held, once free")
}
