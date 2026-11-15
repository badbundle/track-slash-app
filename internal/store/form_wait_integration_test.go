package store_test

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func waitKeyHash(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// mustCreateWaitableForm creates a form its creator answers themselves, with
// a wait key.
func mustCreateWaitableForm(t *testing.T, env *sprintsTestEnv, creatorID uuid.UUID, key string) model.Form {
	t.Helper()
	form, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
		ProjectID:   env.projectID,
		Title:       "Waitable " + key,
		Definition:  formsTestDefinition(),
		AssigneeID:  creatorID,
		CreatedByID: creatorID,
		WaitKeyHash: waitKeyHash(key),
	})
	if err != nil {
		t.Fatalf("CreateForm with wait key %q: %v", key, err)
	}
	return form
}

func mustFormWaitStatus(t *testing.T, env *sprintsTestEnv, key string) model.FormStatus {
	t.Helper()
	wait, err := env.store.GetFormWait(env.ctx, waitKeyHash(key))
	if err != nil {
		t.Fatalf("GetFormWait %q: %v", key, err)
	}
	return wait.Status
}

func TestGetFormWaitFollowsTheFormsStatus(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	form := mustCreateWaitableForm(t, env, owner, "key-one")
	other := mustCreateWaitableForm(t, env, owner, "key-two")
	plain := mustCreateForm(t, env, owner, "No wait key")

	wait, err := env.store.GetFormWait(env.ctx, waitKeyHash("key-one"))
	if err != nil {
		t.Fatalf("GetFormWait: %v", err)
	}
	if wait.FormID != form.ID || wait.Status != model.FormStatusOpen {
		t.Fatalf("wait = %+v, want %s open", wait, form.ID)
	}

	mustSubmitForm(t, env, mustTickRotated(t, env, form))
	if got := mustFormWaitStatus(t, env, "key-one"); got != model.FormStatusSubmitted {
		t.Fatalf("after submit = %s", got)
	}
	// One form's key says nothing about another.
	if got := mustFormWaitStatus(t, env, "key-two"); got != model.FormStatusOpen {
		t.Fatalf("other form = %s, want open", got)
	}
	if _, err := env.store.ReopenForm(env.ctx, form.ID, owner); err != nil {
		t.Fatalf("ReopenForm: %v", err)
	}
	if got := mustFormWaitStatus(t, env, "key-one"); got != model.FormStatusOpen {
		t.Fatalf("after reopen = %s", got)
	}
	if _, err := env.store.CancelForm(env.ctx, other.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
	if got := mustFormWaitStatus(t, env, "key-two"); got != model.FormStatusCancelled {
		t.Fatalf("after cancel = %s", got)
	}

	// A key nobody was given, and no key at all, find nothing. The form
	// created without a key can't be reached by an empty one.
	for name, hash := range map[string][]byte{
		"unknown key": waitKeyHash("never issued"),
		"nil hash":    nil,
		"empty hash":  {},
	} {
		if _, err := env.store.GetFormWait(env.ctx, hash); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if reloaded, err := env.store.GetForm(env.ctx, plain.ID); err != nil || reloaded.Status != model.FormStatusOpen {
		t.Fatalf("plain form = %+v, %v", reloaded, err)
	}
}

func TestGetFormWaitStopsWhenItsCreatorCannotReadTheForm(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	member := mustCreateUser(t, env, "wait-member@example.com")
	blocked := mustCreateUser(t, env, "wait-blocked@example.com")
	removed := mustCreateUser(t, env, "wait-removed@example.com")
	deleted := mustCreateUser(t, env, "wait-deleted@example.com")
	for _, u := range []model.User{member, blocked, removed, deleted} {
		if _, err := env.store.GrantProjectAccess(env.ctx, env.projectID, u.ID); err != nil {
			t.Fatalf("GrantProjectAccess: %v", err)
		}
		mustCreateWaitableForm(t, env, u.ID, u.Email)
	}
	mustCreateWaitableForm(t, env, owner, "owner")

	found := func(key string) bool {
		t.Helper()
		_, err := env.store.GetFormWait(env.ctx, waitKeyHash(key))
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetFormWait %q: %v", key, err)
		}
		return err == nil
	}
	for _, key := range []string{member.Email, blocked.Email, removed.Email, deleted.Email, "owner"} {
		if !found(key) {
			t.Fatalf("%s: wait key not found before anything changed", key)
		}
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
	for _, key := range []string{blocked.Email, removed.Email, deleted.Email} {
		if found(key) {
			t.Fatalf("%s: wait key still works after its creator lost the project", key)
		}
	}
	for _, key := range []string{member.Email, "owner"} {
		if !found(key) {
			t.Fatalf("%s: wait key stopped working for someone who is still a member", key)
		}
	}

	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	for _, key := range []string{member.Email, "owner"} {
		if found(key) {
			t.Fatalf("%s: wait key still works after the project was deleted", key)
		}
	}
}

func TestCreateFormRefusesAWaitKeyHashItCannotKeep(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	owner := formsEnvOwner(t, env)
	mustCreateWaitableForm(t, env, owner, "taken")

	for name, hash := range map[string][]byte{
		"already another form's": waitKeyHash("taken"),
		"not a SHA-256":          []byte("short"),
		"empty":                  {},
	} {
		_, err := env.store.CreateForm(env.ctx, store.CreateFormParams{
			ProjectID:   env.projectID,
			Title:       "Second",
			Definition:  formsTestDefinition(),
			AssigneeID:  owner,
			CreatedByID: owner,
			WaitKeyHash: hash,
		})
		if !errors.Is(err, store.ErrConflict) {
			t.Fatalf("%s: err = %v, want ErrConflict", name, err)
		}
	}

	// A refused form takes no number: the next one follows the first.
	next := mustCreateForm(t, env, owner, "Next")
	if next.Ref != "form-2" {
		t.Fatalf("next ref = %s, want form-2", next.Ref)
	}
}
