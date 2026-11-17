package store_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// answerFilesDefinition is a form with a required file question, a choice,
// and four more optional file questions, so a form can reach its limit of
// files without any one answer reaching its own.
func answerFilesDefinition() model.FormDefinition {
	optional := false
	questions := []model.FormQuestion{
		{ID: "shot", Type: model.FormQuestionFile, Label: "Attach a screenshot"},
		{ID: "channel", Type: model.FormQuestionSingleChoice, Label: "Which channel?", Required: &optional,
			Options: []model.FormOption{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}},
	}
	for i := 1; i <= 4; i++ {
		questions = append(questions, model.FormQuestion{ID: fmt.Sprintf("more%d", i), Type: model.FormQuestionFile, Label: "More", Required: &optional})
	}
	return model.FormDefinition{Sections: []model.FormSection{{Questions: questions}}}
}

func answerFilesForm(t *testing.T, env *sprintsTestEnv, assignee uuid.UUID, attachments ...int) model.Form {
	t.Helper()
	form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
		ProjectID: env.projectID, Title: "Needs files", Definition: answerFilesDefinition(),
		AssigneeID: assignee, CreatedByID: assignee, AttachmentObjectNumbers: attachments,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	return form
}

var answerFileSeq int64
var answerFileSeqMu sync.Mutex

func attachParams(env *sprintsTestEnv, form model.Form, assignee uuid.UUID, question string) store.AttachFormAnswerFileParams {
	answerFileSeqMu.Lock()
	answerFileSeq++
	n := answerFileSeq
	answerFileSeqMu.Unlock()
	return store.AttachFormAnswerFileParams{
		FormID: form.ID, AssigneeID: assignee, QuestionID: question,
		Object: storageObjectParams(env.projectID, assignee, fmt.Sprintf("projects/answers/%s/%d", form.ID, n)),
	}
}

func mustAttach(t *testing.T, env *sprintsTestEnv, form model.Form, assignee uuid.UUID, question string) (model.Form, model.FormAttachment) {
	t.Helper()
	updated, attached, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, assignee, question))
	if err != nil {
		t.Fatalf("AttachFormAnswerFile(%s): %v", question, err)
	}
	return updated, attached
}

