package server

import (
	"bytes"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestUIIssueSprintOptionForNoDates(t *testing.T) {
	t.Parallel()
	option := uiIssueSprintOptionFor(model.Sprint{
		Number: 3,
		Ref:    "sprint-3",
		Name:   "Flexible Sprint",
		Status: model.SprintStatusPlanned,
	}, "Planned")
	if option.Label != "Planned - Flexible Sprint" {
		t.Fatalf("label = %q", option.Label)
	}
}

func TestUIIssuePanelRendersStatusDropdown(t *testing.T) {
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
			Title:         "Design issue detail",
			Status:        model.StatusInProgress,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:    model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		EditStatus: true,
		BackHref:   "/bradley/projects/TRACK/backlog",
		BackHXGet:  "/bradley/projects/TRACK/backlog/panel",
		BackLabel:  "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`aria-label="Change status"`,
		`aria-expanded="true"`,
		`data-option-dropdown-toggle`,
		`data-option-dropdown-list`,
		`data-lucide="chevron-up"`,
		`hx-get="/bradley/issues/TRACK-7/panel"`,
		`method="post" action="/bradley/issues/TRACK-7/status"`,
		`hx-post="/bradley/issues/TRACK-7/status"`,
		`hx-target="#main"`,
		`hx-push-url="false"`,
		`role="listbox" aria-label="Issue status"`,
		`name="status" value="todo"`,
		`name="status" value="in_progress"`,
		`name="status" value="done"`,
		`name="status" value="closed"`,
		`role="option" aria-selected="true"`,
		"To do",
		"In progress",
		"Done",
		"Closed",
		`data-lucide="check"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("status dropdown missing %q: %s", want, body)
		}
	}
	css, err := uiTemplateFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	for _, want := range []string{
		"@keyframes option-dropdown-enter",
		"@keyframes option-dropdown-settle",
		"[data-option-dropdown-toggle]{animation:option-dropdown-settle .12s ease-out both}",
		"[data-option-dropdown-list]{animation:option-dropdown-enter .14s ease-out both;transform-origin:top}",
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("status dropdown stylesheet missing %q", want)
		}
	}
	for _, notWant := range []string{
		`hx-get="/bradley/issues/TRACK-7/status/edit"`,
		`aria-label="Cancel status change"`,
		`cursor-default`,
		`disabled aria-label="Change status"`,
		`title="Cancel status change"`,
		`title="Change status"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("status dropdown included %q: %s", notWant, body)
		}
	}
}

func TestUIIssuePanelRendersPriorityPicker(t *testing.T) {
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
			Title:         "Design issue detail",
			Status:        model.StatusInProgress,
			Priority:      model.PriorityP1,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:      model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		EditPriority: true,
		BackHref:     "/bradley/projects/TRACK/backlog",
		BackHXGet:    "/bradley/projects/TRACK/backlog/panel",
		BackLabel:    "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`method="post" action="/bradley/issues/TRACK-7/priority"`,
		`hx-post="/bradley/issues/TRACK-7/priority"`,
		`hx-target="#main"`,
		`hx-push-url="false"`,
		`role="listbox" aria-label="Issue priority" data-priority-picker class="flex flex-wrap items-center gap-1"`,
		`name="priority" value="P0"`,
		`name="priority" value="P1"`,
		`name="priority" value="P2"`,
		`name="priority" value="P3"`,
		`name="priority" value="P4"`,
		`aria-label="Priority P1"`,
		`bg-red-600`,
		`bg-orange-500`,
		`bg-amber-500`,
		`bg-yellow-500`,
		`bg-gray-500`,
		`role="option" aria-selected="true"`,
		`type="button" hx-get="/bradley/issues/TRACK-7/panel" hx-target="#main" hx-push-url="false" name="priority" value="P1"`,
		`rounded-full p-0.5 transition`,
		`opacity-100`,
		`opacity-40 hover:opacity-80`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("priority picker missing %q: %s", want, body)
		}
	}
	css, err := uiTemplateFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	for _, want := range []string{"@keyframes priority-picker-item-enter", "@media (prefers-reduced-motion:no-preference)", "[data-priority-picker]>:is(label,button):nth-child(5){animation-delay:80ms}"} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("priority picker stylesheet missing %q", want)
		}
	}
	for _, notWant := range []string{
		`hx-get="/bradley/issues/TRACK-7/priority/edit"`,
		`aria-expanded="true"`,
		`data-lucide="chevron-up"`,
		`opacity-100 ring-2 ring-indigo-500`,
		`aria-label="Cancel priority change"`,
		`title="Cancel priority change"`,
		`title="Change priority"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("priority picker included %q: %s", notWant, body)
		}
	}
}

func TestUIIssuePanelRendersDescriptionEditForm(t *testing.T) {
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
			Title:         "Design issue detail",
			Description:   "Editable description",
			Status:        model.StatusTodo,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:         model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		EditDescription: true,
		BackHref:        "/bradley/projects/TRACK/backlog",
		BackHXGet:       "/bradley/projects/TRACK/backlog/panel",
		BackLabel:       "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`method="post" action="/bradley/issues/TRACK-7/description"`,
		`hx-post="/bradley/issues/TRACK-7/description"`,
		`hx-target="#main"`,
		`hx-push-url="false"`,
		`name="description"`,
		`placeholder="` + uiIssueDescriptionPlaceholder + `"`,
		`data-submit-shortcut="meta-enter"`,
		`aria-label="Save description"`,
		`data-lucide="check"`,
		`aria-label="Cancel editing description"`,
		`hx-get="/bradley/issues/TRACK-7/panel"`,
		`data-lucide="x"`,
		"Editable description",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("description edit form missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{
		`aria-label="Edit description"`,
		"No description.",
		"<textarea disabled",
		`title="Save description"`,
		`title="Cancel editing description"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("description edit form included %q: %s", notWant, body)
		}
	}
}

