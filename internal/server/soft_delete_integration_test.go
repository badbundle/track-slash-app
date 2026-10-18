package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func TestHTTPSoftDeleteIssue(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	iss := mustHTTPIssue(t, e)

	code, body := e.do(t, http.MethodDelete, e.issuePath(iss), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete issue code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.issuePath(iss), nil)
	if code != http.StatusNotFound {
		t.Fatalf("get deleted issue code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodDelete, e.issuePath(iss), nil)
	if code != http.StatusNotFound {
		t.Fatalf("delete issue second code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"description": "nope"})
	if code != http.StatusNotFound {
		t.Fatalf("patch deleted issue code = %d body = %s", code, body)
	}
}

func TestHTTPRestoreIssue(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	iss := mustHTTPIssue(t, e)

	code, body := e.do(t, http.MethodDelete, e.issuePath(iss), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete issue code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPost, e.issuePath(iss)+"/restore", nil)
	if code != http.StatusOK {
		t.Fatalf("restore issue code = %d body = %s", code, body)
	}
	restored := decode[model.Issue](t, body)
	if restored.ID != iss.ID || restored.Identifier != iss.Identifier {
		t.Fatalf("restored issue = %+v, want %s", restored, iss.Identifier)
	}
	code, body = e.do(t, http.MethodGet, e.issuePath(iss), nil)
	if code != http.StatusOK {
		t.Fatalf("get restored issue code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPost, e.issuePath(iss)+"/restore", nil)
	if code != http.StatusNotFound {
		t.Fatalf("restore issue second code = %d body = %s", code, body)
	}
}

func TestHTTPListDeletedIssues(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	live, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "live issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue live: %v", err)
	}
	deleted, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "deleted issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue deleted: %v", err)
	}
	parent, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "deleted parent",
	})
	if err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	child, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         "deleted child",
	})
	if err != nil {
		t.Fatalf("CreateSubIssue child: %v", err)
	}
	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "other deleted project", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherDeleted, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: otherProject.ID,
		Title:     "other deleted issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue other deleted: %v", err)
	}
	if err := e.store.DeleteIssue(e.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteIssue deleted: %v", err)
	}
	if err := e.store.DeleteIssue(e.ctx, parent.ID); err != nil {
		t.Fatalf("DeleteIssue parent: %v", err)
	}
	if err := e.store.DeleteIssue(e.ctx, otherDeleted.ID); err != nil {
		t.Fatalf("DeleteIssue other: %v", err)
	}

	code, body := e.do(t, http.MethodGet, e.projectIssuesPath()+"/deleted", nil)
	if code != http.StatusOK {
		t.Fatalf("list deleted code = %d body = %s", code, body)
	}
	page := decodePage[model.Issue](t, body)
	wantIDs := []uuid.UUID{deleted.ID, parent.ID}
	if len(page.Items) != len(wantIDs) || page.NextCursor != nil {
		t.Fatalf("deleted page = %+v next=%v, want %d items no cursor", page.Items, page.NextCursor, len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if page.Items[i].ID != wantID {
			t.Fatalf("deleted page item %d = %s, want %s: %+v", i, page.Items[i].ID, wantID, page.Items)
		}
	}
	for _, got := range page.Items {
		if got.ID == live.ID || got.ID == otherDeleted.ID {
			t.Fatalf("deleted page included excluded issue: %+v", got)
		}
	}

	code, body = e.do(t, http.MethodGet, e.projectIssuesPath()+"/deleted?limit=1", nil)
	if code != http.StatusOK {
		t.Fatalf("deleted page 1 code = %d body = %s", code, body)
	}
	page = decodePage[model.Issue](t, body)
	if len(page.Items) != 1 || page.Items[0].ID != deleted.ID || page.NextCursor == nil {
		t.Fatalf("deleted page 1 = %+v next=%v, want deleted issue and cursor", page.Items, page.NextCursor)
	}
	code, body = e.do(t, http.MethodGet, e.projectIssuesPath()+"/deleted?limit=1&cursor="+url.QueryEscape(*page.NextCursor), nil)
	if code != http.StatusOK {
		t.Fatalf("deleted page 2 code = %d body = %s", code, body)
	}
	page = decodePage[model.Issue](t, body)
	if len(page.Items) != 1 || page.Items[0].ID != parent.ID || page.NextCursor != nil {
		t.Fatalf("deleted page 2 = %+v next=%v, want parent only", page.Items, page.NextCursor)
	}

	code, body = e.do(t, http.MethodPost, e.issuePath(child)+"/restore", nil)
	if code != http.StatusOK {
		t.Fatalf("restore unlisted child code = %d body = %s", code, body)
	}
	if restored := decode[model.Issue](t, body); restored.ID != child.ID {
		t.Fatalf("restored unlisted child = %+v, want %s", restored, child.ID)
	}

	code, body = e.do(t, http.MethodGet, e.issuePath(deleted), nil)
	if code != http.StatusNotFound {
		t.Fatalf("get listed deleted issue code = %d body = %s", code, body)
	}
	for _, path := range []string{
		e.projectIssuesPath() + "/deleted?limit=0",
		e.projectIssuesPath() + "/deleted?cursor=not-base64!!!",
	} {
		code, body := e.do(t, http.MethodGet, path, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("GET %s code = %d body = %s", path, code, body)
		}
	}
}

