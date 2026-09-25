package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestUIUserAvatarFallbackAndImageRendering(t *testing.T) {
	t.Parallel()

	userID := uuid.MustParse("23f14acb-6a57-4035-a046-33e93ffbd5bb")
	thumbID := uuid.MustParse("6a0d51f8-4a4f-46d5-8de1-726a7823d8f4")
	avatar := uiUserAvatar(model.User{
		ID:                            userID,
		Username:                      "ada",
		Email:                         "ada@example.com",
		Name:                          "Ada Lovelace",
		ProfileImageThumbnailObjectID: &thumbID,
	}, "avatar-class")
	if avatar.Label != "Ada Lovelace" || avatar.Initials != "AL" || avatar.Class != "avatar-class" {
		t.Fatalf("avatar = %+v", avatar)
	}
	wantURL := "/users/" + userID.String() + "/profile-image/thumbnail/content?v=" + thumbID.String()
	if avatar.ThumbnailURL != wantURL {
		t.Fatalf("ThumbnailURL = %q, want %q", avatar.ThumbnailURL, wantURL)
	}

	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "user-avatar", avatar); err != nil {
		t.Fatalf("ExecuteTemplate image avatar: %v", err)
	}
	body := buf.String()
	for _, want := range []string{`aria-label="Ada Lovelace"`, `title="Ada Lovelace"`, `class="avatar-class overflow-hidden rounded-full"`, `src="` + wantURL + `"`, `loading="lazy"`, `class="h-full w-full rounded-full object-cover"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("image avatar missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, ">AL</span>") {
		t.Fatalf("image avatar rendered initials fallback: %s", body)
	}

	buf.Reset()
	if err := uiTemplates.ExecuteTemplate(&buf, "user-avatar", uiUserAvatar(model.User{ID: userID, Username: "ada", Name: "Ada Lovelace"}, "avatar-class")); err != nil {
		t.Fatalf("ExecuteTemplate fallback avatar: %v", err)
	}
	body = buf.String()
	if !strings.Contains(body, "AL") || !strings.Contains(body, `class="avatar-class overflow-hidden rounded-full"`) || strings.Contains(body, "<img") {
		t.Fatalf("fallback avatar body = %s", body)
	}
}

func TestUIProfileImageControls(t *testing.T) {
	t.Parallel()

	userID := uuid.MustParse("23f14acb-6a57-4035-a046-33e93ffbd5bb")
	thumbID := uuid.MustParse("6a0d51f8-4a4f-46d5-8de1-726a7823d8f4")
	data := uiProfilePanelData{
		User: model.User{
			ID:                            userID,
			Username:                      "ada",
			Email:                         "ada@example.com",
			Name:                          "Ada Lovelace",
			ProfileImageThumbnailObjectID: &thumbID,
		},
	}
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "profile-panel", data); err != nil {
		t.Fatalf("ExecuteTemplate profile with image: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`data-modal-open="profile-image-picker"`,
		`aria-haspopup="dialog"`,
		`id="profile-image-picker" data-client-modal class="fixed inset-0 z-50 hidden`,
		`role="dialog" aria-modal="true" aria-labelledby="profile-image-picker-title"`,
		`Change image`,
		`action="/settings/profile-image"`,
		`enctype="multipart/form-data"`,
		`name="file" type="file"`,
		`accept="image/png,image/jpeg,image/gif,image/webp,image/bmp"`,
		`data-lucide="image-up"`,
		`action="/settings/profile-image/delete"`,
		`Remove current image`,
		`/users/` + userID.String() + `/profile-image/thumbnail/content?v=` + thumbID.String(),
		`rounded-full`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("profile image state missing %q: %s", want, body)
		}
	}

	buf.Reset()
	data.User.ProfileImageThumbnailObjectID = nil
	if err := uiTemplates.ExecuteTemplate(&buf, "profile-panel", data); err != nil {
		t.Fatalf("ExecuteTemplate profile without image: %v", err)
	}
	body = buf.String()
	if !strings.Contains(body, "AL") || !strings.Contains(body, "Add image") || strings.Contains(body, `action="/settings/profile-image/delete"`) || strings.Contains(body, "<img") {
		t.Fatalf("profile fallback state = %s", body)
	}
}
