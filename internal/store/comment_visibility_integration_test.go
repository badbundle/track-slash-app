package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestCommentVisibility(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	owner, err := env.store.GetUser(env.ctx, project.OwnerID)
	if err != nil {
		t.Fatalf("GetUser owner: %v", err)
	}
	member := mustPushProjectMember(t, env, "visible-member")
	readonly := mustPushProjectMember(t, env, "visible-readonly")
	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	outsiderName := "visible-outsider" + strings.ToLower(uniqueProjectKey(t))
	outsider, err := env.store.CreateUserProfile(env.ctx, outsiderName, outsiderName+"@example.com", "Outsider")
	if err != nil {
		t.Fatalf("CreateUserProfile outsider: %v", err)
	}

	// A private project's comments default to shared, as they always were.
	privateIssue := mustCreateIssue(t, env, "private comments")
	if c := mustCreateComment(t, env, privateIssue.ID, owner.ID, "private default"); c.Visibility != model.CommentVisibilityShared {
		t.Fatalf("private default visibility = %q, want shared", c.Visibility)
	}

	if _, err := env.store.UpdateProjectAccessMode(env.ctx, env.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	for name, tc := range map[string]struct {
		user model.User
		want bool
	}{
		"owner":     {owner, true},
		"member":    {member, true},
		"readonly":  {readonly, true},
		"outsider":  {outsider, false},
		"anonymous": {model.User{}, false},
	} {
		permissions, err := env.store.ProjectPermissionsForUser(env.ctx, tc.user, env.projectID)
		if err != nil || !permissions.CanRead || permissions.CanReadMembersOnly != tc.want {
			t.Fatalf("%s permissions = %+v, %v; want CanReadMembersOnly %v", name, permissions, err, tc.want)
		}
	}

	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Public report", ReporterID: &outsider.ID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	shared := mustCreateComment(t, env, issue.ID, owner.ID, "shared with everyone")
	if shared.Visibility != model.CommentVisibilityShared {
		t.Fatalf("public default visibility = %q, want shared", shared.Visibility)
	}
	private, err := env.store.CreateComment(store.WithActor(env.ctx, owner.ID), store.CreateCommentParams{
		IssueID: issue.ID, AuthorID: owner.ID, Body: "members only: @" + outsider.Username + " @" + readonly.Username,
		Visibility: model.CommentVisibilityMembers,
	})
	if err != nil || private.Visibility != model.CommentVisibilityMembers {
		t.Fatalf("CreateComment members = %+v, %v", private, err)
	}
	if got, err := env.store.GetCommentForIssueByNumber(env.ctx, issue.ID, private.Number); err != nil || got.Visibility != model.CommentVisibilityMembers {
		t.Fatalf("GetCommentForIssueByNumber = %+v, %v", got, err)
	}
	if _, err := env.store.CreateComment(env.ctx, store.CreateCommentParams{IssueID: issue.ID, AuthorID: owner.ID, Body: "x", Visibility: "public"}); !errors.Is(err, store.ErrInvalidCommentVisibility) {
		t.Fatalf("CreateComment invalid visibility err = %v", err)
	}

	listed := func(include bool) []uuid.UUID {
		t.Helper()
		comments, _, err := env.store.ListCommentsForIssue(env.ctx, store.ListCommentsForIssueParams{IssueID: issue.ID, Limit: 10, IncludeMembersOnly: include})
		if err != nil {
			t.Fatalf("ListCommentsForIssue: %v", err)
		}
		ids := make([]uuid.UUID, 0, len(comments))
		for _, c := range comments {
			ids = append(ids, c.ID)
		}
		return ids
	}
	if got := listed(false); len(got) != 1 || got[0] != shared.ID {
		t.Fatalf("shared-only comments = %v, want only %s", got, shared.ID)
	}
	if got := listed(true); len(got) != 2 {
		t.Fatalf("all comments = %v, want 2", got)
	}

	// Changelog entries about a members-only comment follow it.
	changelogFor := func(include bool, commentID uuid.UUID) []model.ProjectChangelogEntry {
		t.Helper()
		entries, _, err := env.store.ListProjectChangelog(env.ctx, store.ListProjectChangelogParams{ProjectID: env.projectID, Limit: 100, IncludeMembersOnly: include})
		if err != nil {
			t.Fatalf("ListProjectChangelog: %v", err)
		}
		var out []model.ProjectChangelogEntry
		for _, entry := range entries {
			if entry.Entity == "comment" && entry.EntityID == commentID {
				out = append(out, entry)
			}
		}
		return out
	}
	if got := changelogFor(false, private.ID); len(got) != 0 {
		t.Fatalf("public changelog shows members-only comment: %+v", got)
	}
	if got := changelogFor(true, private.ID); len(got) != 1 || !got[0].MembersOnly || got[0].Details.Preview == "" {
		t.Fatalf("members changelog = %+v", got)
	}
	if got := changelogFor(false, shared.ID); len(got) != 1 || got[0].MembersOnly {
		t.Fatalf("shared comment changelog = %+v", got)
	}

	// Sharing it only changes visibility: it is not an edit, and its earlier
	// changelog entries become visible with it.
	sharedVisibility := model.CommentVisibilityShared
	opened, err := env.store.UpdateComment(env.ctx, store.UpdateCommentParams{ID: private.ID, AuthorID: owner.ID, Body: private.Body, Visibility: &sharedVisibility})
	if err != nil || opened.Visibility != model.CommentVisibilityShared || opened.EditedAt != nil || opened.Body != private.Body {
		t.Fatalf("share comment = %+v, %v", opened, err)
	}
	entries := changelogFor(false, private.ID)
	if len(entries) != 2 {
		t.Fatalf("shared comment changelog = %+v", entries)
	}
	if change := entries[0]; !strings.HasPrefix(change.Summary, "Changed comment visibility") || len(change.Details.Changes) != 1 ||
		change.Details.Changes[0] != (model.ProjectChangelogChange{Field: "visibility", Label: "Visibility", From: "Members only", To: "Shared"}) {
		t.Fatalf("visibility change entry = %+v", change)
	}
	if got := listed(false); len(got) != 2 {
		t.Fatalf("shared comments after sharing = %v", got)
	}

	// Keeping it to members again, with a new body, hides every entry again.
	membersVisibility := model.CommentVisibilityMembers
	closed, err := env.store.UpdateComment(env.ctx, store.UpdateCommentParams{ID: private.ID, AuthorID: owner.ID, Body: "members only, edited", Visibility: &membersVisibility})
	if err != nil || closed.Visibility != model.CommentVisibilityMembers || closed.EditedAt == nil {
		t.Fatalf("hide comment = %+v, %v", closed, err)
	}
	if got := changelogFor(false, private.ID); len(got) != 0 {
		t.Fatalf("public changelog after hiding = %+v", got)
	}
	entries = changelogFor(true, private.ID)
	if len(entries) != 3 || !strings.HasPrefix(entries[0].Summary, "Edited comment") || len(entries[0].Details.Changes) != 2 {
		t.Fatalf("members changelog after hiding = %+v", entries)
	}
	// Omitting the visibility keeps it.
	if kept, err := env.store.UpdateComment(env.ctx, store.UpdateCommentParams{ID: private.ID, AuthorID: owner.ID, Body: "members only, again"}); err != nil || kept.Visibility != model.CommentVisibilityMembers {
		t.Fatalf("update keeping visibility = %+v, %v", kept, err)
	}
	invalid := model.CommentVisibility("everyone")
	if _, err := env.store.UpdateComment(env.ctx, store.UpdateCommentParams{ID: private.ID, AuthorID: owner.ID, Body: "x", Visibility: &invalid}); !errors.Is(err, store.ErrInvalidCommentVisibility) {
		t.Fatalf("UpdateComment invalid visibility err = %v", err)
	}

	// Deleting a members-only comment logs a members-only entry.
	if err := env.store.DeleteComment(env.ctx, store.DeleteCommentParams{ID: private.ID, AuthorID: owner.ID}); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if got := changelogFor(false, private.ID); len(got) != 0 {
		t.Fatalf("public changelog after delete = %+v", got)
	}
	if got := changelogFor(true, private.ID); len(got) != 5 || got[0].Op != "delete" || !got[0].MembersOnly {
		t.Fatalf("members changelog after delete = %+v", got)
	}

	// Deleting a shared comment keeps its earlier previews to members too.
	if _, err := env.store.UpdateComment(env.ctx, store.UpdateCommentParams{ID: shared.ID, AuthorID: owner.ID, Body: "shared, edited"}); err != nil {
		t.Fatalf("UpdateComment shared: %v", err)
	}
	if got := changelogFor(false, shared.ID); len(got) != 2 {
		t.Fatalf("public changelog before deleting shared comment = %+v", got)
	}
	if err := env.store.DeleteComment(env.ctx, store.DeleteCommentParams{ID: shared.ID, AuthorID: owner.ID}); err != nil {
		t.Fatalf("DeleteComment shared: %v", err)
	}
	if got := changelogFor(false, shared.ID); len(got) != 0 {
		t.Fatalf("public changelog after deleting shared comment = %+v", got)
	}
	entries = changelogFor(true, shared.ID)
	if len(entries) != 3 || entries[0].Op != "delete" || !entries[1].MembersOnly || !entries[2].MembersOnly {
		t.Fatalf("members changelog after deleting shared comment = %+v", entries)
	}

	// A help desk keeps new comments to members unless shared on purpose.
	if _, err := env.pool.Exec(env.ctx, `UPDATE projects SET access_mode = 'helpdesk' WHERE id = $1`, env.projectID); err != nil {
		t.Fatalf("set helpdesk: %v", err)
	}
	if c := mustCreateComment(t, env, issue.ID, owner.ID, "help desk default"); c.Visibility != model.CommentVisibilityMembers {
		t.Fatalf("help desk default visibility = %q, want members", c.Visibility)
	}
}

func TestPushNotificationsSkipMembersOnlyCommentsForNonMembers(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	project, err := env.store.GetProject(env.ctx, env.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if _, err := env.store.UpdateProjectAccessMode(env.ctx, env.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	readonly := mustPushProjectMember(t, env, "pushreadonly")
	if _, err := env.store.SetProjectMemberRole(env.ctx, env.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	outsiderName := "pushoutsider" + strings.ToLower(uniqueProjectKey(t))
	outsider, err := env.store.CreateUserProfile(env.ctx, outsiderName, outsiderName+"@example.com", "Outsider")
	if err != nil {
		t.Fatalf("CreateUserProfile outsider: %v", err)
	}
	preferences := model.PushNotificationPreferences{Mentions: true, Assignments: true, Comments: true}
	for _, user := range []model.User{readonly, outsider} {
		mustUpsertPushSubscription(t, env, user.ID, "https://push.example.test/"+uuid.NewString())
		if _, err := env.store.UpdatePushNotificationPreferences(env.ctx, user.ID, preferences); err != nil {
			t.Fatalf("UpdatePushNotificationPreferences: %v", err)
		}
	}
	issue, err := env.store.CreateIssue(env.ctx, store.CreateIssueParams{ProjectID: env.projectID, Title: "Public report", ReporterID: &outsider.ID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	mustMaterializePushEvents(t, env)
	mustCompletePushDeliveries(t, env, mustClaimPushDeliveries(t, env))

	authorized := func(body string, visibility model.CommentVisibility) map[uuid.UUID]bool {
		t.Helper()
		if _, err := env.store.CreateComment(store.WithActor(env.ctx, project.OwnerID), store.CreateCommentParams{
			IssueID: issue.ID, AuthorID: project.OwnerID, Body: body, Visibility: visibility,
		}); err != nil {
			t.Fatalf("CreateComment: %v", err)
		}
		mustMaterializePushEvents(t, env)
		deliveries := mustClaimPushDeliveries(t, env)
		out := map[uuid.UUID]bool{}
		for _, delivery := range deliveries {
			_, ok, err := env.store.PreparePushNotificationDelivery(env.ctx, delivery)
			if err != nil {
				t.Fatalf("PreparePushNotificationDelivery: %v", err)
			}
			out[delivery.UserID] = ok
		}
		mustCompletePushDeliveries(t, env, deliveries)
		return out
	}
	// The reporter hears about a shared comment, and a mentioned member about
	// a members-only one, but the reporter is never told of a members-only
	// comment, even one that mentions them.
	if got := authorized("Shared update", model.CommentVisibilityShared); !got[outsider.ID] {
		t.Fatalf("shared comment deliveries = %+v", got)
	}
	got := authorized("Internal: @"+outsider.Username+" @"+readonly.Username, model.CommentVisibilityMembers)
	if got[outsider.ID] || !got[readonly.ID] {
		t.Fatalf("members-only comment deliveries = %+v", got)
	}
}
