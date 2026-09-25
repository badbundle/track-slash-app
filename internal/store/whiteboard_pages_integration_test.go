package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func mustCreateWhiteboardPage(t *testing.T, env *sprintsTestEnv, ownerID uuid.UUID, title, body string) model.WhiteboardPage {
	t.Helper()
	page, err := env.store.CreateWhiteboardPage(store.WithActor(env.ctx, ownerID), store.CreateWhiteboardPageParams{
		ProjectID:   env.projectID,
		Title:       title,
		Body:        body,
		CreatedByID: ownerID,
	})
	if err != nil {
		t.Fatalf("CreateWhiteboardPage %q: %v", title, err)
	}
	return page
}

func whiteboardChangelog(t *testing.T, env *sprintsTestEnv) []model.ProjectChangelogEntry {
	t.Helper()
	entries, _, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{ProjectID: env.projectID, Limit: 50})
	if err != nil {
		t.Fatalf("ListProjectChangelog: %v", err)
	}
	var out []model.ProjectChangelogEntry
	for _, entry := range entries {
		if entry.Entity == "whiteboard_page" {
			out = append(out, entry)
		}
	}
	return out
}

func TestWhiteboardPageCRUDOrderingAndChangelog(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	owner := project.OwnerID

	first := mustCreateWhiteboardPage(t, env, owner, "Ideas", "Try a **bolder** onboarding.")
	second := mustCreateWhiteboardPage(t, env, owner, "Scratch", "")
	if first.Ref != "whiteboard-1" || first.Number != 1 || second.Ref != "whiteboard-2" || second.Number != 2 {
		t.Fatalf("refs = %s/%d %s/%d, want whiteboard-1 and whiteboard-2", first.Ref, first.Number, second.Ref, second.Number)
	}
	if first.ProjectID != env.projectID || first.CreatedByID != owner || first.UpdatedByID != owner || first.Body != "Try a **bolder** onboarding." {
		t.Fatalf("created page = %+v", first)
	}
	if second.Body != "" {
		t.Fatalf("empty body page = %+v, want an empty body", second)
	}

	got, err := env.store.GetWhiteboardPageByProjectNumber(env.ctx, env.projectID, first.Number)
	if err != nil {
		t.Fatalf("GetWhiteboardPageByProjectNumber: %v", err)
	}
	if got.ID != first.ID || got.Ref != first.Ref || got.Body != first.Body {
		t.Fatalf("got page = %+v, want %+v", got, first)
	}
	if _, err := env.store.GetWhiteboardPageByProjectNumber(env.ctx, env.projectID, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing page err = %v, want ErrNotFound", err)
	}
	if projectID, err := env.store.ProjectIDForWhiteboardPage(env.ctx, first.ID); err != nil || projectID != env.projectID {
		t.Fatalf("ProjectIDForWhiteboardPage = %s, %v", projectID, err)
	}

	// Most recently updated first, paged with a keyset cursor.
	page1, more, err := env.store.ListWhiteboardPages(env.ctx, store.ListWhiteboardPagesParams{ProjectID: env.projectID, Limit: 1})
	if err != nil {
		t.Fatalf("ListWhiteboardPages page1: %v", err)
	}
	if len(page1) != 1 || !more || page1[0].ID != second.ID || page1[0].Ref != second.Ref || page1[0].Title != "Scratch" {
		t.Fatalf("page1 = %+v more=%v, want newest page first", page1, more)
	}
	page2, more, err := env.store.ListWhiteboardPages(env.ctx, store.ListWhiteboardPagesParams{
		ProjectID: env.projectID,
		Cursor:    &store.WhiteboardPagesCursor{UpdatedAt: page1[0].UpdatedAt, ID: page1[0].ID},
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("ListWhiteboardPages page2: %v", err)
	}
	if len(page2) != 1 || more || page2[0].ID != first.ID {
		t.Fatalf("page2 = %+v more=%v, want the older page", page2, more)
	}

	// Editing an older page moves it to the top.
	newBody := "Try a much bolder onboarding."
	updated, err := env.store.UpdateWhiteboardPage(store.WithActor(env.ctx, owner), store.UpdateWhiteboardPageParams{
		ID: first.ID, Body: &newBody, UpdatedByID: owner,
	})
	if err != nil {
		t.Fatalf("UpdateWhiteboardPage body: %v", err)
	}
	if updated.Body != newBody || updated.Title != "Ideas" || !updated.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("updated page = %+v", updated)
	}
	ordered, _, err := env.store.ListWhiteboardPages(env.ctx, store.ListWhiteboardPagesParams{ProjectID: env.projectID, Limit: 10})
	if err != nil || len(ordered) != 2 || ordered[0].ID != first.ID || ordered[1].ID != second.ID {
		t.Fatalf("ordered after edit = %+v err=%v, want edited page first", ordered, err)
	}

	renamed := "Onboarding ideas"
	updated, err = env.store.UpdateWhiteboardPage(store.WithActor(env.ctx, owner), store.UpdateWhiteboardPageParams{
		ID: first.ID, Title: &renamed, Body: &newBody, UpdatedByID: owner,
	})
	if err != nil {
		t.Fatalf("UpdateWhiteboardPage title: %v", err)
	}
	if updated.Title != renamed || updated.Body != newBody {
		t.Fatalf("renamed page = %+v", updated)
	}

	// A save that changes nothing keeps the page, its order, and the changelog.
	unchanged, err := env.store.UpdateWhiteboardPage(store.WithActor(env.ctx, owner), store.UpdateWhiteboardPageParams{
		ID: second.ID, Title: &second.Title, Body: &second.Body, UpdatedByID: owner,
	})
	if err != nil {
		t.Fatalf("UpdateWhiteboardPage no-op: %v", err)
	}
	if !unchanged.UpdatedAt.Equal(second.UpdatedAt) || unchanged.Title != second.Title {
		t.Fatalf("no-op update = %+v, want %+v", unchanged, second)
	}
	if _, err := env.store.UpdateWhiteboardPage(env.ctx, store.UpdateWhiteboardPageParams{ID: second.ID, UpdatedByID: owner}); err != nil {
		t.Fatalf("UpdateWhiteboardPage empty: %v", err)
	}

	if err := env.store.DeleteWhiteboardPage(store.WithActor(env.ctx, owner), second.ID); err != nil {
		t.Fatalf("DeleteWhiteboardPage: %v", err)
	}
	if err := env.store.DeleteWhiteboardPage(env.ctx, second.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}

	entries := whiteboardChangelog(t, env)
	var summaries []string
	for _, entry := range entries {
		summaries = append(summaries, entry.Op+": "+entry.Summary)
		if entry.Actor == nil || entry.Actor.ID != owner {
			t.Fatalf("changelog actor = %+v, want owner", entry.Actor)
		}
	}
	want := []string{
		"delete: Deleted whiteboard page Scratch",
		"update: Updated whiteboard page Onboarding ideas",
		"update: Updated whiteboard page Ideas",
		"insert: Created whiteboard page Scratch",
		"insert: Created whiteboard page Ideas",
	}
	if strings.Join(summaries, "\n") != strings.Join(want, "\n") {
		t.Fatalf("whiteboard changelog =\n%s\nwant\n%s", strings.Join(summaries, "\n"), strings.Join(want, "\n"))
	}
	if entries[0].TargetRef != second.Ref || entries[0].TargetTitle != "Scratch" || entries[0].EntityID != second.ID {
		t.Fatalf("delete entry = %+v", entries[0])
	}
	if changes := entries[1].Details.Changes; len(changes) != 1 || changes[0] != (model.ProjectChangelogChange{Field: "title", Label: "Title", From: "Ideas", To: renamed}) {
		t.Fatalf("rename changes = %+v", changes)
	}
	if changes := entries[2].Details.Changes; len(changes) != 1 || changes[0].Field != "body" || changes[0].From != "Try a **bolder** onboarding." || changes[0].To != newBody {
		t.Fatalf("body changes = %+v", changes)
	}
	if entries[4].Details.Preview != "Try a **bolder** onboarding." || entries[4].TargetRef != "whiteboard-1" {
		t.Fatalf("create entry = %+v", entries[4])
	}
}