func TestAttachAndRemoveFormAnswerFiles(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	asked := mustUploadForForm(t, env, owner, nil, "projects/answers/asked")
	form := answerFilesForm(t, env, owner, asked.Number)
	if len(form.Attachments) != 1 || form.Attachments[0].AnswerQuestionID != "" || form.Attachments[0].IsAnswerFile() {
		t.Fatalf("the asker's media = %+v, want one file with no question", form.Attachments)
	}
	before := countRows(t, env, `SELECT version FROM forms WHERE id = $1`, form.ID)

	updated, first := mustAttach(t, env, form, owner, "shot")
	if first.AnswerQuestionID != "shot" || first.CreatedByID != owner || first.Object.Filename != "image.png" || first.Object.ProjectID != env.projectID {
		t.Fatalf("attached = %+v", first)
	}
	if got := updated.Answers["shot"].Files; len(got) != 1 || got[0] != first.Object.Ref {
		t.Fatalf("Answers[shot].Files = %v, want [%s]", got, first.Object.Ref)
	}
	if !updated.Definition.Questions()[0].Answered(updated.Answers["shot"]) {
		t.Fatal("the file question is not answered by its file")
	}
	_, second := mustAttach(t, env, updated, owner, "shot")
	_, onChoice := mustAttach(t, env, updated, owner, "channel")
	if after := countRows(t, env, `SELECT version FROM forms WHERE id = $1`, form.ID); after != before+3 {
		t.Fatalf("version = %d, want %d: each file counts as an answer change", after, before+3)
	}

	// Reading the form fills the files of each answer, in object order, and
	// the asker's media stays apart.
	for name, read := range map[string]func() (model.Form, error){
		"by id": func() (model.Form, error) { return env.store.GetForm(env.ctx, form.ID) },
		"by number": func() (model.Form, error) {
			return env.store.GetFormByProjectNumber(env.ctx, env.projectID, form.Number)
		},
	} {
		got, err := read()
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if files := got.Answers["shot"].Files; len(files) != 2 || files[0] != first.Object.Ref || files[1] != second.Object.Ref {
			t.Fatalf("%s: shot files = %v", name, files)
		}
		if files := got.Answers["channel"].Files; len(files) != 1 || files[0] != onChoice.Object.Ref {
			t.Fatalf("%s: channel files = %v", name, files)
		}
		if got.Definition.Questions()[1].Answered(got.Answers["channel"]) {
			t.Fatalf("%s: a file answered a choice question", name)
		}
		if len(got.Attachments) != 4 || len(got.AskerAttachments()) != 1 || got.AskerAttachments()[0].Object.ID != asked.ID {
			t.Fatalf("%s: attachments = %d, asker's = %v", name, len(got.Attachments), got.AskerAttachments())
		}
		if got.AnswerFileCount("") != 3 {
			t.Fatalf("%s: answer files = %d", name, got.AnswerFileCount(""))
		}
	}

	// Files are derived: never stored in the answers, not even after a save.
	if _, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{
		FormID: form.ID, AssigneeID: owner,
		Answers: model.FormAnswers{
			"channel": {FormValue: model.FormValue{Selected: []string{"a"}, Files: []string{"object-999"}}, Note: "n"},
			"shot":    {Note: "see this", FormValue: model.FormValue{Files: []string{"object-998"}}},
		},
	}); err != nil {
		t.Fatalf("SaveFormAnswers: %v", err)
	}
	var stored string
	if err := env.pool.QueryRow(env.ctx, `SELECT answers::text FROM forms WHERE id = $1`, form.ID).Scan(&stored); err != nil {
		t.Fatalf("read answers: %v", err)
	}
	if strings.Contains(stored, "files") || strings.Contains(stored, "object-") {
		t.Fatalf("stored answers hold files: %s", stored)
	}
	got, _ := env.store.GetForm(env.ctx, form.ID)
	if len(got.Answers["shot"].Files) != 2 || len(got.Answers["channel"].Files) != 1 || got.Answers["channel"].Selected[0] != "a" || got.Answers["shot"].Note != "see this" {
		t.Fatalf("answers after a save = %+v", got.Answers)
	}
	// Even a form whose stored answers somehow hold files reads them from the
	// attachments alone.
	if _, err := env.pool.Exec(env.ctx, `UPDATE forms SET answers = '{"more1": {"files": ["object-777"]}}' WHERE id = $1`, form.ID); err != nil {
		t.Fatalf("plant files: %v", err)
	}
	got, _ = env.store.GetForm(env.ctx, form.ID)
	if len(got.Answers["more1"].Files) != 0 || len(got.Answers["shot"].Files) != 2 {
		t.Fatalf("planted files were read: %+v", got.Answers)
	}

	// Answer files are the form's: no generic object lookup shows them.
	for _, object := range []model.StorageObject{first.Object, second.Object, onChoice.Object} {
		if _, err := env.store.GetStorageObjectByProjectNumber(env.ctx, env.projectID, object.Number); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s is visible as an ordinary object: %v", object.Ref, err)
		}
		if _, err := env.store.GetVisibleStorageObjectByProjectNumber(env.ctx, env.projectID, object.Number, true); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s is visible as a visible object: %v", object.Ref, err)
		}
	}
	listed, _, err := env.store.ListStorageObjects(env.ctx, store.ListStorageObjectsParams{ProjectID: env.projectID, Limit: 50})
	if err != nil || len(listed) != 0 {
		t.Fatalf("ListStorageObjects = %+v, %v, want none", listed, err)
	}

	// Removing takes the file off the answer and queues its bytes.
	removed, err := env.store.RemoveFormAnswerFile(env.ctx, form.ID, owner, "shot", first.Object.Number)
	if err != nil {
		t.Fatalf("RemoveFormAnswerFile: %v", err)
	}
	if files := removed.Answers["shot"].Files; len(files) != 1 || files[0] != second.Object.Ref {
		t.Fatalf("files after the removal = %v", files)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_attachments WHERE storage_object_id = $1`, first.Object.ID); n != 0 {
		t.Fatal("the attachment row is still there")
	}
	if n := countRows(t, env, `SELECT count(*) FROM storage_objects WHERE id = $1 AND deleted_at IS NOT NULL`, first.Object.ID); n != 1 {
		t.Fatal("the object was not soft-deleted")
	}
	if n := countRows(t, env, `SELECT count(*) FROM storage_object_deletions WHERE storage_object_id = $1 AND status = 'pending'`, first.Object.ID); n != 1 {
		t.Fatalf("queued deletions = %d, want 1", n)
	}
	if _, err := env.store.RemoveFormAnswerFile(env.ctx, form.ID, owner, "shot", first.Object.Number); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removing twice err = %v, want ErrNotFound", err)
	}
	if after := countRows(t, env, `SELECT version FROM forms WHERE id = $1`, form.ID); after < before+5 {
		t.Fatalf("the removal did not touch the form: version %d", after)
	}
}

func TestFormAnswerFilesAreRefusedWhatTheyShouldBe(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	other := mustCreateUser(t, env, "answer-files-other@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, other.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	asked := mustUploadForForm(t, env, owner, nil, "projects/answers/asked2")
	form := answerFilesForm(t, env, owner, asked.Number)
	_, mine := mustAttach(t, env, form, owner, "shot")
	otherForm := answerFilesForm(t, env, owner)
	_, theirs := mustAttach(t, env, otherForm, owner, "shot")
	objectsBefore := countRows(t, env, `SELECT count(*) FROM storage_objects WHERE project_id = $1`, env.projectID)

	// Someone who is not the assignee.
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, other.ID, "shot")); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("attach by another member err = %v, want ErrConflict", err)
	}
	if _, err := env.store.RemoveFormAnswerFile(env.ctx, form.ID, other.ID, "shot", mine.Object.Number); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("remove by another member err = %v, want ErrConflict", err)
	}
	// A form that doesn't exist.
	missing := attachParams(env, form, owner, "shot")
	missing.FormID = uuid.New()
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("attach to an unknown form err = %v, want ErrNotFound", err)
	}
	// A question the form doesn't have.
	var formErr *model.FormError
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, owner, "nope")); !errors.As(err, &formErr) || formErr.Field != "nope" {
		t.Fatalf("attach to an unknown question err = %v, want a *model.FormError", err)
	}
	if _, err := env.store.RemoveFormAnswerFile(env.ctx, form.ID, owner, "nope", mine.Object.Number); !errors.As(err, &formErr) {
		t.Fatalf("remove from an unknown question err = %v, want a *model.FormError", err)
	}

	// Removing a file that is not this answer's: the asker's, another
	// question's, another form's, one that is not there at all.
	for name, tc := range map[string]struct {
		form     uuid.UUID
		question string
		number   int
	}{
		"the asker's":      {form.ID, "shot", asked.Number},
		"another question": {form.ID, "more1", mine.Object.Number},
		"another form":     {form.ID, "shot", theirs.Object.Number},
		"one that is gone": {form.ID, "shot", 99999},
	} {
		if _, err := env.store.RemoveFormAnswerFile(env.ctx, tc.form, owner, tc.question, tc.number); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("remove %s err = %v, want ErrNotFound", name, err)
		}
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_attachments WHERE form_id = $1 AND answer_question_id IS NULL`, form.ID); n != 1 {
		t.Fatalf("the asker's media: %d rows, want 1", n)
	}
	// Nothing was added by the refused attaches.
	if n := countRows(t, env, `SELECT count(*) FROM storage_objects WHERE project_id = $1`, env.projectID); n != objectsBefore {
		t.Fatalf("storage objects = %d, want %d: a refused attach left an object", n, objectsBefore)
	}
	// A second good file, so the reopen below has two to keep.
	mustAttach(t, env, form, owner, "shot")

	// Only while the form is open.
	submitted, err := env.store.SaveFormAnswers(env.ctx, store.SaveFormAnswersParams{FormID: form.ID, AssigneeID: owner})
	if err != nil {
		t.Fatalf("SaveFormAnswers: %v", err)
	}
	if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: submitted.ID, AssigneeID: owner, Via: model.FormSubmitWeb}); err != nil {
		t.Fatalf("SubmitForm: %v", err)
	}
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, owner, "shot")); !errors.Is(err, store.ErrConflict) || !strings.Contains(err.Error(), "can't change") {
		t.Fatalf("attach to a submitted form err = %v, want the status conflict", err)
	}
	if _, err := env.store.RemoveFormAnswerFile(env.ctx, form.ID, owner, "shot", mine.Object.Number); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("remove from a submitted form err = %v, want ErrConflict", err)
	}
	// Reopening keeps the files.
	reopened, err := env.store.ReopenForm(env.ctx, form.ID, owner)
	if err != nil || len(reopened.Answers["shot"].Files) != 2 {
		t.Fatalf("reopened = %+v, %v, want its two files kept", reopened.Answers["shot"], err)
	}
	if _, err := env.store.CancelForm(env.ctx, form.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, owner, "shot")); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("attach to a cancelled form err = %v", err)
	}
}