// A sub-issue deleted on its own, while its parent stays live, is listed
// with the deleted issues. One deleted with its parent is not, since
// restoring the parent brings it back.
func TestListDeletedIssuesIncludesSubIssuesOfLiveParents(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "deleted-sub-issues")
	liveParent := mustHTTPIssue(t, e)
	orphan, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: liveParent.ID,
		Title:         "deleted child of live parent",
	})
	if err != nil {
		t.Fatalf("CreateSubIssue orphan: %v", err)
	}
	deletedParent, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "deleted parent"})
	if err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	hidden, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: deletedParent.ID,
		Title:         "deleted child of deleted parent",
	})
	if err != nil {
		t.Fatalf("CreateSubIssue hidden: %v", err)
	}
	if err := e.store.DeleteIssue(e.ctx, orphan.ID); err != nil {
		t.Fatalf("DeleteIssue orphan: %v", err)
	}
	if err := e.store.DeleteIssue(e.ctx, deletedParent.ID); err != nil {
		t.Fatalf("DeleteIssue parent: %v", err)
	}
	wantIDs := []uuid.UUID{orphan.ID, deletedParent.ID}

	code, body := e.do(t, http.MethodGet, e.projectIssuesPath()+"/deleted", nil)
	if code != http.StatusOK {
		t.Fatalf("list deleted code = %d body = %s", code, body)
	}
	page := decodePage[model.Issue](t, body)
	if len(page.Items) != len(wantIDs) || page.Items[0].ID != wantIDs[0] || page.Items[1].ID != wantIDs[1] {
		t.Fatalf("REST deleted = %+v, want orphan then deleted parent", page.Items)
	}

	session := mcpConnect(t, e, e.authToken)
	out := mcpCall(t, e, session, "track_list_deleted_issues", map[string]any{"owner": e.ownerUsername, "key": e.projKey})
	items := decodeMCPField[[]model.Issue](t, out, "items")
	if len(items) != len(wantIDs) || items[0].ID != wantIDs[0] || items[1].ID != wantIDs[1] {
		t.Fatalf("MCP deleted = %+v, want orphan then deleted parent", items)
	}

	ui := e.uiGet(t, e.projectPath()+"/deleted", token)
	for _, want := range []string{orphan.Title, ">Sub-issue</div>", `hx-post="` + e.issuePath(orphan) + `/restore"`, deletedParent.Title} {
		if !strings.Contains(ui, want) {
			t.Fatalf("deleted page missing %q: %s", want, ui)
		}
	}
	if strings.Contains(ui, hidden.Title) {
		t.Fatalf("deleted page listed the deleted parent's child: %s", ui)
	}
}

func TestHTTPSoftDeleteProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	iss := mustHTTPIssue(t, e)
	sp, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "S1",
		StartDate: datePtr(dateHTTP(2026, 6, 1)),
		EndDate:   datePtr(dateHTTP(2026, 6, 14)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}

	code, body := e.do(t, http.MethodDelete, e.projectPath(), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete project code = %d body = %s", code, body)
	}
	for _, path := range []string{
		e.projectPath(),
		e.issuePath(iss),
		e.sprintPath(sp),
	} {
		code, body = e.do(t, http.MethodGet, path, nil)
		if code != http.StatusNotFound {
			t.Fatalf("GET %s code = %d body = %s", path, code, body)
		}
	}
	code, body = e.do(t, http.MethodDelete, e.projectPath(), nil)
	if code != http.StatusNotFound {
		t.Fatalf("delete project second code = %d body = %s", code, body)
	}
}

