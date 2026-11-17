package store_test

import (
	"crypto/sha256"
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

func uploadKeyHash(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

func mustCreateGrant(t *testing.T, env *sprintsTestEnv, creatorID uuid.UUID, key string, maxFiles int, expiresIn time.Duration) store.FormUploadGrant {
	t.Helper()
	grant, err := env.store.CreateFormUploadGrant(env.ctx, store.CreateFormUploadGrantParams{
		ProjectID:   env.projectID,
		CreatedByID: creatorID,
		KeyHash:     uploadKeyHash(key),
		ExpiresAt:   time.Now().Add(expiresIn),
		MaxFiles:    maxFiles,
	})
	if err != nil {
		t.Fatalf("CreateFormUploadGrant %q: %v", key, err)
	}
	return grant
}

// mustUploadForForm records a pending upload for a form, as the server does
// once the bytes are stored. grant may be nil, for an upload with a token.
func mustUploadForForm(t *testing.T, env *sprintsTestEnv, creatorID uuid.UUID, grant *store.FormUploadGrant, key string) model.StorageObject {
	t.Helper()
	params := store.CreateFormUploadParams{Object: storageObjectParams(env.projectID, creatorID, key)}
	if grant != nil {
		params.GrantID = &grant.ID
	}
	object, err := env.store.CreateFormUpload(env.ctx, params)
	if err != nil {
		t.Fatalf("CreateFormUpload %q: %v", key, err)
	}
	return object
}

func countRows(t *testing.T, env *sprintsTestEnv, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.pool.QueryRow(env.ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func pendingUploads(t *testing.T, env *sprintsTestEnv) int {
	return countRows(t, env, `SELECT count(*) FROM form_uploads WHERE project_id = $1`, env.projectID)
}

func nextFormNumber(t *testing.T, env *sprintsTestEnv) int {
	return countRows(t, env, `SELECT next_form_number FROM projects WHERE id = $1`, env.projectID)
}

func createFormWithMedia(env *sprintsTestEnv, creatorID uuid.UUID, def model.FormDefinition, attachments ...int) (model.Form, error) {
	return env.store.CreateForm(env.ctx, store.CreateFormParams{
		ProjectID:               env.projectID,
		Title:                   "With media",
		Definition:              def,
		AssigneeID:              creatorID,
		CreatedByID:             creatorID,
		AttachmentObjectNumbers: attachments,
	})
}

// mediaDefinition is a form whose question shows the first object and whose
// first option shows the second.
func mediaDefinition(question, option string) model.FormDefinition {
	def := formsTestDefinition()
	def.Sections[0].Questions[0].Media = []string{question}
	def.Sections[0].Questions[0].Options[0].Media = option
	return def
}

func TestFormUploadGrantTakesFilesUpToItsLimit(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	grant := mustCreateGrant(t, env, owner, "key-a", 2, time.Hour)
	if grant.MaxFiles != 2 || grant.FilesUsed != 0 || grant.ProjectID != env.projectID || grant.CreatedByID != owner {
		t.Fatalf("grant = %+v", grant)
	}

	got, err := env.store.GetFormUploadGrant(env.ctx, uploadKeyHash("key-a"))
	if err != nil || got.ID != grant.ID {
		t.Fatalf("GetFormUploadGrant = %+v, %v", got, err)
	}
	first := mustUploadForForm(t, env, owner, &grant, "projects/up/1")
	second := mustUploadForForm(t, env, owner, &grant, "projects/up/2")
	if first.Number != 1 || second.Number != 2 || first.ProjectID != env.projectID {
		t.Fatalf("objects = %+v %+v", first, second)
	}

	// The grant is spent: it is still a known key, but says so.
	if _, err := env.store.GetFormUploadGrant(env.ctx, uploadKeyHash("key-a")); !errors.Is(err, store.ErrFormUploadGrantFull) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("spent grant err = %v, want ErrFormUploadGrantFull wrapping ErrConflict", err)
	}
	// A late upload that got past the check still can't take a place, and
	// leaves nothing behind.
	_, err = env.store.CreateFormUpload(env.ctx, store.CreateFormUploadParams{
		Object:  storageObjectParams(env.projectID, owner, "projects/up/3"),
		GrantID: &grant.ID,
	})
	if !errors.Is(err, store.ErrFormUploadGrantFull) {
		t.Fatalf("upload past the limit err = %v", err)
	}
	if n := countRows(t, env, `SELECT count(*) FROM storage_objects WHERE object_key = 'projects/up/3'`); n != 0 {
		t.Fatalf("%d objects were left by the refused upload", n)
	}

	// A token upload takes no place from any grant.
	mustUploadForForm(t, env, owner, nil, "projects/up/token")
	if n := pendingUploads(t, env); n != 3 {
		t.Fatalf("pending uploads = %d, want 3", n)
	}
	var grantID *uuid.UUID
	if err := env.pool.QueryRow(env.ctx, `SELECT grant_id FROM form_uploads WHERE storage_object_id = (SELECT id FROM storage_objects WHERE object_key = 'projects/up/token')`).Scan(&grantID); err != nil || grantID != nil {
		t.Fatalf("token upload grant = %v, %v, want none", grantID, err)
	}
}

func TestFormUploadsRacingForTheLastPlaceLeaveOneWinner(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	grant := mustCreateGrant(t, env, owner, "race", 1, time.Hour)

	const racers = 8
	errs := make([]error, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = env.store.CreateFormUpload(env.ctx, store.CreateFormUploadParams{
				Object:  storageObjectParams(env.projectID, owner, fmt.Sprintf("projects/race/%d", i)),
				GrantID: &grant.ID,
			})
		}()
	}
	close(start)
	wg.Wait()

	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, store.ErrFormUploadGrantFull):
			t.Fatalf("racer err = %v", err)
		}
	}
	if won != 1 || pendingUploads(t, env) != 1 {
		t.Fatalf("winners = %d, pending = %d, want 1 and 1", won, pendingUploads(t, env))
	}
	if used := countRows(t, env, `SELECT files_used FROM form_upload_grants WHERE id = $1`, grant.ID); used != 1 {
		t.Fatalf("files_used = %d, want 1", used)
	}
}