func TestWhiteboardPageSoftDeleteHidesPageAndKeepsRef(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	page := mustCreateWhiteboardPage(t, env, project.OwnerID, "Throwaway", "Gone soon.")
	if err := env.store.DeleteWhiteboardPage(env.ctx, page.ID); err != nil {
		t.Fatalf("DeleteWhiteboardPage: %v", err)
	}

	var deletedAt *string
	if err := env.pool.QueryRow(env.ctx, `SELECT deleted_at::text FROM whiteboard_pages WHERE id = $1`, page.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("select soft-deleted row: %v", err)
	}
	if deletedAt == nil {
		t.Fatal("deleted page row has no deleted_at, want a soft delete")
	}
	if _, err := env.store.GetWhiteboardPageByProjectNumber(env.ctx, env.projectID, page.Number); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get deleted page err = %v, want ErrNotFound", err)
	}
	pages, _, err := env.store.ListWhiteboardPages(env.ctx, store.ListWhiteboardPagesParams{ProjectID: env.projectID, Limit: 10})
	if err != nil || len(pages) != 0 {
		t.Fatalf("list after delete = %+v err=%v, want empty", pages, err)
	}
	title := "Revived"
	if _, err := env.store.UpdateWhiteboardPage(env.ctx, store.UpdateWhiteboardPageParams{ID: page.ID, Title: &title, UpdatedByID: project.OwnerID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update deleted page err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.ProjectIDForWhiteboardPage(env.ctx, page.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ProjectIDForWhiteboardPage deleted err = %v, want ErrNotFound", err)
	}

	next := mustCreateWhiteboardPage(t, env, project.OwnerID, "Next", "")
	if next.Ref != "whiteboard-2" {
		t.Fatalf("next ref = %s, want whiteboard-2 so deleted refs are never reused", next.Ref)
	}
}

func TestWhiteboardPagesHiddenWithDeletedProject(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	page := mustCreateWhiteboardPage(t, env, project.OwnerID, "Plans", "")
	if err := env.store.DeleteProject(env.ctx, env.projectID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if _, err := env.store.GetWhiteboardPageByProjectNumber(env.ctx, env.projectID, page.Number); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get page of deleted project err = %v, want ErrNotFound", err)
	}
	if _, _, err := env.store.ListWhiteboardPages(env.ctx, store.ListWhiteboardPagesParams{ProjectID: env.projectID, Limit: 10}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("list pages of deleted project err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.CreateWhiteboardPage(env.ctx, store.CreateWhiteboardPageParams{ProjectID: env.projectID, Title: "Late", CreatedByID: project.OwnerID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("create page in deleted project err = %v, want ErrNotFound", err)
	}
	title := "Late edit"
	if _, err := env.store.UpdateWhiteboardPage(env.ctx, store.UpdateWhiteboardPageParams{ID: page.ID, Title: &title, UpdatedByID: project.OwnerID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update page of deleted project err = %v, want ErrNotFound", err)
	}
	if err := env.store.DeleteWhiteboardPage(env.ctx, page.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete page of deleted project err = %v, want ErrNotFound", err)
	}
	if _, err := env.store.ProjectIDForWhiteboardPage(env.ctx, page.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ProjectIDForWhiteboardPage of deleted project err = %v, want ErrNotFound", err)
	}
}

func TestWhiteboardPageWriteErrorsMapToSentinels(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	owner := project.OwnerID

	for _, tc := range []struct {
		name   string
		params store.CreateWhiteboardPageParams
		want   error
	}{
		{name: "missing project", params: store.CreateWhiteboardPageParams{ProjectID: uuid.New(), Title: "Nope", CreatedByID: owner}, want: store.ErrNotFound},
		{name: "unknown author", params: store.CreateWhiteboardPageParams{ProjectID: env.projectID, Title: "Nope", CreatedByID: uuid.New()}, want: store.ErrNotFound},
		{name: "empty title", params: store.CreateWhiteboardPageParams{ProjectID: env.projectID, Title: "", CreatedByID: owner}, want: store.ErrConflict},
		{name: "long title", params: store.CreateWhiteboardPageParams{ProjectID: env.projectID, Title: strings.Repeat("t", 201), CreatedByID: owner}, want: store.ErrConflict},
		{name: "long body", params: store.CreateWhiteboardPageParams{ProjectID: env.projectID, Title: "Big", Body: strings.Repeat("b", 100001), CreatedByID: owner}, want: store.ErrConflict},
	} {
		t.Run("create "+tc.name, func(t *testing.T) {
			if _, err := env.store.CreateWhiteboardPage(env.ctx, tc.params); !errors.Is(err, tc.want) {
				t.Fatalf("CreateWhiteboardPage err = %v, want %v", err, tc.want)
			}
		})
	}

	page := mustCreateWhiteboardPage(t, env, owner, "Valid", "")
	longTitle := strings.Repeat("t", 201)
	emptyTitle := ""
	body := "changed"
	for _, tc := range []struct {
		name   string
		params store.UpdateWhiteboardPageParams
		want   error
	}{
		{name: "missing page", params: store.UpdateWhiteboardPageParams{ID: uuid.New(), Title: &body, UpdatedByID: owner}, want: store.ErrNotFound},
		{name: "unknown editor", params: store.UpdateWhiteboardPageParams{ID: page.ID, Body: &body, UpdatedByID: uuid.New()}, want: store.ErrNotFound},
		{name: "empty title", params: store.UpdateWhiteboardPageParams{ID: page.ID, Title: &emptyTitle, UpdatedByID: owner}, want: store.ErrConflict},
		{name: "long title", params: store.UpdateWhiteboardPageParams{ID: page.ID, Title: &longTitle, UpdatedByID: owner}, want: store.ErrConflict},
	} {
		t.Run("update "+tc.name, func(t *testing.T) {
			if _, err := env.store.UpdateWhiteboardPage(env.ctx, tc.params); !errors.Is(err, tc.want) {
				t.Fatalf("UpdateWhiteboardPage err = %v, want %v", err, tc.want)
			}
		})
	}
	if err := env.store.DeleteWhiteboardPage(env.ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete missing page err = %v, want ErrNotFound", err)
	}
	if _, _, err := env.store.ListWhiteboardPages(env.ctx, store.ListWhiteboardPagesParams{ProjectID: uuid.New(), Limit: 10}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("list missing project err = %v, want ErrNotFound", err)
	}
}

func TestWhiteboardPagesStayInTheirProject(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	other, err := env.store.CreateProjectForUser(env.ctx, project.OwnerID, uniqueProjectKey(t), "other whiteboard", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser other: %v", err)
	}
	mustCreateWhiteboardPage(t, env, project.OwnerID, "Mine", "")
	otherPage, err := env.store.CreateWhiteboardPage(env.ctx, store.CreateWhiteboardPageParams{ProjectID: other.ID, Title: "Theirs", CreatedByID: project.OwnerID})
	if err != nil {
		t.Fatalf("CreateWhiteboardPage other: %v", err)
	}
	if otherPage.Ref != "whiteboard-1" {
		t.Fatalf("other project ref = %s, want its own whiteboard-1", otherPage.Ref)
	}
	pages, _, err := env.store.ListWhiteboardPages(env.ctx, store.ListWhiteboardPagesParams{ProjectID: env.projectID, Limit: 10})
	if err != nil || len(pages) != 1 || pages[0].Title != "Mine" {
		t.Fatalf("project pages = %+v err=%v, want only this project's page", pages, err)
	}
	got, err := env.store.GetWhiteboardPageByProjectNumber(env.ctx, other.ID, 1)
	if err != nil || got.ID != otherPage.ID {
		t.Fatalf("other project page = %+v err=%v", got, err)
	}
}