func TestHTTPSoftDeleteUser(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	u := mustHTTPUser(t, e)

	code, body := e.do(t, http.MethodDelete, "/users/"+u.ID.String(), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete user code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, "/users/"+u.ID.String(), nil)
	if code != http.StatusNotFound {
		t.Fatalf("get deleted user code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodDelete, "/users/"+u.ID.String(), nil)
	if code != http.StatusNotFound {
		t.Fatalf("delete user second code = %d body = %s", code, body)
	}
}

func TestHTTPSoftDeleteSprint(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	planned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "planned",
		StartDate: datePtr(dateHTTP(2026, 7, 1)),
		EndDate:   datePtr(dateHTTP(2026, 7, 14)),
	})
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}
	active, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "active",
		StartDate: datePtr(dateHTTP(2026, 7, 15)),
		EndDate:   datePtr(dateHTTP(2026, 7, 28)),
	})
	if err != nil {
		t.Fatalf("CreateSprint active: %v", err)
	}
	st := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, active.ID, store.UpdateSprintParams{Status: &st}); err != nil {
		t.Fatalf("UpdateSprint active: %v", err)
	}
	completed, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "completed",
		StartDate: datePtr(dateHTTP(2026, 7, 29)),
		EndDate:   datePtr(dateHTTP(2026, 8, 11)),
	})
	if err != nil {
		t.Fatalf("CreateSprint completed: %v", err)
	}

	code, body := e.do(t, http.MethodDelete, e.sprintPath(active), nil)
	if code != http.StatusConflict {
		t.Fatalf("delete active sprint code = %d body = %s", code, body)
	}
	if _, err := e.store.CompleteSprint(e.ctx, active.ID); err != nil {
		t.Fatalf("CompleteSprint active: %v", err)
	}
	if _, err := e.store.UpdateSprint(e.ctx, completed.ID, store.UpdateSprintParams{Status: &st}); err != nil {
		t.Fatalf("UpdateSprint completed active: %v", err)
	}
	if _, err := e.store.CompleteSprint(e.ctx, completed.ID); err != nil {
		t.Fatalf("CompleteSprint completed: %v", err)
	}
	for _, sp := range []model.Sprint{active, completed} {
		code, body = e.do(t, http.MethodDelete, e.sprintPath(sp), nil)
		if code != http.StatusConflict {
			t.Fatalf("delete completed sprint %s code = %d body = %s", sp.ID, code, body)
		}
	}
	code, body = e.do(t, http.MethodDelete, e.sprintPath(planned), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete planned sprint code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.sprintPath(planned), nil)
	if code != http.StatusNotFound {
		t.Fatalf("get deleted sprint code = %d body = %s", code, body)
	}
}

func TestHTTPSoftDeleteBadIDs(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for _, path := range []string{
		"/users/nope",
		"/bad!/projects/" + e.projKey,
		"/" + e.ownerUsername + "/issues/nope",
		e.projectPath() + "/sprints/nope",
	} {
		code, body := e.do(t, http.MethodDelete, path, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("DELETE %s code = %d body = %s", path, code, body)
		}
	}
	code, body := e.do(t, http.MethodPost, "/"+e.ownerUsername+"/issues/nope/restore", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("POST bad restore code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, "/bad!/projects/"+e.projKey+"/issues/deleted", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("GET bad deleted issues code = %d body = %s", code, body)
	}
	for _, path := range []string{
		"/users/" + uuid.New().String(),
		"/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t),
		"/" + e.ownerUsername + "/issues/" + e.projKey + "-999999",
		e.projectPath() + "/sprints/sprint-999999",
	} {
		code, body := e.do(t, http.MethodDelete, path, nil)
		if code != http.StatusNotFound {
			t.Fatalf("DELETE %s code = %d body = %s", path, code, body)
		}
	}
	code, body = e.do(t, http.MethodPost, "/"+e.ownerUsername+"/issues/"+e.projKey+"-999999/restore", nil)
	if code != http.StatusNotFound {
		t.Fatalf("POST unknown restore code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, "/"+e.ownerUsername+"/projects/"+uniqueProjectKey(t)+"/issues/deleted", nil)
	if code != http.StatusNotFound {
		t.Fatalf("GET unknown deleted issues code = %d body = %s", code, body)
	}
}

func TestHTTPOldUUIDObjectRoutesRemoved(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	issue := mustHTTPIssue(t, e)
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Old Route Sprint",
		StartDate: datePtr(dateHTTP(2026, 6, 1)),
		EndDate:   datePtr(dateHTTP(2026, 6, 14)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	comment, err := e.store.CreateComment(e.ctx, store.CreateCommentParams{
		IssueID:  issue.ID,
		AuthorID: e.adminID,
		Body:     "old route comment",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	target, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "old route target",
	})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	link, err := e.store.CreateIssueLink(e.ctx, store.CreateIssueLinkParams{
		SourceID: issue.ID,
		TargetID: target.ID,
		LinkType: model.LinkTypeBlocks,
	})
	if err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}

	for _, path := range []string{
		"/projects/" + e.projectID.String(),
		"/issues/" + issue.ID.String(),
		"/sprints/" + sprint.ID.String(),
		"/comments/" + comment.ID.String(),
		"/issue-links/" + link.ID.String(),
	} {
		code, body := e.do(t, http.MethodGet, path, nil)
		if code != http.StatusNotFound {
			t.Fatalf("GET %s code = %d body = %s", path, code, body)
		}
	}
}

func dateHTTP(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