func TestFormAnswerFileLimits(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	form := answerFilesForm(t, env, owner)

	for i := 0; i < model.FormMaxAnswerFiles; i++ {
		form, _ = mustAttach(t, env, form, owner, "shot")
	}
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, owner, "shot")); !errors.Is(err, store.ErrFormAnswerFilesFull) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("the seventh file on one answer err = %v, want ErrFormAnswerFilesFull", err)
	}
	if err := store.CheckFormAnswerFileAttach(form, "shot"); !errors.Is(err, store.ErrFormAnswerFilesFull) {
		t.Fatalf("CheckFormAnswerFileAttach(full answer) = %v", err)
	}
	// Other answers still take files, up to the form's own limit of 20.
	for i := 0; form.AnswerFileCount("") < model.FormMaxAnswerFilesPerForm; i++ {
		question := fmt.Sprintf("more%d", 1+i/model.FormMaxAnswerFiles)
		form, _ = mustAttach(t, env, form, owner, question)
	}
	if form.AnswerFileCount("") != 20 {
		t.Fatalf("answer files = %d", form.AnswerFileCount(""))
	}
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, owner, "more4")); !errors.Is(err, store.ErrFormFilesFull) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("the twenty-first file on a form err = %v, want ErrFormFilesFull", err)
	}
	if err := store.CheckFormAnswerFileAttach(form, "more4"); !errors.Is(err, store.ErrFormFilesFull) {
		t.Fatalf("CheckFormAnswerFileAttach(full form) = %v", err)
	}
	// The asker's own twenty files are a separate budget: a form that carries
	// its own media still takes answer files (CreateForm takes at most twenty).
	var numbers []int
	for i := 0; i < model.FormMaxAttachments; i++ {
		numbers = append(numbers, mustUploadForForm(t, env, owner, nil, fmt.Sprintf("projects/answers/asker/%d", i)).Number)
	}
	asked := answerFilesForm(t, env, owner, numbers...)
	mustAttach(t, env, asked, owner, "shot")

	// Removing one makes room again.
	removeOne := form.Answers["shot"].Files[0]
	number, _ := model.ParseStorageObjectRef(removeOne)
	if _, err := env.store.RemoveFormAnswerFile(env.ctx, form.ID, owner, "shot", number); err != nil {
		t.Fatalf("RemoveFormAnswerFile: %v", err)
	}
	if _, _, err := env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, owner, "more4")); err != nil {
		t.Fatalf("attach after making room: %v", err)
	}
}

