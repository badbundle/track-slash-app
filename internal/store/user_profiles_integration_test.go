package store_test

import (
	"testing"

	"github.com/google/uuid"
)

func TestListUserProfilesByID(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)

	suffix := uniqueProjectKey(t)
	zed, err := env.store.CreateUserProfile(env.ctx, "zed-"+suffix, "zed-"+suffix+"@example.com", "Zed")
	if err != nil {
		t.Fatalf("CreateUserProfile zed: %v", err)
	}
	amy, err := env.store.CreateUserProfile(env.ctx, "amy-"+suffix, "amy-"+suffix+"@example.com", "Amy")
	if err != nil {
		t.Fatalf("CreateUserProfile amy: %v", err)
	}
	original := mustCreateUserProfileObject(t, env, amy.ID, "original")
	thumbnail := mustCreateUserProfileObject(t, env, amy.ID, "thumbnail")
	if _, err := env.store.ReplaceUserProfileImage(env.ctx, amy.ID, original.ID, thumbnail.ID); err != nil {
		t.Fatalf("ReplaceUserProfileImage: %v", err)
	}
	gone, err := env.store.CreateUserProfile(env.ctx, "gone-"+suffix, "gone-"+suffix+"@example.com", "Gone")
	if err != nil {
		t.Fatalf("CreateUserProfile gone: %v", err)
	}
	if err := env.store.DeleteUser(env.ctx, gone.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	admin, err := env.store.GetUser(env.ctx, project.OwnerID)
	if err != nil {
		t.Fatalf("GetUser admin: %v", err)
	}
	if !admin.IsAdmin || admin.Email == "" {
		t.Fatalf("fixture admin = %+v, want an admin with an email", admin)
	}

	if users, err := env.store.ListUserProfilesByID(env.ctx, nil); err != nil || users != nil {
		t.Fatalf("ListUserProfilesByID(nil) = %+v, %v; want nil, nil", users, err)
	}

	users, err := env.store.ListUserProfilesByID(env.ctx, []uuid.UUID{zed.ID, gone.ID, uuid.New(), amy.ID, admin.ID, zed.ID})
	if err != nil {
		t.Fatalf("ListUserProfilesByID: %v", err)
	}
	// Ordered by username ("amy-" < "owner-" < "zed-"); the deleted and
	// unknown ids are skipped and the duplicate collapses.
	want := []uuid.UUID{amy.ID, admin.ID, zed.ID}
	if len(users) != len(want) {
		t.Fatalf("ListUserProfilesByID returned %d users, want %d: %+v", len(users), len(want), users)
	}
	for i, u := range users {
		if u.ID != want[i] {
			t.Fatalf("users[%d] = %s (%s), want %s", i, u.ID, u.Username, want[i])
		}
		if u.Email != "" || u.IsAdmin {
			t.Fatalf("users[%d] exposed private fields: email=%q admin=%v", i, u.Email, u.IsAdmin)
		}
	}
	if users[0].Name != "Amy" || users[0].Username != amy.Username {
		t.Fatalf("amy profile = %+v, want name and username", users[0])
	}
	if users[0].ProfileImageThumbnailObjectID == nil || *users[0].ProfileImageThumbnailObjectID != thumbnail.ID {
		t.Fatalf("amy thumbnail = %v, want %s", users[0].ProfileImageThumbnailObjectID, thumbnail.ID)
	}
	if users[2].ProfileImageThumbnailObjectID != nil {
		t.Fatalf("zed thumbnail = %v, want nil", users[2].ProfileImageThumbnailObjectID)
	}
}
