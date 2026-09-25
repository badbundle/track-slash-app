package server

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

func TestUIProjectsPanelOwnerAvatar(t *testing.T) {
	t.Parallel()

	ownerID := uuid.New()
	thumbnailID := uuid.New()
	panel := &uiProjectsPanelData{Owners: map[uuid.UUID]model.User{
		ownerID: {ID: ownerID, Username: "badbundle", Name: "Bad Bundle", ProfileImageThumbnailObjectID: &thumbnailID},
	}}

	known := panel.OwnerAvatar(model.Project{OwnerID: ownerID, OwnerUsername: "badbundle"})
	if known.Label != "Bad Bundle" || known.Initials != "BB" {
		t.Fatalf("known owner avatar = %+v, want the owner's name and initials", known)
	}
	if !strings.Contains(known.ThumbnailURL, thumbnailID.String()) {
		t.Fatalf("known owner thumbnail = %q, want %s", known.ThumbnailURL, thumbnailID)
	}
	if known.Class != uiProjectOwnerAvatarClass {
		t.Fatalf("known owner class = %q", known.Class)
	}

	// An owner the profile lookup did not return (deleted since the list was
	// read) still gets an initials avatar from the project's owner username.
	missingID := uuid.New()
	missing := panel.OwnerAvatar(model.Project{OwnerID: missingID, OwnerUsername: "someone"})
	if missing.Label != "@someone" || missing.ThumbnailURL != "" || missing.ID != missingID {
		t.Fatalf("missing owner avatar = %+v, want username fallback without thumbnail", missing)
	}
}