func TestUIIssuePanelRendersSprintEditForm(t *testing.T) {
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
			Title:         "Design issue detail",
			Status:        model.StatusInProgress,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:       model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		EditSprint:    true,
		CanEditSprint: true,
		SprintInput:   "sprint-2",
		SprintError:   "Choose an active or planned sprint.",
		SprintOptions: []uiIssueSprintOption{
			{Value: "sprint-1", Label: "Active - Current Sprint - Jun 1-Jun 14"},
			{Value: "sprint-3", Label: "Planned - Next Sprint - Jun 15-Jun 28"},
			{Value: "sprint-5", Label: "Planned - Flexible Sprint"},
		},
		BackHref:  "/bradley/projects/TRACK/sprint",
		BackHXGet: "/bradley/projects/TRACK/sprint/panel",
		BackLabel: "Sprint",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	sprintFormStart := strings.Index(body, `method="post" action="/bradley/issues/TRACK-7/sprint"`)
	if sprintFormStart < 0 {
		t.Fatalf("sprint edit form missing: %s", body)
	}
	sprintFormEnd := strings.Index(body[sprintFormStart:], `</form>`)
	if sprintFormEnd < 0 {
		t.Fatalf("sprint edit form did not close: %s", body)
	}
	sprintForm := body[sprintFormStart : sprintFormStart+sprintFormEnd]
	for _, want := range []string{
		`method="post" action="/bradley/issues/TRACK-7/sprint"`,
		`hx-post="/bradley/issues/TRACK-7/sprint"`,
		`hx-target="#main"`,
		`hx-push-url="false"`,
		`data-search`,
		`name="sprint" value="sprint-2" autocomplete="off"`,
		`data-search-input`,
		`placeholder="None"`,
		`data-lucide="search"`,
		`aria-label="Save sprint"`,
		`aria-label="Cancel editing sprint"`,
		`hx-get="/bradley/issues/TRACK-7/panel"`,
		`role="listbox" aria-label="Sprint suggestions"`,
		`data-search-option`,
		`data-value="sprint-1"`,
		`data-search-text="sprint-1 Active - Current Sprint - Jun 1-Jun 14"`,
		`Active - Current Sprint - Jun 1-Jun 14`,
		`data-value="sprint-3"`,
		`Planned - Next Sprint - Jun 15-Jun 28`,
		`data-value="sprint-5"`,
		`Planned - Flexible Sprint`,
		`Choose an active or planned sprint.`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sprint edit form missing %q: %s", want, body)
		}
	}
	activeIndex := strings.Index(body, `data-value="sprint-1"`)
	plannedIndex := strings.Index(body, `data-value="sprint-3"`)
	if activeIndex < 0 || plannedIndex < 0 || activeIndex > plannedIndex {
		t.Fatalf("active sprint option should render before planned option: %s", body)
	}
	for _, notWant := range []string{
		`<datalist`,
		`list="issue-sprint-options"`,
		`hx-get="/bradley/issues/TRACK-7/sprint/edit"`,
		`value="sprint-4"`,
		`Completed Sprint`,
		`title="Save sprint"`,
		`title="Cancel editing sprint"`,
	} {
		if strings.Contains(sprintForm, notWant) {
			t.Fatalf("sprint edit form included %q: %s", notWant, sprintForm)
		}
	}
}