func TestFormUploadGrantIsNotFoundWhenItCannotBeUsed(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)

	mustCreateGrant(t, env, owner, "expired", 3, -time.Minute)
	for name, hash := range map[string][]byte{
		"unknown": uploadKeyHash("nobody was given this"),
		"empty":   {},
		"expired": uploadKeyHash("expired"),
	} {
		if _, err := env.store.GetFormUploadGrant(env.ctx, hash); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	// An expired grant takes no more files either.
	expired, _ := env.store.CreateFormUploadGrant(env.ctx, store.CreateFormUploadGrantParams{
		ProjectID: env.projectID, CreatedByID: owner, KeyHash: uploadKeyHash("just-expired"), ExpiresAt: time.Now().Add(-time.Second), MaxFiles: 1,
	})
	_, err := env.store.CreateFormUpload(env.ctx, store.CreateFormUploadParams{
		Object: storageObjectParams(env.projectID, owner, "projects/exp/1"), GrantID: &expired.ID,
	})
	if !errors.Is(err, store.ErrFormUploadGrantFull) {
		t.Fatalf("upload on an expired grant err = %v", err)
	}
}

func TestFormUploadGrantStopsWhenItsCreatorCannotWrite(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	member := mustCreateUser(t, env, "up-member@example.com")
	readOnly := mustCreateUser(t, env, "up-readonly@example.com")
	blocked := mustCreateUser(t, env, "up-blocked@example.com")
	removed := mustCreateUser(t, env, "up-removed@example.com")
	deleted := mustCreateUser(t, env, "up-deleted@example.com")
	for _, u := range []model.User{member, readOnly, blocked, removed, deleted} {
		if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, u.ID); err != nil {
			t.Fatalf("GrantProjectAccess: %v", err)
		}
		mustCreateGrant(t, env, u.ID, u.Email, 5, time.Hour)
	}
	mustCreateGrant(t, env, owner, "owner", 5, time.Hour)

	found := func(key string) bool {
		t.Helper()
		_, err := env.store.GetFormUploadGrant(env.ctx, uploadKeyHash(key))
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetFormUploadGrant %q: %v", key, err)
		}
		return err == nil
	}
	for _, key := range []string{member.Email, readOnly.Email, blocked.Email, removed.Email, deleted.Email, "owner"} {
		if !found(key) {
			t.Fatalf("%s: upload key not found before anything changed", key)
		}
	}

	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readOnly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	if _, err := env.store.BlockProjectUser(env.ctx, env.projectID, blocked.ID, owner); err != nil {
		t.Fatalf("BlockProjectUser: %v", err)
	}
	if err := env.store.RevokeProjectAccess(env.ctx, env.projectID, removed.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	if err := env.store.DeleteUser(env.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	for _, key := range []string{readOnly.Email, blocked.Email, removed.Email, deleted.Email} {
		if found(key) {
			t.Fatalf("%s: upload key still works after its creator lost write access", key)
		}
	}
	for _, key := range []string{member.Email, "owner"} {
		if !found(key) {
			t.Fatalf("%s: upload key stopped working for someone who can still write", key)
		}
	}

	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	for _, key := range []string{member.Email, "owner"} {
		if found(key) {
			t.Fatalf("%s: upload key still works after the project was deleted", key)
		}
	}
	// Nothing is recorded for a project that is gone, and the grant keeps its
	// place for nobody.
	grant := store.FormUploadGrant{}
	if err := env.pool.QueryRow(env.ctx, `SELECT id FROM form_upload_grants WHERE key_hash = $1`, uploadKeyHash("owner")).Scan(&grant.ID); err != nil {
		t.Fatalf("select grant: %v", err)
	}
	_, err := env.store.CreateFormUpload(env.ctx, store.CreateFormUploadParams{
		Object: storageObjectParams(env.projectID, owner, "projects/gone/1"), GrantID: &grant.ID,
	})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("upload to a deleted project err = %v, want ErrNotFound", err)
	}
	if used := countRows(t, env, `SELECT files_used FROM form_upload_grants WHERE id = $1`, grant.ID); used != 0 {
		t.Fatalf("files_used = %d after a refused upload, want 0", used)
	}
}