func TestFormAnswerFilesRacingForTheLastPlaceLeaveOneWinner(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	form := answerFilesForm(t, env, owner)
	for i := 0; i < model.FormMaxAnswerFiles-1; i++ {
		form, _ = mustAttach(t, env, form, owner, "shot")
	}

	const racers = 4
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = env.store.AttachFormAnswerFile(env.ctx, attachParams(env, form, owner, "shot"))
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, store.ErrFormAnswerFilesFull):
			t.Fatalf("a racer err = %v, want nil or ErrFormAnswerFilesFull", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d racers won the last place, want 1", won)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_attachments WHERE form_id = $1`, form.ID); n != model.FormMaxAnswerFiles {
		t.Fatalf("attachments = %d, want %d", n, model.FormMaxAnswerFiles)
	}
}

func TestFormAnswerFilesGoWhenAClosedFormsMediaExpires(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	asked := mustUploadForForm(t, env, owner, nil, "projects/answers/exp-asked")
	form := answerFilesForm(t, env, owner, asked.Number)
	_, one := mustAttach(t, env, form, owner, "shot")
	_, two := mustAttach(t, env, form, owner, "more1")
	if _, err := env.store.CancelForm(env.ctx, form.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx, `UPDATE forms SET cancelled_at = now() - interval '31 days' WHERE id = $1`, form.ID); err != nil {
		t.Fatalf("age the form: %v", err)
	}
	done, err := env.store.ExpireFormMedia(env.ctx, formMediaMaxAge, 10)
	if err != nil || done != (store.FormMediaExpiry{Forms: 1, Files: 3}) {
		t.Fatalf("ExpireFormMedia = %+v, %v, want the asker's file and both answer files", done, err)
	}
	for _, object := range []model.StorageObject{asked, one.Object, two.Object} {
		if n := countRows(t, env, `SELECT count(*) FROM storage_objects WHERE id = $1 AND deleted_at IS NOT NULL`, object.ID); n != 1 {
			t.Fatalf("%s was not soft-deleted", object.Ref)
		}
	}
	got, _ := env.store.GetForm(env.ctx, form.ID)
	if len(got.Attachments) != 0 || len(got.Answers["shot"].Files) != 0 || got.MediaRemovedAt == nil {
		t.Fatalf("form after expiry = attachments %d, files %v, removed %v", len(got.Attachments), got.Answers["shot"].Files, got.MediaRemovedAt)
	}
}