func TestUIIssuePanelDoneDisablesSprintEdit(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	sprintID := uuid.MustParse("d7fc0dbf-845c-41b4-84ab-89f487cc4a08")
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
			Title:         "Done issue",
			Status:        model.StatusDone,
			SprintID:      &sprintID,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:   model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		Sprint:    &model.Sprint{ID: sprintID, Name: "Completed Work"},
		BackHref:  "/bradley/projects/TRACK/sprint",
		BackHXGet: "/bradley/projects/TRACK/sprint/panel",
		BackLabel: "Sprint",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		"Completed Work",
		`aria-label="Edit sprint"`,
		"disabled",
		"cursor-not-allowed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("done sprint row missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{
		`hx-get="/bradley/issues/TRACK-7/sprint/edit"`,
		`method="post" action="/bradley/issues/TRACK-7/sprint"`,
		`name="sprint"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("done sprint row included %q: %s", notWant, body)
		}
	}
}

func TestUIIssuePanelClosedDisablesSprintEdit(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	sprintID := uuid.MustParse("d7fc0dbf-845c-41b4-84ab-89f487cc4a08")
	closeReason := model.CloseReasonWontDo
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
			Title:         "Closed issue",
			Status:        model.StatusClosed,
			CloseReason:   &closeReason,
			SprintID:      &sprintID,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:   model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		Sprint:    &model.Sprint{ID: sprintID, Name: "Closed Work"},
		BackHref:  "/bradley/projects/TRACK/sprint",
		BackHXGet: "/bradley/projects/TRACK/sprint/panel",
		BackLabel: "Sprint",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		"Closed Work",
		`aria-label="Edit sprint"`,
		"disabled",
		"cursor-not-allowed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("closed sprint row missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{
		`hx-get="/bradley/issues/TRACK-7/sprint/edit"`,
		`method="post" action="/bradley/issues/TRACK-7/sprint"`,
		`name="sprint"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("closed sprint row included %q: %s", notWant, body)
		}
	}
}

