package server

import (
	"bytes"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestUIIssuePanelRendersReadonlyDetail(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	linkedID := uuid.MustParse("ae77b9b8-9dcf-4a18-8b69-42b97bd4a4b5")
	userID := uuid.MustParse("23f14acb-6a57-4035-a046-33e93ffbd5bb")
	commentID := uuid.MustParse("d0c74b63-c75c-42b0-b899-6baf6948e3fd")
	linkID := uuid.MustParse("48c98f2e-bad8-4054-89d7-5a45a68af54f")
	when := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	dueDate, err := model.ParseDate("2099-06-24")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	assignee := model.User{ID: userID, Name: "Ada Lovelace", Email: "ada@example.com"}
	reporter := model.User{ID: userID, Name: "Ada Lovelace", Email: "ada@example.com"}
	sprint := model.Sprint{ID: uuid.MustParse("d7fc0dbf-845c-41b4-84ab-89f487cc4a08"), ProjectID: projectID, Name: "Planned One", Status: model.SprintStatusPlanned}
	tags := []model.IssueTag{{
		ID:          uuid.MustParse("746e026d-d7dd-4de3-a039-9070b287cf0b"),
		ProjectID:   projectID,
		Name:        "Customer Beta",
		DisplayName: "#Customer Beta",
		Color:       model.TagColorBlue,
	}}
	var buf bytes.Buffer
	err = uiTemplates.ExecuteTemplate(&buf, "issue-panel", &uiIssuePanelData{
		CanWrite: true,
		Issue: model.Issue{
			ID:            issueID,
			ProjectID:     projectID,
			OwnerUsername: "bradley",
			ProjectKey:    "TRACK",
			Identifier:    "TRACK-7",
			Title:         "Design issue detail",
			Description:   "Readonly description",
			Status:        model.StatusInProgress,
			AssigneeID:    &userID,
			ReporterID:    &userID,
			SprintID:      &sprint.ID,
			DueDate:       &dueDate,
			Tags:          tags,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:       model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		Sprint:        &sprint,
		Assignee:      &assignee,
		Reporter:      &reporter,
		CanEditSprint: true,
		Comments: []uiIssueCommentItem{{
			Comment:     model.Comment{ID: commentID, IssueID: issueID, Number: 2, Ref: "comment-2", AuthorID: userID, Body: "Looks **ready**.", CreatedAt: when, UpdatedAt: when},
			BodyHTML:    `<p>Looks <strong>ready</strong>.</p>`,
			AuthorName:  "Ada Lovelace",
			AuthorEmail: "ada@example.com",
			CanEdit:     true,
		}},
		Links: []uiIssueLinkItem{{
			Link:        model.IssueLink{ID: linkID, ProjectID: projectID, Number: 4, Ref: "link-4", SourceID: issueID, TargetID: linkedID, LinkType: model.LinkTypeBlocks, CreatedAt: when, UpdatedAt: when},
			LinkedIssue: model.Issue{ID: linkedID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-8", Title: "Linked work", Status: model.StatusDone},
			HasIssue:    true,
		}},
		BackHref:  "/bradley/projects/TRACK/planned",
		BackHXGet: "/bradley/projects/TRACK/planned/panel",
		BackLabel: "Planned",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`aria-label="Breadcrumb"`,
		`href="/projects"`,
		`hx-get="/projects/panel"`,
		`href="/bradley/projects/TRACK/all"`,
		`hx-get="/bradley/projects/TRACK/all/panel"`,
		`aria-current="page"`,
		"TRACK-7",
		"text-3xl",
		"Design issue detail",
		"Readonly description",
		"In progress",
		"Track Slash",
		"#Customer Beta",
		"Context",
		"Ada Lovelace",
		"Planned One",
		"Due date",
		"Jun 24",
		`aria-label="Due Jun 24, 2099"`,
		`data-lucide="calendar"`,
		"Linked issues",
		"Blocks",
		"TRACK-8",
		"Linked work",
		"Comments",
		`<p>Looks <strong>ready</strong>.</p>`,
		`href="/bradley/issues/TRACK-8"`,
		`hx-get="/bradley/issues/TRACK-8/panel"`,
		`aria-label="Issue actions"`,
		`data-lucide="more-horizontal"`,
		`cursor-pointer list-none`,
		`method="post" action="/bradley/issues/TRACK-7/delete"`,
		`hx-post="/bradley/issues/TRACK-7/delete"`,
		`hx-push-url="/bradley/projects/TRACK/planned"`,
		`hx-confirm="Delete this issue? You can undo it from the next screen."`,
		`Delete issue`,
		`data-lucide="trash-2"`,
		`text-rose-600`,
		`dark:hover:bg-rose-950/40`,
		`aria-label="Edit title"`,
		`hx-get="/bradley/issues/TRACK-7/title/edit"`,
		`aria-label="Manage tags"`,
		`href="/bradley/issues/TRACK-7/tags"`,
		`hx-get="/bradley/issues/TRACK-7/tags"`,
		`href="/bradley/projects/TRACK/planned"`,
		`hx-get="/bradley/projects/TRACK/planned/panel"`,
		`hx-push-url="/bradley/projects/TRACK/planned"`,
		`data-lucide="corner-up-left"`,
		`aria-label="Edit description"`,
		`hx-get="/bradley/issues/TRACK-7/description/edit"`,
		`aria-label="Edit link"`,
		`hx-get="/bradley/issues/TRACK-7/links/link-4/edit"`,
		`aria-label="Edit comment"`,
		`hx-get="/bradley/issues/TRACK-7/comments/comment-2/edit"`,
		`aria-label="Change status"`,
		`aria-expanded="false"`,
		`hx-get="/bradley/issues/TRACK-7/status/edit"`,
		`aria-label="Edit assignee"`,
		`aria-label="Edit reporter"`,
		`aria-label="Edit due date"`,
		`hx-get="/bradley/issues/TRACK-7/due-date/edit"`,
		`aria-label="Edit sprint"`,
		`hx-get="/bradley/issues/TRACK-7/sprint/edit"`,
		`aria-label="Manage context"`,
		`hx-get="/bradley/issues/TRACK-7/context"`,
		`hx-push-url="/bradley/issues/TRACK-7/context"`,
		`data-lucide="book-open"`,
		`aria-label="Ada Lovelace" title="Ada Lovelace"`,
		`class="flex min-w-0 items-center gap-2 text-slate-900 dark:text-slate-100"`,
		`<span class="min-w-0 truncate text-slate-900 dark:text-slate-100">Planned One</span>`,
		`aria-label="Add link"`,
		`hx-get="/bradley/issues/TRACK-7/links/new"`,
		`aria-label="Add sub-issue"`,
		`hx-get="/bradley/issues/TRACK-7/sub-issues/new"`,
		`data-modal-open="issue-comment-create"`,
		`aria-controls="issue-comment-create"`,
		`id="issue-comment-create" data-client-modal class="fixed inset-0 z-50 hidden`,
		`role="dialog" aria-modal="true" aria-labelledby="issue-comment-create-title"`,
		`aria-haspopup="listbox"`,
		`data-lucide="chevron-down"`,
		`role="img" aria-label="Linked issue progress: 1 done, 0 in progress, 0 to do"`,
		`viewBox="0 0 1 1"`,
		`<rect x="0" width="1" height="1" class="fill-emerald-500 dark:fill-emerald-400"`,
		`placeholder="Write a comment"`,
		`method="post" action="/bradley/issues/TRACK-7/comments"`,
		`hx-post="/bradley/issues/TRACK-7/comments"`,
		`hx-target="#main"`,
		`hx-push-url="false"`,
		`data-submit-shortcut="meta-enter"`,
		`data-autogrow-textarea`,
		`<textarea name="body" rows="4"`,
		`data-lucide="send-horizontal"`,
		`class="order-2 min-w-0 space-y-6 lg:order-1"`,
		`class="order-1 min-w-0 lg:order-2"`,
		`class="min-h-24 w-full resize-y rounded-md border border-slate-200 bg-white px-3 py-2 text-sm leading-6 text-slate-950`,
		`class="space-y-3 px-4"`,
		`class="grid h-4 w-4 shrink-0 place-items-center bg-slate-100 text-[7px] font-semibold leading-none text-slate-600 dark:bg-slate-800 dark:text-slate-300 overflow-hidden rounded-full"`,
		`class="w-fit max-w-full rounded-xl border border-indigo-100 bg-indigo-50/70 px-3 py-2 dark:border-indigo-900/50 dark:bg-indigo-950/25"`,
		`class="mb-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 pl-1"`,
		`class="markdown-body text-sm leading-6 text-slate-800 dark:text-slate-200"`,
		`inline-flex w-fit justify-self-start items-center whitespace-nowrap rounded-md border border-slate-300 bg-white px-1.5 py-0.5 font-mono text-[11px]`,
		`class="col-span-2 row-start-2 flex min-w-0 flex-wrap items-center gap-2 hover:text-indigo-700 dark:hover:text-indigo-200 sm:col-span-1 sm:col-start-2 sm:row-start-1 sm:flex-nowrap"`,
		`class="min-w-0 basis-full break-words text-slate-900 dark:text-slate-100 sm:basis-auto sm:flex-1 sm:truncate">Linked work</span>`,
		`class="min-w-0 overflow-hidden rounded-md border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900 w-full"`,
		`class="text-xs font-semibold uppercase text-slate-500 dark:text-slate-400">Sub-issues</h2>`,
		`class="text-xs font-semibold uppercase text-slate-500 dark:text-slate-400">Linked issues</h2>`,
		`class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-slate-100 px-4 py-2.5 text-xs`,
		`sm:grid-cols-[4.75rem_minmax(0,1fr)_auto]`,
		`h-5 w-5`,
		`h-3 w-3`,
		`border-blue-300 bg-blue-50 text-blue-800`,
		`bg-blue-50/45 dark:bg-blue-950/15`,
		`bg-emerald-50/45 hover:bg-emerald-50`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("issue panel missing %q: %s", want, body)
		}
	}
	if got := strings.Count(body, "bg-blue-50/45 dark:bg-blue-950/15"); got != 1 {
		t.Fatalf("issue panel should tint the title card only, got %d matches: %s", got, body)
	}
	if strings.Contains(body, "⌘ + Enter to send") {
		t.Fatalf("issue panel should not render command-enter helper text: %s", body)
	}
	requireInlineCount(t, body, "Sub-issues", 0)
	requireInlineCount(t, body, "Linked issues", 1)
	requireInlineCount(t, body, "Comments", 1)
	detailsStart := strings.Index(body, ">Details</h2>")
	if detailsStart < 0 {
		t.Fatalf("issue panel missing Details heading: %s", body)
	}
	commentsHeadingStart := strings.Index(body, ">Comments</h2>")
	commentsSectionStart := -1
	if commentsHeadingStart >= 0 {
		commentsSectionStart = strings.LastIndex(body[:commentsHeadingStart], "<section")
	}
	if commentsSectionStart < 0 || commentsHeadingStart > detailsStart {
		t.Fatalf("issue panel missing comments section before details: %s", body)
	}
	commentsBlock := body[commentsSectionStart:detailsStart]
	for _, notWant := range []string{
		`overflow-hidden rounded-lg border border-slate-200 bg-white`,
		`border-t border-slate-100 p-4`,
		`border-t border-dashed border-slate-200 px-4 py-4`,
		`rotate-45`,
	} {
		if strings.Contains(commentsBlock, notWant) {
			t.Fatalf("comments section should not render the outer card treatment %q: %s", notWant, body)
		}
	}
	detailsBlock := body[detailsStart:]
	if strings.Contains(detailsBlock, ">Tags</dt>") {
		t.Fatalf("issue panel should render tags in the title header, not details: %s", body)
	}
	statusIndex := strings.Index(detailsBlock, `aria-label="Change status"`)
	projectIndex := strings.Index(detailsBlock, ">Project</dt>")
	if statusIndex < 0 || projectIndex < 0 {
		t.Fatalf("issue panel missing status control or project details row: %s", body)
	}
	if statusIndex > projectIndex {
		t.Fatalf("status control should render before project detail: %s", body)
	}
	if strings.Contains(detailsBlock, ">Status</dt>") || strings.Contains(body, `aria-label="Edit status"`) {
		t.Fatalf("status control should not render a separate title or edit button: %s", body)
	}
	for _, notWant := range []string{`/archive`, `Archive issue`, `data-lucide="archive"`, `data-lucide="settings"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue panel included removed archive control %q: %s", notWant, body)
		}
	}
	if got := strings.Count(body, `class="mt-1 flex items-center justify-between gap-3"`); got != 5 {
		t.Fatalf("context, due date, assignee, reporter, and sprint rows should align action buttons with values, got %d rows: %s", got, body)
	}
	if strings.Contains(detailsBlock, `class="flex items-start justify-between gap-3"`) {
		t.Fatalf("detail edit buttons should not align with row titles: %s", body)
	}
	for _, notWant := range []string{`method="post" action="/bradley/issues/TRACK-7/sub-issues"`, `aria-label="Create sub-issue"`, `aria-label="Cancel adding sub-issue"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("issue panel should not render the sub-issue composer by default %q: %s", notWant, body)
		}
	}
	if strings.Contains(body, `border-b border-slate-100 px-4 py-4 last:border-b-0`) {
		t.Fatalf("comments should render as bubbles instead of bordered rows: %s", body)
	}
	commentMetaStart := strings.Index(body, `<span class="text-xs font-medium text-slate-600 dark:text-slate-300">Ada Lovelace</span>`)
	commentBodyStart := strings.Index(body, `<p>Looks <strong>ready</strong>.</p>`)
	if commentMetaStart < 0 || commentBodyStart < 0 || commentMetaStart > commentBodyStart {
		t.Fatalf("issue panel should render comment metadata above the body: %s", body)
	}
	commentComposerStart := strings.Index(commentsBlock, `placeholder="Write a comment"`)
	if commentComposerStart < 0 || commentsSectionStart+commentComposerStart > commentMetaStart {
		t.Fatalf("comment composer should render above the comment list: %s", body)
	}
	commentBubbleStart := strings.Index(body, `class="w-fit max-w-full rounded-xl border border-indigo-100 bg-indigo-50/70 px-3 py-2`)
	if commentBubbleStart < 0 || commentBubbleStart < commentMetaStart || commentBubbleStart > commentBodyStart {
		t.Fatalf("comment body should render inside the bubble after metadata: %s", body)
	}
	commentAvatarStart := strings.Index(body, `class="grid h-4 w-4 shrink-0 place-items-center bg-slate-100 text-[7px] font-semibold leading-none text-slate-600 dark:bg-slate-800 dark:text-slate-300 overflow-hidden rounded-full"`)
	if commentAvatarStart < 0 || commentAvatarStart > commentMetaStart {
		t.Fatalf("comment avatar should render with the metadata beside the author name: %s", body)
	}
	commentEditStart := strings.Index(body, `aria-label="Edit comment"`)
	if commentEditStart < 0 || commentEditStart < commentMetaStart || commentEditStart > commentBubbleStart {
		t.Fatalf("comment edit button should render with metadata above the bubble: %s", body)
	}
	if strings.Contains(body, "<textarea disabled") {
		t.Fatalf("comment composer should be enabled: %s", body)
	}
	titleHeaderEnd := strings.Index(body, "</header>")
	if titleHeaderEnd < 0 {
		t.Fatalf("issue panel missing title header: %s", body)
	}
	titleHeader := body[:titleHeaderEnd]
	if !strings.Contains(titleHeader, "#Customer Beta") ||
		!strings.Contains(titleHeader, `aria-label="Manage tags"`) ||
		!strings.Contains(titleHeader, "Planned One") {
		t.Fatalf("title header should render tags, tag manager action, and sprint title: %s", body)
	}
	tagActionStart := strings.Index(titleHeader, `aria-label="Manage tags"`)
	if tagActionStart < 0 {
		t.Fatalf("title header missing tag manager action: %s", titleHeader)
	}
	tagActionEnd := strings.Index(titleHeader[tagActionStart:], "</a>")
	if tagActionEnd < 0 || !strings.Contains(titleHeader[tagActionStart:tagActionStart+tagActionEnd], `hx-push-url="false"`) {
		t.Fatalf("title header tag manager action should open without pushing URL: %s", titleHeader)
	}
	for _, notWant := range []string{"Edit issue", "Change status", "Edit description", "Edit status", "In progress", "cursor-not-allowed"} {
		if strings.Contains(titleHeader, notWant) {
			t.Fatalf("title card still contains section action/status %q: %s", notWant, body)
		}
	}
	if strings.Contains(titleHeader, "title=") {
		t.Fatalf("issue panel title controls should not render native title tooltips: %s", body)
	}
}

func TestUIIssuePanelKeepsTitleEditActionAttached(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	when := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		title    string
		leading  string
		trailing string
	}{
		{name: "short", title: "A", trailing: "A"},
		{name: "long", title: "A deliberately long issue title that can wrap naturally before its final character界", leading: "A deliberately long issue title that can wrap naturally before its final character", trailing: "界"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			err := uiTemplates.ExecuteTemplate(&buf, "issue-panel-header", &uiIssuePanelData{
				CanWrite: true,
				Issue: model.Issue{
					ID:            issueID,
					ProjectID:     projectID,
					OwnerUsername: "bradley",
					ProjectKey:    "TRACK",
					Identifier:    "TRACK-7",
					Title:         tc.title,
					Status:        model.StatusTodo,
					CreatedAt:     when,
					UpdatedAt:     when,
				},
				Project:   model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
				BackHref:  "/bradley/projects/TRACK/backlog",
				BackHXGet: "/bradley/projects/TRACK/backlog/panel",
				BackLabel: "Backlog",
			})
			if err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}

			body := buf.String()
			tail := `<span data-issue-title-tail class="inline-flex items-center whitespace-nowrap align-middle"><span>` + tc.trailing + `</span><button type="button" aria-label="Edit title"`
			if !strings.Contains(body, tc.leading+tail) {
				t.Fatalf("issue title does not keep its final character and edit action together: %s", body)
			}
			for _, want := range []string{
				`class="mt-2 min-w-0 break-words text-2xl sm:text-3xl`,
				`hx-get="/bradley/issues/TRACK-7/title/edit"`,
				`hx-target="#main"`,
				`hx-push-url="false"`,
				`class="ml-2 grid h-7 w-7 shrink-0`,
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("attached title action missing %q: %s", want, body)
				}
			}
			if strings.Contains(body, `title="Edit title"`) {
				t.Fatalf("attached title action should use the shared aria-label tooltip: %s", body)
			}
		})
	}
}