func TestFormListsCountFilesAsAnswers(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	form := answerFilesForm(t, env, owner)
	empty := answerFilesForm(t, env, owner)
	mustAttach(t, env, form, owner, "shot")
	mustAttach(t, env, form, owner, "shot")
	mustAttach(t, env, form, owner, "more1")

	rows, _, err := env.store.ListProjectForms(env.ctx, store.ListProjectFormsParams{ProjectID: env.projectID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListProjectForms = %d rows, %v", len(rows), err)
	}
	byID := map[uuid.UUID]model.FormSummary{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	if got := byID[form.ID]; got.RequiredCount != 1 || got.RequiredAnswered != 1 {
		t.Fatalf("form with a file = %d of %d required answered, want 1 of 1", got.RequiredAnswered, got.RequiredCount)
	}
	if got := byID[empty.ID]; got.RequiredAnswered != 0 {
		t.Fatalf("form with no file = %d answered", got.RequiredAnswered)
	}
	inbox, _, err := env.store.ListInboxForms(env.ctx, store.ListInboxFormsParams{UserID: owner, State: store.InboxTodo, Limit: 10})
	if err != nil || len(inbox) != 2 {
		t.Fatalf("ListInboxForms = %d rows, %v", len(inbox), err)
	}
	for _, row := range inbox {
		if row.ID == form.ID && row.RequiredAnswered != 1 {
			t.Fatalf("inbox row for the form with a file = %d answered", row.RequiredAnswered)
		}
	}
	// Submitting works once the file is attached, and not before.
	if _, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: empty.ID, AssigneeID: owner, Via: model.FormSubmitWeb}); err == nil {
		t.Fatal("a form with no file was submitted")
	}
	if got, err := env.store.SubmitForm(env.ctx, store.SubmitFormParams{FormID: form.ID, AssigneeID: owner, Via: model.FormSubmitAPI}); err != nil || got.Status != model.FormStatusSubmitted {
		t.Fatalf("SubmitForm with a file = %+v, %v", got.Status, err)
	}
}