func TestCreateFormUploadGrantRefusesWhatItCannotKeep(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	mustCreateGrant(t, env, owner, "taken", 1, time.Hour)

	base := store.CreateFormUploadGrantParams{
		ProjectID: env.projectID, CreatedByID: owner, KeyHash: uploadKeyHash("fresh"), ExpiresAt: time.Now().Add(time.Hour), MaxFiles: 1,
	}
	for name, tc := range map[string]struct {
		mutate func(*store.CreateFormUploadGrantParams)
		want   error
	}{
		"key hash in use":       {func(p *store.CreateFormUploadGrantParams) { p.KeyHash = uploadKeyHash("taken") }, store.ErrConflict},
		"hash not a SHA-256":    {func(p *store.CreateFormUploadGrantParams) { p.KeyHash = []byte("short") }, store.ErrConflict},
		"no place to take":      {func(p *store.CreateFormUploadGrantParams) { p.MaxFiles = 0 }, store.ErrConflict},
		"creator doesn't exist": {func(p *store.CreateFormUploadGrantParams) { p.CreatedByID = uuid.New() }, store.ErrNotFound},
		"project doesn't exist": {func(p *store.CreateFormUploadGrantParams) { p.ProjectID = uuid.New() }, store.ErrNotFound},
	} {
		p := base
		tc.mutate(&p)
		if _, err := env.store.CreateFormUploadGrant(env.ctx, p); !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestCreateFormClaimsTheUploadsItNames(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	grant := mustCreateGrant(t, env, owner, "claim", 5, time.Hour)
	forQuestion := mustUploadForForm(t, env, owner, &grant, "projects/c/question")
	forOption := mustUploadForForm(t, env, owner, nil, "projects/c/option")
	extra := mustUploadForForm(t, env, owner, &grant, "projects/c/extra")
	left := mustUploadForForm(t, env, owner, &grant, "projects/c/left")

	// The question's own file is also named in attachments, and attachments
	// repeats one: each file counts once.
	form, err := createFormWithMedia(env, owner, mediaDefinition(forQuestion.Ref, forOption.Ref), extra.Number, forQuestion.Number, extra.Number)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	if len(form.Attachments) != 3 {
		t.Fatalf("attachments = %+v, want 3", form.Attachments)
	}
	for i, want := range []model.StorageObject{forQuestion, forOption, extra} {
		got := form.Attachments[i]
		if got.Object.ID != want.ID || got.FormID != form.ID || got.CreatedByID != owner || got.Object.Filename != "image.png" {
			t.Fatalf("attachment %d = %+v, want %s", i, got, want.Ref)
		}
	}
	// Only the file nobody named is still pending.
	if n := pendingUploads(t, env); n != 1 {
		t.Fatalf("pending uploads = %d, want 1", n)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_uploads WHERE storage_object_id = $1`, left.ID); n != 1 {
		t.Fatalf("the unnamed upload is no longer pending")
	}

	// Reading the form again, and its attachments alone, gives the same list.
	byNumber, err := env.store.GetFormByProjectNumber(env.ctx, env.projectID, form.Number)
	if err != nil || len(byNumber.Attachments) != 3 {
		t.Fatalf("GetFormByProjectNumber = %+v, %v", byNumber.Attachments, err)
	}
	listed, err := env.store.ListFormAttachments(env.ctx, form.ID)
	if err != nil || len(listed) != 3 || listed[0].Object.Number != forQuestion.Number || listed[2].Object.Number != extra.Number {
		t.Fatalf("ListFormAttachments = %+v, %v", listed, err)
	}
	one, err := env.store.GetFormAttachmentByObjectNumber(env.ctx, form.ID, forOption.Number)
	if err != nil || one.Object.ID != forOption.ID {
		t.Fatalf("GetFormAttachmentByObjectNumber = %+v, %v", one, err)
	}
	other := mustCreateForm(t, env, owner, "Another form")
	for name, number := range map[string]int{"on another form": forOption.Number, "pending": left.Number, "missing": 999} {
		if _, err := env.store.GetFormAttachmentByObjectNumber(env.ctx, other.ID, number); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if none, err := env.store.ListFormAttachments(env.ctx, other.ID); err != nil || len(none) != 0 {
		t.Fatalf("ListFormAttachments of a form with none = %+v, %v", none, err)
	}

	// A file that is removed drops out of its form.
	if _, err := env.store.DeleteStorageObject(env.ctx, extra.ID); err != nil {
		t.Fatalf("DeleteStorageObject: %v", err)
	}
	if listed, _ := env.store.ListFormAttachments(env.ctx, form.ID); len(listed) != 2 {
		t.Fatalf("attachments after removing one = %+v", listed)
	}
	if _, err := env.store.GetFormAttachmentByObjectNumber(env.ctx, form.ID, extra.Number); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("removed attachment err = %v", err)
	}
}

func TestCreateFormRefusesUploadsItCannotClaim(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	member := mustCreateUser(t, env, "claim-member@example.com")
	if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}

	mine := mustUploadForForm(t, env, owner, nil, "projects/x/mine")
	theirs := mustUploadForForm(t, env, member.ID, nil, "projects/x/theirs")
	claimedElsewhere := mustUploadForForm(t, env, owner, nil, "projects/x/elsewhere")
	if _, err := createFormWithMedia(env, owner, formsTestDefinition(), claimedElsewhere.Number); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	deleted := mustUploadForForm(t, env, owner, nil, "projects/x/deleted")
	if _, err := env.store.DeleteStorageObject(env.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteStorageObject: %v", err)
	}
	generic := mustCreateStorageObject(t, env, "projects/x/generic")
	issue := mustCreateIssue(t, env, "Has a file")
	onIssue := mustCreateStorageObject(t, env, "projects/x/on-issue")
	if _, err := env.store.CreateIssueAttachment(env.ctx, store.CreateIssueAttachmentParams{
		IssueID: issue.ID, StorageObjectID: onIssue.ID, CreatedByID: owner,
	}); err != nil {
		t.Fatalf("CreateIssueAttachment: %v", err)
	}
	otherProject, err := env.store.CreateProjectForUser(env.ctx, owner, uniqueProjectKey(t), "other", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	// Numbers run per project, so give the other project enough objects that
	// its last number is one this project doesn't have.
	var inOtherProject model.StorageObject
	for i := range 20 {
		inOtherProject, err = env.store.CreateFormUpload(env.ctx, store.CreateFormUploadParams{
			Object: storageObjectParams(otherProject.ID, owner, fmt.Sprintf("projects/x/other-project/%d", i)),
		})
		if err != nil {
			t.Fatalf("CreateFormUpload in another project: %v", err)
		}
	}

	before := nextFormNumber(t, env)
	pendingBefore := pendingUploads(t, env)
	for name, tc := range map[string]struct {
		attach []int
		refs   []string
	}{
		"someone else's pending upload": {[]int{theirs.Number}, []string{theirs.Ref}},
		"attached to another form":      {[]int{claimedElsewhere.Number}, []string{claimedElsewhere.Ref}},
		"deleted":                       {[]int{deleted.Number}, []string{deleted.Ref}},
		"a generic project object":      {[]int{generic.Number}, []string{generic.Ref}},
		"an issue attachment":           {[]int{onIssue.Number}, []string{onIssue.Ref}},
		"another project's upload":      {[]int{inOtherProject.Number}, []string{model.StorageObjectRef(inOtherProject.Number)}},
		"missing":                       {[]int{9999}, []string{"object-9999"}},
		// A good upload named beside bad ones is not claimed either.
		"two bad ones with a good one": {[]int{mine.Number, theirs.Number, 9999}, []string{theirs.Ref, "object-9999"}},
	} {
		_, err := createFormWithMedia(env, owner, formsTestDefinition(), tc.attach...)
		var notClaimable *store.FormMediaNotClaimableError
		if !errors.As(err, &notClaimable) || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: err = %v, want a FormMediaNotClaimableError wrapping ErrNotFound", name, err)
		}
		if strings.Join(notClaimable.Refs, ",") != strings.Join(tc.refs, ",") {
			t.Fatalf("%s: refs = %v, want %v", name, notClaimable.Refs, tc.refs)
		}
		for _, ref := range tc.refs {
			if !strings.Contains(err.Error(), ref) || !strings.HasPrefix(err.Error(), "media: ") {
				t.Fatalf("%s: message %q does not name %s", name, err, ref)
			}
		}
		if got := pendingUploads(t, env); got != pendingBefore {
			t.Fatalf("%s: pending uploads = %d, want %d: a refused form claimed some", name, got, pendingBefore)
		}
		if got := nextFormNumber(t, env); got != before {
			t.Fatalf("%s: next form number = %d, want %d: a refused form took one", name, got, before)
		}
	}
	// Media named in the questions is claimed the same way.
	if _, err := createFormWithMedia(env, owner, mediaDefinition(theirs.Ref, mine.Ref)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("question media that isn't the creator's: err = %v", err)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_attachments WHERE project_id = $1`, env.projectID); n != 1 {
		t.Fatalf("attachments = %d, want only the first form's", n)
	}
	// The plural message names every ref.
	_, err = createFormWithMedia(env, owner, formsTestDefinition(), theirs.Number, 9999)
	if err == nil || !strings.Contains(err.Error(), "object-9999") || !strings.Contains(err.Error(), "are not among your uploads") {
		t.Fatalf("plural message = %v", err)
	}
	// The upload that was fine all along is still there to use.
	if _, err := createFormWithMedia(env, owner, formsTestDefinition(), mine.Number); err != nil {
		t.Fatalf("claiming the good upload: %v", err)
	}
}

func TestCreateFormTakesAtMostTwentyFiles(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	numbers := make([]int, 0, model.FormMaxAttachments+1)
	for i := range model.FormMaxAttachments + 1 {
		numbers = append(numbers, mustUploadForForm(t, env, owner, nil, fmt.Sprintf("projects/many/%d", i)).Number)
	}
	_, err := createFormWithMedia(env, owner, formsTestDefinition(), numbers...)
	var formErr *model.FormError
	if !errors.As(err, &formErr) || formErr.Field != "attachments" {
		t.Fatalf("21 files err = %v, want a FormError on attachments", err)
	}
	if n := pendingUploads(t, env); n != model.FormMaxAttachments+1 {
		t.Fatalf("pending uploads = %d, want all %d", n, model.FormMaxAttachments+1)
	}
	if _, err := createFormWithMedia(env, owner, formsTestDefinition(), numbers[:model.FormMaxAttachments]...); err != nil {
		t.Fatalf("20 files: %v", err)
	}
}

func TestFormMediaIsLeftOutOfGenericObjectLookups(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	plain := mustCreateStorageObject(t, env, "projects/v/plain")
	pending := mustUploadForForm(t, env, owner, nil, "projects/v/pending")
	attached := mustUploadForForm(t, env, owner, nil, "projects/v/attached")
	if _, err := createFormWithMedia(env, owner, formsTestDefinition(), attached.Number); err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	for _, hide := range []bool{false, true} {
		for name, object := range map[string]model.StorageObject{"pending": pending, "attached": attached} {
			if _, err := env.store.GetVisibleStorageObjectByProjectNumber(env.ctx, env.projectID, object.Number, hide); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("%s (hide=%v): err = %v, want ErrNotFound", name, hide, err)
			}
		}
		if got, err := env.store.GetVisibleStorageObjectByProjectNumber(env.ctx, env.projectID, plain.Number, hide); err != nil || got.ID != plain.ID {
			t.Fatalf("plain (hide=%v) = %+v, %v", hide, got, err)
		}
		listed, _, err := env.store.ListStorageObjects(env.ctx, store.ListStorageObjectsParams{ProjectID: env.projectID, Limit: 50, HideMembersOnlyIssueObjects: hide})
		if err != nil || len(listed) != 1 || listed[0].ID != plain.ID {
			t.Fatalf("ListStorageObjects (hide=%v) = %+v, %v, want only the plain object", hide, listed, err)
		}
	}
	if _, err := env.store.GetStorageObjectByProjectNumber(env.ctx, env.projectID, pending.Number); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetStorageObjectByProjectNumber of a pending upload err = %v", err)
	}
}