func TestUIIssuePanelRendersTitleEditor(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	when := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "issue-panel", &uiIssuePanelData{
		CanWrite: true,
		Issue: model.Issue{
			ID:            issueID,
			ProjectID:     projectID,
			OwnerUsername: "bradley",
			ProjectKey:    "TRACK",
			Identifier:    "TRACK-7",
			Title:         "Title editor",
			Status:        model.StatusTodo,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:    model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		EditTitle:  true,
		TitleInput: " Draft title ",
		TitleError: "Title required, max 200 chars.",
		BackHref:   "/bradley/projects/TRACK/backlog",
		BackHXGet:  "/bradley/projects/TRACK/backlog/panel",
		BackLabel:  "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`method="post" action="/bradley/issues/TRACK-7/title"`,
		`hx-post="/bradley/issues/TRACK-7/title"`,
		`hx-push-url="false"`,
		`name="title" value=" Draft title "`,
		`aria-label="Issue title"`,
		`[field-sizing:content]`,
		`class="flex min-w-0 flex-wrap items-center gap-2"`,
		`aria-label="Save title"`,
		`aria-label="Cancel editing title"`,
		`hx-get="/bradley/issues/TRACK-7/panel"`,
		"Title required, max 200 chars.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("title editor missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `hx-get="/bradley/issues/TRACK-7/title/edit"`) {
		t.Fatalf("title editor rendered readonly edit button: %s", body)
	}
	formStart := strings.Index(body, `method="post" action="/bradley/issues/TRACK-7/title"`)
	formEnd := -1
	if formStart >= 0 {
		formEnd = strings.Index(body[formStart:], "</form>")
	}
	if formStart < 0 || formEnd < 0 {
		t.Fatalf("title editor missing title form: %s", body)
	}
	titleForm := body[formStart : formStart+formEnd]
	for _, notWant := range []string{`sm:grid-cols-[minmax(0,1fr)_auto]`, `h-9 w-9`} {
		if strings.Contains(titleForm, notWant) {
			t.Fatalf("title editor action should flow after title instead of fixed grid %q: %s", notWant, body)
		}
	}
}

func TestUIDeletedIssuePanelRendersRestore(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	when := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "deleted-issue-panel", &uiDeletedIssuePanelData{
		CanWrite: true,
		Issue: model.Issue{
			ID:            issueID,
			ProjectID:     projectID,
			OwnerUsername: "bradley",
			ProjectKey:    "TRACK",
			Identifier:    "TRACK-7",
			Title:         "Deleted issue title",
			Description:   "Hidden deleted description",
			Status:        model.StatusDone,
			Priority:      model.PriorityP1,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:   model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		BackHref:  "/bradley/projects/TRACK/deleted",
		BackHXGet: "/bradley/projects/TRACK/deleted/panel",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`rounded-lg border border-slate-300`,
		`mx-auto max-w-lg pt-6 sm:pt-10`,
		`px-4 pb-5 pt-6 text-center`,
		`sm:px-7 sm:pb-7 sm:pt-10`,
		`Deleted issue`,
		"TRACK-7",
		"Deleted issue title",
		"This issue has been deleted",
		"Track Slash",
		"Done",
		`h-px max-w-xs bg-slate-200`,
		`method="post" action="/bradley/issues/TRACK-7/restore"`,
		`hx-post="/bradley/issues/TRACK-7/restore"`,
		`hx-target="#main"`,
		`hx-push-url="/bradley/issues/TRACK-7"`,
		`data-lucide="rotate-ccw"`,
		"Restore issue",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("deleted issue panel missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{`data-lucide="arrow-left"`, `href="/bradley/projects/TRACK/deleted"`, `hx-get="/bradley/projects/TRACK/deleted/panel"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("deleted issue panel should not render back button markup %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{"Hidden deleted description", "Comments", "Sub-issues", `aria-label="Issue actions"`, `Delete issue`, `data-lucide="trash-2"`, `rounded-t-[`, `rounded-b-md`, `mt-4 rounded-lg border border-slate-200 bg-slate-50 p-4`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("deleted issue panel leaked full issue UI %q: %s", notWant, body)
		}
	}
}

func TestUIIssuePanelRendersDueDateEditor(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	when := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	dueDate, err := model.ParseDate("2026-06-24")
	if err != nil {
		t.Fatalf("ParseDate: %v", err)
	}
	var buf bytes.Buffer
	err = uiTemplates.ExecuteTemplate(&buf, "issue-panel", &uiIssuePanelData{
		CanWrite: true,
		Issue: model.Issue{
			ID:            issueID,
			ProjectID:     projectID,
			OwnerUsername: "bradley",
			ProjectKey:    "TRACK",
			Identifier:    "TRACK-7",
			Title:         "Due date editor",
			Status:        model.StatusTodo,
			DueDate:       &dueDate,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:      model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		EditDueDate:  true,
		DueDateInput: "2026-06-24",
		DueDateError: "Use YYYY-MM-DD.",
		BackHref:     "/bradley/projects/TRACK/backlog",
		BackHXGet:    "/bradley/projects/TRACK/backlog/panel",
		BackLabel:    "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`method="post" action="/bradley/issues/TRACK-7/due-date"`,
		`hx-post="/bradley/issues/TRACK-7/due-date"`,
		`type="date" name="due_date" value="2026-06-24"`,
		`aria-label="Save due date"`,
		`aria-label="Cancel editing due date"`,
		`hx-get="/bradley/issues/TRACK-7/panel"`,
		"Use YYYY-MM-DD.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("due date editor missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `hx-get="/bradley/issues/TRACK-7/due-date/edit"`) {
		t.Fatalf("due date editor rendered readonly edit button: %s", body)
	}
}