func fileGrantParams(form model.Form, object model.StorageObject, creator uuid.UUID, key string, expiresIn time.Duration) store.CreateFormFileGrantParams {
	return store.CreateFormFileGrantParams{
		FormID: form.ID, StorageObjectID: object.ID, CreatedByID: creator,
		KeyHash: uploadKeyHash(key), ExpiresAt: time.Now().Add(expiresIn),
	}
}

func TestFormFileGrantServesOneFileUntilItCannotBeUsed(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	member := mustCreateUser(t, env, "grant-member@example.com")
	removed := mustCreateUser(t, env, "grant-removed@example.com")
	blocked := mustCreateUser(t, env, "grant-blocked@example.com")
	deleted := mustCreateUser(t, env, "grant-deleted@example.com")
	for _, u := range []model.User{member, removed, blocked, deleted} {
		if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, u.ID); err != nil {
			t.Fatalf("GrantProjectAccess: %v", err)
		}
	}
	asked := mustUploadForForm(t, env, owner, nil, "projects/answers/grant-asked")
	form := answerFilesForm(t, env, owner, asked.Number)
	_, answer := mustAttach(t, env, form, owner, "shot")
	// A read-only member can read the form, and so can make a URL.
	readOnly := mustCreateUser(t, env, "grant-readonly@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, readOnly.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readOnly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}

	for key, creator := range map[string]uuid.UUID{"owner": owner, "member": member.ID, "readonly": readOnly.ID, "removed": removed.ID, "blocked": blocked.ID, "deleted": deleted.ID} {
		grant, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(form, answer.Object, creator, key, 10*time.Minute))
		if err != nil || grant.CreatedByID != creator || grant.FormID != form.ID || grant.ProjectID != env.projectID || time.Until(grant.ExpiresAt) < 9*time.Minute {
			t.Fatalf("CreateFormFileGrant(%s) = %+v, %v", key, grant, err)
		}
	}
	// The asker's media gets a grant too.
	if _, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(form, asked, owner, "asked", time.Minute)); err != nil {
		t.Fatalf("grant for the asker's file: %v", err)
	}

	resolve := func(key string) (store.FormFileGrant, error) {
		return env.store.GetFormFileGrant(env.ctx, uploadKeyHash(key))
	}
	for _, key := range []string{"owner", "member", "readonly", "removed", "blocked", "deleted"} {
		got, err := resolve(key)
		if err != nil || got.Object.ID != answer.Object.ID || got.Object.Filename != "image.png" || got.FormID != form.ID {
			t.Fatalf("GetFormFileGrant(%s) = %+v, %v", key, got, err)
		}
	}
	if got, err := resolve("asked"); err != nil || got.Object.ID != asked.ID {
		t.Fatalf("GetFormFileGrant(asked) = %+v, %v", got, err)
	}

	// Unknown, empty, expired.
	if _, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(form, answer.Object, owner, "expired", -time.Second)); err != nil {
		t.Fatalf("expired grant: %v", err)
	}
	for name, hash := range map[string][]byte{"unknown": uploadKeyHash("nobody"), "empty": {}, "expired": uploadKeyHash("expired")} {
		if _, err := env.store.GetFormFileGrant(env.ctx, hash); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: err = %v, want ErrNotFound", name, err)
		}
	}

	// A creator who can no longer read the form stops being able to use the URL.
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, removed.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	if _, err := env.store.BlockProjectUser(env.ctx, env.projectID, blocked.ID, owner); err != nil {
		t.Fatalf("BlockProjectUser: %v", err)
	}
	if err := env.store.DeleteUser(env.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	for _, key := range []string{"removed", "blocked", "deleted"} {
		if _, err := resolve(key); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: the URL still works after its creator lost the form: %v", key, err)
		}
	}
	for _, key := range []string{"owner", "member", "readonly"} {
		if _, err := resolve(key); err != nil {
			t.Fatalf("%s: the URL stopped working for a member: %v", key, err)
		}
	}

	// A file that is removed from the form stops being served; so does one the
	// asker's media expiry took.
	number := answer.Object.Number
	if _, err := env.store.RemoveFormAnswerFile(env.ctx, form.ID, owner, "shot", number); err != nil {
		t.Fatalf("RemoveFormAnswerFile: %v", err)
	}
	if _, err := resolve("owner"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the URL of a removed file err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(form, answer.Object, owner, "late", time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a grant for a removed file err = %v, want ErrNotFound", err)
	}
	// A file of another form is not the form's to give out.
	otherForm := answerFilesForm(t, env, owner)
	if _, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(otherForm, asked, owner, "wrong-form", time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a grant for another form's file err = %v, want ErrNotFound", err)
	}

	// A deleted project serves nothing and takes no grant.
	if _, err := resolve("asked"); err != nil {
		t.Fatalf("asked before the project is deleted: %v", err)
	}
	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := resolve("asked"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the URL after the project was deleted err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(form, asked, owner, "gone", time.Minute)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a grant in a deleted project err = %v, want ErrNotFound", err)
	}
}