func TestSweepFormUploadsRemovesOnlyUnclaimedOldUploads(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	oldGrant := mustCreateGrant(t, env, owner, "old", 3, time.Hour)
	youngGrant := mustCreateGrant(t, env, owner, "young", 3, time.Hour)
	oldPending := mustUploadForForm(t, env, owner, &oldGrant, "projects/s/old")
	youngPending := mustUploadForForm(t, env, owner, &youngGrant, "projects/s/young")
	claimed := mustUploadForForm(t, env, owner, nil, "projects/s/claimed")
	form, err := createFormWithMedia(env, owner, formsTestDefinition(), claimed.Number)
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	plain := mustCreateStorageObject(t, env, "projects/s/plain")

	age := func(query string, args ...any) {
		t.Helper()
		if _, err := env.pool.Exec(env.ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	age(`UPDATE form_uploads SET created_at = now() - interval '25 hours' WHERE storage_object_id = $1`, oldPending.ID)
	age(`UPDATE form_attachments SET created_at = now() - interval '48 hours' WHERE form_id = $1`, form.ID)
	age(`UPDATE form_upload_grants SET expires_at = now() - interval '25 hours' WHERE id = $1`, oldGrant.ID)
	age(`UPDATE form_upload_grants SET expires_at = now() - interval '1 hour' WHERE id = $1`, youngGrant.ID)

	swept, err := env.store.SweepFormUploads(env.ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("SweepFormUploads: %v", err)
	}
	if swept.Uploads != 1 || swept.Grants != 1 {
		t.Fatalf("swept = %+v, want 1 upload and 1 grant", swept)
	}

	deletedAt := func(id uuid.UUID) *time.Time {
		t.Helper()
		var at *time.Time
		if err := env.pool.QueryRow(env.ctx, `SELECT deleted_at FROM storage_objects WHERE id = $1`, id).Scan(&at); err != nil {
			t.Fatalf("select deleted_at: %v", err)
		}
		return at
	}
	if deletedAt(oldPending.ID) == nil {
		t.Fatal("the old pending upload was not soft-deleted")
	}
	if n := countRows(t, env, `SELECT count(*) FROM storage_object_deletions WHERE storage_object_id = $1 AND status = 'pending'`, oldPending.ID); n != 1 {
		t.Fatalf("deletion jobs queued for the old upload = %d, want 1", n)
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_uploads WHERE storage_object_id = $1`, oldPending.ID); n != 0 {
		t.Fatal("the old upload is still pending")
	}
	for name, id := range map[string]uuid.UUID{"young": youngPending.ID, "claimed": claimed.ID, "plain": plain.ID} {
		if deletedAt(id) != nil {
			t.Fatalf("the %s object was deleted", name)
		}
	}
	if n := countRows(t, env, `SELECT count(*) FROM form_uploads WHERE storage_object_id = $1`, youngPending.ID); n != 1 {
		t.Fatal("the young upload stopped being pending")
	}
	if got, err := env.store.ListFormAttachments(env.ctx, form.ID); err != nil || len(got) != 1 {
		t.Fatalf("claimed attachments = %+v, %v", got, err)
	}
	// The old grant is gone; the one that expired more recently is not.
	if n := countRows(t, env, `SELECT count(*) FROM form_upload_grants`); n != 1 {
		t.Fatalf("grants left = %d, want 1", n)
	}
	// Nothing is left to sweep.
	if again, err := env.store.SweepFormUploads(env.ctx, 24*time.Hour); err != nil || again.Uploads != 0 || again.Grants != 0 {
		t.Fatalf("second sweep = %+v, %v", again, err)
	}
}
