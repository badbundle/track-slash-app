package server_test

import (
	"errors"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestUIAddEditAndRemoveIssueLinks(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-links")
	targetDue, err := model.ParseDate("2099-06-24")
	if err != nil {
		t.Fatalf("ParseDate target: %v", err)
	}
	replacementDue, err := model.ParseDate("2099-06-26")
	if err != nil {
		t.Fatalf("ParseDate replacement: %v", err)
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "link ui source"})
	if err != nil {
		t.Fatalf("CreateIssue source: %v", err)
	}
	target, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "link ui target", DueDate: &targetDue})
	if err != nil {
		t.Fatalf("CreateIssue target: %v", err)
	}
	replacement, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "link ui replacement", DueDate: &replacementDue})
	if err != nil {
		t.Fatalf("CreateIssue replacement: %v", err)
	}
	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "other link ui", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}

	edit := e.uiGet(t, e.issueLinksPath(issue)+"/new", token)
	for _, want := range []string{
		"link ui source",
		`id="issue-link-create" data-client-modal class="fixed inset-0 z-50 grid`,
		`role="dialog" aria-modal="true" aria-labelledby="issue-link-create-title"`,
		`method="post" action="` + e.issueLinksPath(issue) + `"`,
		`hx-post="` + e.issueLinksPath(issue) + `"`,
		`name="relation" class=`,
		`value="relates_to" selected`,
		`value="blocked_by"`,
		`name="target_issue" value="" placeholder="` + e.projKey + `-12"`,
		`aria-label="Cancel adding issue link"`,
		`>Add link</button>`,
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("add link response missing %q: %s", want, edit)
		}
	}
	if strings.Contains(edit, "No linked issues.") {
		t.Fatalf("add link response should replace empty state with form: %s", edit)
	}

	empty := url.Values{"relation": {"relates_to"}, "target_issue": {"   "}}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.issueLinksPath(issue), token, strings.NewReader(empty.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("empty target code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Linked issue required.") {
		t.Fatalf("empty target response missing validation error: %s", body)
	}
	if !strings.Contains(body, `id="issue-link-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("invalid issue link should keep modal open: %s", body)
	}

	badRelation := url.Values{"relation": {"blocks_by_magic"}, "target_issue": {target.Identifier}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueLinksPath(issue), token, strings.NewReader(badRelation.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bad relation code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Choose a valid relationship.") || !strings.Contains(body, `value="`+target.Identifier+`"`) {
		t.Fatalf("bad relation response missing preserved form state: %s", body)
	}

	crossProject := url.Values{"relation": {"relates_to"}, "target_issue": {otherProject.Key + "-999999"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueLinksPath(issue), token, strings.NewReader(crossProject.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("cross-project target code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Linked issue must be in this project.") || strings.Contains(body, "Linked issue not found.") {
		t.Fatalf("cross-project target response missing project validation: %s", body)
	}

	form := url.Values{"relation": {"blocked_by"}, "target_issue": {target.Identifier}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueLinksPath(issue), token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("create link code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Blocked by") || !strings.Contains(body, "link ui target") || !strings.Contains(body, "Jun 24") || strings.Contains(body, `name="target_issue"`) {
		t.Fatalf("create link response did not return read mode: %s", body)
	}
	links, _, err := e.store.ListIssueLinksForIssue(e.ctx, store.ListIssueLinksForIssueParams{IssueID: issue.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListIssueLinksForIssue: %v", err)
	}
	if len(links) != 1 || links[0].SourceID != target.ID || links[0].TargetID != issue.ID || links[0].LinkType != model.LinkTypeBlocks {
		t.Fatalf("links = %+v, want target blocks issue", links)
	}
	link := links[0]

	edit = e.uiGet(t, e.issueLinksPath(issue)+"/"+link.Ref+"/edit", token)
	for _, want := range []string{
		`method="post" action="` + e.issueLinksPath(issue) + `/` + link.Ref + `"`,
		`hx-post="` + e.issueLinksPath(issue) + `/` + link.Ref + `"`,
		`value="blocked_by" selected`,
		`name="target_issue" value="` + target.Identifier + `"`,
		`aria-label="Cancel editing link"`,
		`aria-label="Remove link"`,
		`hx-post="` + e.issueLinksPath(issue) + `/` + link.Ref + `/delete"`,
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("edit link response missing %q: %s", want, edit)
		}
	}

	update := url.Values{"relation": {"clones"}, "target_issue": {replacement.Identifier}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueLinksPath(issue)+"/"+link.Ref, token, strings.NewReader(update.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update link code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Clones") || !strings.Contains(body, "link ui replacement") || !strings.Contains(body, "Jun 26") || strings.Contains(body, "link ui target") {
		t.Fatalf("update link response missing updated read mode: %s", body)
	}
	updated, err := e.store.GetIssueLink(e.ctx, link.ID)
	if err != nil {
		t.Fatalf("GetIssueLink after update: %v", err)
	}
	if updated.SourceID != issue.ID || updated.TargetID != replacement.ID || updated.LinkType != model.LinkTypeClones || updated.Ref != link.Ref {
		t.Fatalf("updated link = %+v, want issue clones replacement with same ref %s", updated, link.Ref)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueLinksPath(issue)+"/"+link.Ref+"/delete", token, nil)
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete link code = %d body = %s", res.StatusCode, body)
	}
	for _, want := range []string{
		">Linked issues</h2>",
		`aria-label="Linked issue progress: no linked issues"`,
		`aria-label="Add link"`,
		"w-full sm:w-auto sm:flex-1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("delete link response missing empty link state %q: %s", want, body)
		}
	}
	for _, notWant := range []string{"No linked issues.", "link ui replacement"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("delete link response included stale linked issue state %q: %s", notWant, body)
		}
	}
	if _, err := e.store.GetIssueLink(e.ctx, link.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetIssueLink after delete err = %v, want ErrNotFound", err)
	}
}

func TestUICreateCommentPostsAndRerendersIssuePanel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-comment")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "comment target issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	form := url.Values{"body": {"new ui comment"}}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.issueCommentsPath(issue), token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("code = %d body = %s", res.StatusCode, body)
	}
	for _, want := range []string{"comment target issue", "new ui comment", `placeholder="Write a comment"`, `data-modal-open="issue-comment-create"`, `id="issue-comment-create" data-client-modal class="fixed inset-0 z-50 hidden`} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment post response missing %q: %s", want, body)
		}
	}
	composerStart := strings.Index(body, `placeholder="Write a comment"`)
	firstCommentStart := strings.Index(body, "new ui comment")
	if composerStart < 0 || firstCommentStart < 0 || composerStart > firstCommentStart {
		t.Fatalf("comment composer should render above comments: %s", body)
	}
	comments, _, err := e.store.ListCommentsForIssue(e.ctx, store.ListCommentsForIssueParams{IssueID: issue.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListCommentsForIssue: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "new ui comment" || comments[0].AuthorID != user.ID {
		t.Fatalf("comments = %+v, want one new comment by %s", comments, user.ID)
	}

	second := url.Values{"body": {"second ui comment with **markdown** <script>alert(1)</script>"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueCommentsPath(issue), token, strings.NewReader(second.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("second code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "<strong>markdown</strong>") || strings.Contains(body, "**markdown**") {
		t.Fatalf("comment should render as Markdown: %s", body)
	}
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("comment should not render raw HTML: %s", body)
	}
	secondCommentStart := strings.Index(body, "second ui comment")
	firstCommentStart = strings.Index(body, "new ui comment")
	composerStart = strings.Index(body, `placeholder="Write a comment"`)
	if composerStart < 0 || secondCommentStart < 0 || firstCommentStart < 0 || composerStart > secondCommentStart || secondCommentStart > firstCommentStart {
		t.Fatalf("comments should render newest-first below the composer: %s", body)
	}
	comments, _, err = e.store.ListCommentsForIssue(e.ctx, store.ListCommentsForIssueParams{IssueID: issue.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListCommentsForIssue after second comment: %v", err)
	}
	if len(comments) != 2 || comments[0].Body != "new ui comment" || comments[1].Body != "second ui comment with **markdown** <script>alert(1)</script>" {
		t.Fatalf("store comments = %+v, want API/store default oldest-first", comments)
	}

	empty := url.Values{"body": {"   "}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueCommentsPath(issue), token, strings.NewReader(empty.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("empty code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Comment required, max 10000 chars.") {
		t.Fatalf("empty comment response missing validation error: %s", body)
	}
	if !strings.Contains(body, `id="issue-comment-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("invalid comment should keep modal open: %s", body)
	}
	comments, _, err = e.store.ListCommentsForIssue(e.ctx, store.ListCommentsForIssueParams{IssueID: issue.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListCommentsForIssue after validation: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("empty comment should not create a row, comments = %+v", comments)
	}
}

func TestUIEditCommentAuthorOnly(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	author, authorToken := e.mustProjectMemberToken(t, "ui-comment-author")
	_, otherToken := e.mustProjectMemberToken(t, "ui-comment-other")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "editable comment issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	comment, err := e.store.CreateComment(e.ctx, store.CreateCommentParams{
		IssueID:  issue.ID,
		AuthorID: author.ID,
		Body:     "original ui comment",
	})
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	commentPath := e.issueCommentsPath(issue) + "/" + comment.Ref
	editPath := commentPath + "/edit"

	body := e.uiGet(t, e.issuePath(issue), authorToken)
	for _, want := range []string{
		"original ui comment",
		`aria-label="Edit comment"`,
		`hx-get="` + editPath + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("author issue body missing %q: %s", want, body)
		}
	}
	otherBody := e.uiGet(t, e.issuePath(issue), otherToken)
	for _, notWant := range []string{`aria-label="Edit comment"`, `hx-get="` + editPath + `"`} {
		if strings.Contains(otherBody, notWant) {
			t.Fatalf("non-author issue body included edit control %q: %s", notWant, otherBody)
		}
	}

	edit := e.uiGet(t, editPath, authorToken)
	for _, want := range []string{
		`method="post" action="` + commentPath + `"`,
		`hx-post="` + commentPath + `"`,
		`<textarea name="body" rows="2"`,
		"original ui comment",
		`aria-label="Save comment"`,
		`aria-label="Cancel editing comment"`,
		"⌘ + Enter to save",
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("edit response missing %q: %s", want, edit)
		}
	}

	empty := url.Values{"body": {"   "}}
	res := e.uiDoNoRedirect(t, http.MethodPost, commentPath, authorToken, strings.NewReader(empty.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("empty edit code = %d body = %s", res.StatusCode, body)
	}
	for _, want := range []string{"Comment required, max 10000 chars.", `aria-label="Save comment"`, ">   </textarea>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty edit response missing %q: %s", want, body)
		}
	}

	form := url.Values{"body": {"edited ui comment"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, commentPath, authorToken, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("edit code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "edited ui comment") || !strings.Contains(body, ">•</span>") || !strings.Contains(body, "Edited ") || strings.Contains(body, "original ui comment") || strings.Contains(body, `aria-label="Save comment"`) {
		t.Fatalf("edit response did not return edited read mode: %s", body)
	}
	updated, err := e.store.GetComment(e.ctx, comment.ID)
	if err != nil {
		t.Fatalf("GetComment after edit: %v", err)
	}
	if updated.Body != "edited ui comment" || updated.EditedAt == nil {
		t.Fatalf("updated comment = %+v, want edited body and edited_at", updated)
	}

	res = e.uiDoNoRedirect(t, http.MethodGet, editPath, otherToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-author edit GET code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, commentPath, otherToken, strings.NewReader(url.Values{"body": {"not yours"}}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("non-author edit POST code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	got, err := e.store.GetComment(e.ctx, comment.ID)
	if err != nil {
		t.Fatalf("GetComment after non-author edit: %v", err)
	}
	if got.Body != "edited ui comment" {
		t.Fatalf("non-author changed body to %q", got.Body)
	}
}

func TestUICreateSubIssuePostsTitleOnlyAndRerendersIssuePanel(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sub-issue")
	parent, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "parent target issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	edit := e.uiGet(t, e.issueSubIssuesPath(parent)+"/new", token)
	for _, want := range []string{
		"parent target issue",
		`id="issue-sub-issue-create" data-client-modal class="fixed inset-0 z-50 grid`,
		`role="dialog" aria-modal="true" aria-labelledby="issue-sub-issue-create-title"`,
		`aria-label="Cancel adding sub-issue"`,
		`method="post" action="` + e.issueSubIssuesPath(parent) + `"`,
		`hx-post="` + e.issueSubIssuesPath(parent) + `"`,
		`name="title" value="" autofocus placeholder="Issue title"`,
		`>Create sub-issue</button>`,
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("sub-issue add response missing %q: %s", want, edit)
		}
	}
	if !strings.Contains(edit, `data-modal-return="issue-sub-issue-create"`) || !strings.Contains(edit, `hx-get="`+e.issueSubIssuesPath(parent)+`/new"`) {
		t.Fatalf("sub-issue modal response should retain its trigger: %s", edit)
	}

	form := url.Values{"title": {"new child from ui"}}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.issueSubIssuesPath(parent), token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("code = %d body = %s", res.StatusCode, body)
	}
	for _, want := range []string{"parent target issue", "Sub-issues", "new child from ui", `aria-label="Add sub-issue"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("sub-issue post response missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `aria-label="Create sub-issue"`) || strings.Contains(body, `name="title"`) {
		t.Fatalf("successful sub-issue post should close the composer: %s", body)
	}
	children, _, err := e.store.ListSubIssuesForIssue(e.ctx, store.ListSubIssuesForIssueParams{
		ParentIssueID: parent.ID,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("ListSubIssuesForIssue: %v", err)
	}
	if len(children) != 1 || children[0].Title != "new child from ui" || children[0].Description != "" || children[0].Priority != model.PriorityP2 {
		t.Fatalf("children = %+v, want one title-only P2 child", children)
	}
	childDue, err := model.ParseDate("2099-06-25")
	if err != nil {
		t.Fatalf("ParseDate child due: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, children[0].ID, store.UpdateIssueParams{DueDate: &childDue}); err != nil {
		t.Fatalf("UpdateIssue child due date: %v", err)
	}
	body = e.uiGet(t, e.issuePath(parent), token)
	if !strings.Contains(body, "new child from ui") || !strings.Contains(body, "Jun 25") {
		t.Fatalf("parent issue panel missing sub-issue due date: %s", body)
	}

	empty := url.Values{"title": {"   "}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issueSubIssuesPath(parent), token, strings.NewReader(empty.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("empty code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, "Title required, max 200 chars.") {
		t.Fatalf("empty sub-issue response missing validation error: %s", body)
	}
	if !strings.Contains(body, `id="issue-sub-issue-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("invalid sub-issue should keep modal open: %s", body)
	}
	for _, want := range []string{
		`aria-label="Cancel adding sub-issue"`,
		`method="post" action="` + e.issueSubIssuesPath(parent) + `"`,
		`name="title" value="   " autofocus placeholder="Issue title"`,
		`>Create sub-issue</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty sub-issue response should keep composer open with %q: %s", want, body)
		}
	}
	formIndex := strings.Index(body, `name="title" value="   "`)
	childIndex := strings.Index(body, "new child from ui")
	if formIndex < 0 || childIndex < 0 || formIndex > childIndex {
		t.Fatalf("empty sub-issue response should render composer before rows: %s", body)
	}
}