func TestCreateFormFileGrantRefusesWhatItCannotKeep(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	asked := mustUploadForForm(t, env, owner, nil, "projects/answers/grant-refuse")
	form := answerFilesForm(t, env, owner, asked.Number)
	if _, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(form, asked, owner, "taken", time.Minute)); err != nil {
		t.Fatalf("CreateFormFileGrant: %v", err)
	}
	dup := fileGrantParams(form, asked, owner, "taken", time.Minute)
	if _, err := env.store.CreateFormFileGrant(env.ctx, dup); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a key already in use err = %v, want ErrConflict", err)
	}
	short := fileGrantParams(form, asked, owner, "short", time.Minute)
	short.KeyHash = []byte("short")
	if _, err := env.store.CreateFormFileGrant(env.ctx, short); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("a key hash that is not a SHA-256 err = %v, want ErrConflict", err)
	}
	nobody := fileGrantParams(form, asked, uuid.New(), "nobody", time.Minute)
	if _, err := env.store.CreateFormFileGrant(env.ctx, nobody); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a creator who does not exist err = %v, want ErrNotFound", err)
	}
}

func TestSweepFormUploadsDeletesExpiredFileGrants(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	asked := mustUploadForForm(t, env, owner, nil, "projects/answers/sweep")
	form := answerFilesForm(t, env, owner, asked.Number)
	for key, expiresIn := range map[string]time.Duration{"old": -48 * time.Hour, "recent": -time.Hour, "live": time.Hour} {
		if _, err := env.store.CreateFormFileGrant(env.ctx, fileGrantParams(form, asked, owner, key, expiresIn)); err != nil {
			t.Fatalf("CreateFormFileGrant(%s): %v", key, err)
		}
	}
	swept, err := env.store.SweepFormUploads(env.ctx, 24*time.Hour)
	if err != nil || swept.FileGrants != 1 {
		t.Fatalf("SweepFormUploads = %+v, %v, want one file grant", swept, err)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_file_grants WHERE form_id = $1`, form.ID); n != 2 {
		t.Fatalf("file grants left = %d, want 2", n)
	}
}