func TestUIIssuePanelRendersSubIssueProgressBar(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	closeReason := model.CloseReasonWontDo
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
			Title:         "Parent issue",
			Status:        model.StatusTodo,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project: model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		SubIssues: []model.Issue{
			{Status: model.StatusDone},
			{Status: model.StatusClosed, CloseReason: &closeReason},
			{Status: model.StatusInProgress},
			{Status: model.StatusTodo},
		},
		BackHref:  "/bradley/projects/TRACK/backlog",
		BackHXGet: "/bradley/projects/TRACK/backlog/panel",
		BackLabel: "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`role="img" aria-label="Sub-issue progress: 2 done, 1 in progress, 1 to do"`,
		`viewBox="0 0 4 1"`,
		`class="mt-1.5 h-1 w-full overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800"`,
		`data-issue-summary-row`,
		`sm:grid-cols-[7rem_auto_minmax(0,1fr)_auto]`,
		`<rect x="0" width="2" height="1" class="fill-emerald-500 dark:fill-emerald-400"`,
		`<rect x="2" width="1" height="1" class="fill-blue-400 dark:fill-blue-500"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sub-issue progress bar missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "max-w-xs") {
		t.Fatalf("sub-issue progress bar should fill the available width: %s", body)
	}
	requireInlineCount(t, body, "Sub-issues", 4)
	addIndex := strings.Index(body, `aria-label="Add sub-issue"`)
	progressIndex := strings.Index(body, `role="img" aria-label="Sub-issue progress: 2 done, 1 in progress, 1 to do"`)
	if addIndex < 0 || progressIndex < 0 || addIndex > progressIndex {
		t.Fatalf("sub-issue progress bar should render after the title row controls: %s", body)
	}
}

func TestUIIssuePanelRendersLinkedIssueProgressBar(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	doneID := uuid.MustParse("ae77b9b8-9dcf-4a18-8b69-42b97bd4a4b5")
	closedID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	progressID := uuid.MustParse("138095fe-77d7-4644-b127-d0b995757ff2")
	todoID := uuid.MustParse("2eeaf29c-ad20-4513-af41-edbb2c9abc2c")
	deletedID := uuid.MustParse("0e4c50a0-ae1a-46e9-a7b5-75989e4f3ec3")
	closeReason := model.CloseReasonInvalid
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
			Title:         "Issue with links",
			Status:        model.StatusTodo,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project: model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		Links: []uiIssueLinkItem{
			{
				Link:        model.IssueLink{ID: uuid.MustParse("48c98f2e-bad8-4054-89d7-5a45a68af54f"), ProjectID: projectID, Number: 1, Ref: "link-1", SourceID: issueID, TargetID: doneID, LinkType: model.LinkTypeRelatesTo, CreatedAt: when, UpdatedAt: when},
				LinkedIssue: model.Issue{ID: doneID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-8", Title: "Done link", Status: model.StatusDone},
				HasIssue:    true,
			},
			{
				Link:        model.IssueLink{ID: uuid.MustParse("4f6df8d9-f343-40f9-9c65-861d2967af90"), ProjectID: projectID, Number: 5, Ref: "link-5", SourceID: issueID, TargetID: closedID, LinkType: model.LinkTypeRelatesTo, CreatedAt: when, UpdatedAt: when},
				LinkedIssue: model.Issue{ID: closedID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-11", Title: "Closed link", Status: model.StatusClosed, CloseReason: &closeReason},
				HasIssue:    true,
			},
			{
				Link:        model.IssueLink{ID: uuid.MustParse("af63e70c-bf9d-4f80-999d-df145379ec6d"), ProjectID: projectID, Number: 2, Ref: "link-2", SourceID: issueID, TargetID: progressID, LinkType: model.LinkTypeBlocks, CreatedAt: when, UpdatedAt: when},
				LinkedIssue: model.Issue{ID: progressID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-9", Title: "Progress link", Status: model.StatusInProgress},
				HasIssue:    true,
			},
			{
				Link:        model.IssueLink{ID: uuid.MustParse("57bf290a-e723-42e6-8a1d-2c7ed8672646"), ProjectID: projectID, Number: 3, Ref: "link-3", SourceID: todoID, TargetID: issueID, LinkType: model.LinkTypeBlocks, CreatedAt: when, UpdatedAt: when},
				LinkedIssue: model.Issue{ID: todoID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-10", Title: "Todo link", Status: model.StatusTodo},
				HasIssue:    true,
			},
			{
				Link:     model.IssueLink{ID: uuid.MustParse("9b208bfe-63f0-461d-ad2b-4725106fd314"), ProjectID: projectID, Number: 4, Ref: "link-4", SourceID: issueID, TargetID: deletedID, LinkType: model.LinkTypeClones, CreatedAt: when, UpdatedAt: when},
				HasIssue: false,
			},
		},
		BackHref:  "/bradley/projects/TRACK/backlog",
		BackHXGet: "/bradley/projects/TRACK/backlog/panel",
		BackLabel: "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`role="img" aria-label="Linked issue progress: 2 done, 1 in progress, 1 to do"`,
		`viewBox="0 0 4 1"`,
		`<rect x="0" width="2" height="1" class="fill-emerald-500 dark:fill-emerald-400"`,
		`<rect x="2" width="1" height="1" class="fill-blue-400 dark:fill-blue-500"`,
		"Deleted issue",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("linked issue progress missing %q: %s", want, body)
		}
	}
	requireInlineCount(t, body, "Linked issues", 5)
	addIndex := strings.Index(body, `aria-label="Add link"`)
	progressIndex := strings.Index(body, `role="img" aria-label="Linked issue progress: 2 done, 1 in progress, 1 to do"`)
	if addIndex < 0 || progressIndex < 0 || addIndex > progressIndex {
		t.Fatalf("linked issue progress bar should render after the title row controls: %s", body)
	}
}
