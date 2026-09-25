package server_test

import (
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"strings"
	"testing"
	"time"
)

func TestUIRendersIssueDetailPage(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-issue")
	reporterID := user.ID
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:   e.projectID,
		Title:       "detail page issue",
		Description: "read only body",
		AssigneeID:  &user.ID,
		ReporterID:  &reporterID,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	sp, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Detail Planned Sprint",
		StartDate: datePtr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign sprint: %v", err)
	}
	tag, err := e.store.CreateIssueTag(e.ctx, store.CreateIssueTagParams{ProjectID: e.projectID, Name: "UI", Color: model.TagColorBlue})
	if err != nil {
		t.Fatalf("CreateIssueTag: %v", err)
	}
	if _, err := e.store.CreateIssueTagLink(e.ctx, store.CreateIssueTagLinkParams{IssueID: issue.ID, TagID: tag.ID}); err != nil {
		t.Fatalf("CreateIssueTagLink: %v", err)
	}
	linked, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "linked detail issue"})
	if err != nil {
		t.Fatalf("CreateIssue linked: %v", err)
	}
	link, err := e.store.CreateIssueLink(e.ctx, store.CreateIssueLinkParams{
		SourceID: issue.ID,
		TargetID: linked.ID,
		LinkType: model.LinkTypeBlocks,
	})
	if err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	if _, err := e.store.CreateComment(e.ctx, store.CreateCommentParams{
		IssueID:  issue.ID,
		AuthorID: user.ID,
		Body:     "detail comment body",
	}); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	subIssue, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: issue.ID,
		Title:         "detail child issue",
		AssigneeID:    &user.ID,
		ReporterID:    &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateSubIssue: %v", err)
	}
	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "Other Detail Project", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, otherProject.ID, user.ID); err != nil {
		t.Fatalf("GrantProjectAccess other: %v", err)
	}
	otherIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: otherProject.ID, Title: "unrelated detail issue"})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	if _, err := e.store.CreateComment(e.ctx, store.CreateCommentParams{
		IssueID:  otherIssue.ID,
		AuthorID: user.ID,
		Body:     "unrelated comment body",
	}); err != nil {
		t.Fatalf("CreateComment other: %v", err)
	}

	body := e.uiGet(t, e.issuePath(issue), token)
	for _, want := range []string{
		"detail page issue",
		"read only body",
		"Detail Planned Sprint",
		"#UI",
		user.Name,
		"Blocks",
		"linked detail issue",
		"Sub-issues",
		"detail child issue",
		"detail comment body",
		`aria-label="Issue actions"`,
		`data-lucide="more-horizontal"`,
		`aria-label="Edit description"`,
		`hx-get="` + e.issuePath(issue) + `/description/edit"`,
		`aria-label="Edit link"`,
		`aria-label="Edit comment"`,
		`aria-label="Change status"`,
		`aria-expanded="false"`,
		`hx-get="` + e.issuePath(issue) + `/status/edit"`,
		`aria-label="Edit assignee"`,
		`hx-get="` + e.issuePath(issue) + `/assignee/edit"`,
		`aria-label="Edit reporter"`,
		`hx-get="` + e.issuePath(issue) + `/reporter/edit"`,
		`aria-label="Edit sprint"`,
		`hx-get="` + e.issuePath(issue) + `/sprint/edit"`,
		`aria-label="Add link"`,
		`hx-get="` + e.issueLinksPath(issue) + `/new"`,
		`aria-label="Add sub-issue"`,
		`hx-get="` + e.issueSubIssuesPath(issue) + `/new"`,
		`hx-get="` + e.issueLinksPath(issue) + `/` + link.Ref + `/edit"`,
		`data-modal-open="issue-comment-create"`,
		`id="issue-comment-create" data-client-modal class="fixed inset-0 z-50 hidden`,
		`role="dialog" aria-modal="true" aria-labelledby="issue-comment-create-title"`,
		`aria-haspopup="listbox"`,
		`data-lucide="chevron-down"`,
		`placeholder="Write a comment"`,
		`method="post" action="` + e.issueCommentsPath(issue) + `"`,
		`hx-post="` + e.issueCommentsPath(issue) + `"`,
		`data-submit-shortcut="meta-enter"`,
		`method="post" action="` + e.issuePath(issue) + `/delete"`,
		`hx-post="` + e.issuePath(issue) + `/delete"`,
		`hx-push-url="` + e.projectPath() + `/planned"`,
		`hx-confirm="Delete this issue? You can undo it from the next screen."`,
		`Delete issue`,
		`data-lucide="trash-2"`,
		`aria-label="Edit title"`,
		`hx-get="` + e.issuePath(issue) + `/title/edit"`,
		`aria-label="Manage tags"`,
		`hx-get="` + e.issuePath(issue) + `/tags"`,
		`href="` + e.projectPath() + `/planned"`,
		`hx-get="` + e.projectPath() + `/planned/panel"`,
		`hx-push-url="` + e.projectPath() + `/planned"`,
		`data-lucide="corner-up-left"`,
		`text-rose-600`,
		`href="` + e.issuePath(linked) + `"`,
		`hx-get="` + e.issuePath(linked) + `/panel"`,
		`href="` + e.issuePath(subIssue) + `"`,
		`hx-get="` + e.issuePath(subIssue) + `/panel"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("issue body missing %q: %s", want, body)
		}
	}
	mainStart := strings.Index(body, `<main id="main"`)
	if mainStart < 0 {
		t.Fatalf("issue body missing main content: %s", body)
	}
	titleHeaderStartOffset := strings.Index(body[mainStart:], "<header")
	if titleHeaderStartOffset < 0 {
		t.Fatalf("issue body missing title header: %s", body)
	}
	titleHeaderStart := mainStart + titleHeaderStartOffset
	titleHeaderEndOffset := strings.Index(body[titleHeaderStart:], "</header>")
	if titleHeaderEndOffset < 0 {
		t.Fatalf("issue body missing title header end: %s", body)
	}
	titleHeader := body[titleHeaderStart : titleHeaderStart+titleHeaderEndOffset]
	if !strings.Contains(titleHeader, "#UI") ||
		!strings.Contains(titleHeader, `aria-label="Manage tags"`) ||
		!strings.Contains(titleHeader, "Detail Planned Sprint") {
		t.Fatalf("title card should render issue tags, tag manager action, and sprint title: %s", body)
	}
	for _, notWant := range []string{"Edit issue", "Change status", "Edit description", "Edit status", "To do", "In progress", "Done"} {
		if strings.Contains(titleHeader, notWant) {
			t.Fatalf("title card still contains section action/status %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{
		`title="Issue actions"`,
		`title="Edit description"`,
		`title="Add link"`,
		`title="Edit link"`,
		`title="Edit comment"`,
		`title="Change status"`,
		`title="Edit status"`,
		`title="Edit assignee"`,
		`title="Edit reporter"`,
		`title="Edit sprint"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue body still renders native title tooltip %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{`href="` + e.projectPath() + `/all"`, `hx-get="` + e.projectPath() + `/all/panel"`} {
		if strings.Contains(titleHeader, notWant) {
			t.Fatalf("issue title card included stale back target markup %q: %s", notWant, body)
		}
	}
	if strings.Contains(body, `>Tags</dt>`) {
		t.Fatalf("issue body included stale tag detail row: %s", body)
	}
	for _, notWant := range []string{`aria-label="Edit status"`, ">Status</dt>"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue body still renders separate status edit affordance %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{`/archive`, `Archive issue`, `data-lucide="archive"`, `data-lucide="settings"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue body included removed archive control %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{`<textarea disabled`, `aria-label="Post comment" class="grid h-9 w-9 shrink-0 cursor-not-allowed`, "\n            Comment\n"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue body renders disabled or text-labeled composer %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{
		`method="post" action="` + e.issueSubIssuesPath(issue) + `"`,
		`hx-post="` + e.issueSubIssuesPath(issue) + `"`,
		`aria-label="Create sub-issue"`,
		`aria-label="Cancel adding sub-issue"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue body should not render sub-issue composer by default %q: %s", notWant, body)
		}
	}
	if strings.Contains(body, `name="description"`) ||
		strings.Contains(body, `placeholder="Describe the issue: context, steps to reproduce, acceptance criteria… Markdown is supported."`) ||
		strings.Contains(body, `name="priority"`) ||
		strings.Contains(body, `aria-label="Sub-issue priority"`) {
		t.Fatalf("sub-issue composer should be title-only: %s", body)
	}
	if strings.Contains(body, `aria-label="Save description"`) || strings.Contains(body, `<textarea name="description"`) {
		t.Fatalf("issue detail should not start in description edit mode: %s", body)
	}
	if strings.Contains(body, `role="listbox" aria-label="Issue status"`) || strings.Contains(body, `aria-label="Cancel status change"`) {
		t.Fatalf("issue detail should not start in status edit mode: %s", body)
	}
	for _, notWant := range []string{"unrelated detail issue", "unrelated comment body", "Other Detail Project"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue body included unrelated content %q: %s", notWant, body)
		}
	}

	panel := e.uiGet(t, e.issuePath(issue)+"/panel", token)
	if strings.Contains(panel, "<!doctype html>") {
		t.Fatalf("panel returned shell: %s", panel)
	}
	if !strings.Contains(panel, "detail page issue") || !strings.Contains(panel, "detail comment body") {
		t.Fatalf("panel missing issue context: %s", panel)
	}
}

func TestUIRendersSubIssueDetailWithParentBacklinkAndNoSprintControls(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-sub-detail")
	parent, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "parent detail issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	child, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         "child detail issue",
		Description:   "child detail body",
		AssigneeID:    &user.ID,
		ReporterID:    &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateSubIssue: %v", err)
	}

	body := e.uiGet(t, e.issuePath(child), token)
	for _, want := range []string{
		`aria-label="Breadcrumb"`,
		parent.Identifier,
		child.Identifier,
		"child detail issue",
		"child detail body",
		"Sub-issue of",
		"Parent",
		"parent detail issue",
		`href="` + e.issuePath(parent) + `"`,
		`hx-get="` + e.issuePath(parent) + `/panel"`,
		`data-lucide="corner-up-left"`,
		user.Name,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sub-issue detail missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{
		"Sub-issues",
		`action="` + e.issueSubIssuesPath(child) + `"`,
		`aria-label="Add sub-issue"`,
		`aria-label="Create sub-issue"`,
		`aria-label="Edit sprint"`,
		">Sprint</dt>",
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("sub-issue detail included %q: %s", notWant, body)
		}
	}
}
