package server

import (
	"bytes"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestUIProjectBreadcrumbIncludesCurrentView(t *testing.T) {
	t.Parallel()

	project := model.Project{OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	for _, tt := range []struct {
		view  string
		label string
	}{
		{view: "sprint", label: "Sprint"},
		{view: "planned", label: "Planned"},
		{view: "all", label: "All"},
		{view: "context", label: "Context"},
		{view: "about", label: "About"},
		{view: "members", label: "Members"},
		{view: "sprints", label: "Sprint history"},
		{view: "changelog", label: "Changelog"},
	} {
		t.Run(tt.view, func(t *testing.T) {
			items := uiProjectBreadcrumb(project, tt.view).Items
			if len(items) != 3 {
				t.Fatalf("breadcrumb item count = %d, want 3: %#v", len(items), items)
			}
			if items[1].Label != project.Name || items[1].Href != "/bradley/projects/TRACK/all" || items[1].HXGet != "/bradley/projects/TRACK/all/panel" || items[1].Current {
				t.Fatalf("project breadcrumb = %#v, want linked project name", items[1])
			}
			if items[2].Label != tt.label || !items[2].Current {
				t.Fatalf("view breadcrumb = %#v, want current %q", items[2], tt.label)
			}
		})
	}
}

func TestUIBreadcrumbsIncludeOwnerForAnotherProfile(t *testing.T) {
	t.Parallel()

	project := model.Project{OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	issue := model.Issue{Identifier: "TRACK-20"}
	for name, breadcrumb := range map[string]uiBreadcrumbData{
		"project":       uiProjectBreadcrumb(project, "all", true),
		"issue":         uiIssueBreadcrumb(project, issue, nil, true),
		"issue context": uiIssueContextBreadcrumb(project, issue, nil, true),
	} {
		t.Run(name, func(t *testing.T) {
			items := breadcrumb.Items
			if len(items) < 3 {
				t.Fatalf("breadcrumb items = %#v, want owner hierarchy", items)
			}
			owner := items[1]
			if owner.Label != "@bradley" || owner.Href != "/bradley/projects" || owner.HXGet != "/bradley/projects/panel" || owner.Current {
				t.Fatalf("owner breadcrumb = %#v", owner)
			}
			if items[2].Label != project.Name {
				t.Fatalf("project breadcrumb = %#v, want project after owner", items[2])
			}
		})
	}
}

func TestUIProjectFavoriteViewKeepsSprintHistory(t *testing.T) {
	t.Parallel()
	if got := uiProjectPanelView("sprints"); got != "sprints" {
		t.Fatalf("uiProjectPanelView(sprints) = %q, want sprints", got)
	}
	if got := uiProjectPanelView("unknown"); got != "sprint" {
		t.Fatalf("uiProjectPanelView(unknown) = %q, want sprint", got)
	}
}

func TestUIProjectDeleteModalIsGatedAndSelfExplaining(t *testing.T) {
	t.Parallel()

	project := model.Project{OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	modal := uiProjectDeleteModal(&uiProjectPanelData{Project: project, View: "all"})
	if modal.ID != "project-delete" || !modal.Open || !modal.ClientControlled {
		t.Fatalf("delete modal = %#v", modal)
	}
	if modal.CancelHXGet != "/bradley/projects/TRACK/all/panel" || modal.CancelHXPushURL != "false" {
		t.Fatalf("delete modal cancel = %#v", modal)
	}
	if len(modal.Badges) != 1 || modal.Badges[0].Label != project.Key {
		t.Fatalf("delete modal badges = %#v", modal.Badges)
	}
	if !strings.Contains(modal.Description, project.Name) {
		t.Fatalf("delete modal description = %q, want the project named", modal.Description)
	}

	render := func(t *testing.T, panel *uiProjectPanelData) string {
		t.Helper()
		var buf bytes.Buffer
		panel.Project = project
		panel.View = "sprint"
		panel.ProjectTabs = uiProjectTabs(project, "sprint", nil)
		if err := uiTemplates.ExecuteTemplate(&buf, "project-panel", panel); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}

	hidden := render(t, &uiProjectPanelData{CanWrite: true})
	if strings.Contains(hidden, "Delete project") {
		t.Fatalf("delete action rendered without permission: %s", hidden)
	}
	closed := render(t, &uiProjectPanelData{CanWrite: true, CanDeleteProject: true})
	if !strings.Contains(closed, `href="/bradley/projects/TRACK/delete?view=sprint"`) {
		t.Fatalf("delete action missing from the project actions menu: %s", closed)
	}
	if strings.Contains(closed, `id="project-delete"`) {
		t.Fatalf("delete dialog rendered while closed: %s", closed)
	}
	// The dialog only opens for someone allowed to use it, so a stale flag on a
	// panel without permission cannot expose the form.
	denied := render(t, &uiProjectPanelData{CanWrite: true, DeleteProject: true})
	if strings.Contains(denied, `id="project-delete"`) {
		t.Fatalf("delete dialog rendered without permission: %s", denied)
	}
	open := render(t, &uiProjectPanelData{
		CSRFToken:          "csrf",
		CanWrite:           true,
		CanDeleteProject:   true,
		DeleteProject:      true,
		DeleteProjectInput: "TRAC",
		DeleteProjectError: "Type TRACK to confirm deletion.",
	})
	for _, want := range []string{
		`id="project-delete"`,
		`action="/bradley/projects/TRACK/delete"`,
		`name="key"`,
		`value="TRAC"`,
		"Type TRACK to confirm deletion.",
		`name="csrf_token"`,
	} {
		if !strings.Contains(open, want) {
			t.Fatalf("open delete dialog missing %q: %s", want, open)
		}
	}
}

func TestUIProjectPanelRendersCohesiveHeaderAndAboutDetails(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	selectedID := uuid.MustParse("23f14acb-6a57-4035-a046-33e93ffbd5bb")
	projectThumbnailID := uuid.MustParse("6a0d51f8-4a4f-46d5-8de1-726a7823d8f4")
	project := model.Project{
		ID:                     projectID,
		OwnerUsername:          "bradley",
		Key:                    "TRACK",
		Name:                   "Track Slash",
		Description:            "Fast issue tracking.",
		ImageThumbnailObjectID: &projectThumbnailID,
		CreatedAt:              time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC),
		UpdatedAt:              time.Date(2026, 6, 2, 10, 45, 0, 0, time.UTC),
	}
	selected := []uuid.UUID{selectedID}
	assignees := []model.ProjectAssignee{
		{ID: selectedID, Username: "ada", Name: "Ada Lovelace"},
	}
	tags := []model.IssueTag{
		uiTestIssueTag(projectID, 1, "Customer Beta", model.TagColorGreen),
	}
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "project-panel", &uiProjectPanelData{
		CanWrite:             true,
		Project:              project,
		View:                 "about",
		ProjectTabs:          uiProjectTabs(project, "about", selected),
		AssigneeFilters:      uiProjectAssigneeFilters(project, "about", assignees, selected),
		AssigneeFilterActive: true,
		ProjectStats: model.ProjectStats{
			ProjectID: projectID,
			AllTime: model.ProjectIssueStatusCounts{
				Total:      9,
				Todo:       3,
				InProgress: 2,
				Done:       3,
				Closed:     1,
			},
			Last7Days: model.ProjectIssueStatusCounts{
				Total:      4,
				Todo:       1,
				InProgress: 1,
				Done:       1,
				Closed:     1,
			},
			TopAssignees: []model.ProjectAssigneeIssueStats{{
				UserID:   selectedID,
				Username: "ada",
				Name:     "Ada Lovelace",
				Counts: model.ProjectIssueStatusCounts{
					Total:      5,
					Todo:       2,
					InProgress: 1,
					Done:       1,
					Closed:     1,
				},
			}},
		},
		Tags: tags,
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	projectHeaderStart := strings.Index(body, `<header class="rounded-lg`)
	if projectHeaderStart < 0 {
		t.Fatalf("project panel missing title card: %s", body)
	}
	headerEnd := strings.Index(body[projectHeaderStart:], "</header>")
	if headerEnd < 0 {
		t.Fatalf("project panel missing title card header: %s", body)
	}
	headerEnd += projectHeaderStart
	header := body[projectHeaderStart:headerEnd]
	contentStart := strings.Index(body, `<div id="project-panel-tab-content">`)
	if contentStart <= headerEnd {
		t.Fatalf("project tab content should render after the persistent project header: %s", body)
	}
	for _, want := range []string{`id="project-breadcrumb"`, `aria-label="Breadcrumb"`, `href="/projects"`, `hx-get="/projects/panel"`, `>Projects</a>`, `href="/bradley/projects/TRACK/all"`, `hx-get="/bradley/projects/TRACK/all/panel"`, `>Track Slash</a>`, `aria-current="page"`, `>About</span>`, `id="project-panel-tab-content"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project panel missing breadcrumb markup %q: %s", want, body)
		}
	}
	tabNav := strings.Index(body, `aria-label="Project views"`)
	if tabNav < 0 {
		t.Fatalf("project panel missing project view tabs: %s", body)
	}
	if tabNav < projectHeaderStart || tabNav > headerEnd {
		t.Fatalf("project view tabs should render inside title card: %s", body)
	}
	if strings.Contains(header, "Fast issue tracking.") {
		t.Fatalf("project description should not render inside title card: %s", body)
	}
	for _, want := range []string{"TRACK", "font-mono text-sm font-semibold uppercase", "Track Slash", "About", "Sprint", "Planned", "All", "Sprint history", "Changelog", `data-lucide="person-standing"`, `data-lucide="archive"`, `data-lucide="history"`, `data-lucide="info"`, `aria-current="page"`, `/bradley/projects/TRACK/image/thumbnail/content?v=` + projectThumbnailID.String(), `id="project-tab-bar"`, `id="project-actions-menu"`, `hx-target="#project-panel-tab-content"`, `hx-select="#project-panel-tab-content"`, `hx-select-oob="#project-breadcrumb,#project-tab-bar,#project-actions-menu,#project-favorite-action"`, `hx-swap="outerHTML"`, `aria-label="New issue"`, `href="/bradley/projects/TRACK/issues/new"`, `hx-get="/bradley/projects/TRACK/issues/new/panel"`, `aria-label="Project actions"`, `data-lucide="more-horizontal"`, "lg:mt-0 lg:border-t-0 lg:pt-0", `href="/bradley/projects/TRACK/sprints"`, `hx-get="/bradley/projects/TRACK/sprints/panel"`, `href="/bradley/projects/TRACK/deleted"`, `hx-get="/bradley/projects/TRACK/deleted/panel"`, `data-lucide="trash-2"`, "Deleted issues"} {
		if !strings.Contains(header, want) {
			t.Fatalf("project title card missing markup %q: %s", want, body)
		}
	}
	if strings.Contains(header, "hx-preserve") {
		t.Fatalf("project header should stay mounted instead of moving its image through HTMX preservation: %s", header)
	}
	for _, want := range []string{"Description", "Fast issue tracking.", "Issue stats", "All time", "Last 7 days", "Top assignees", "Ada Lovelace", "@ada", "AL", "Details", "Project image", `data-modal-open="project-image-picker"`, `id="project-image-picker" data-client-modal class="fixed inset-0 z-50 hidden`, `role="dialog" aria-modal="true" aria-labelledby="project-image-picker-title"`, "Change image", `action="/bradley/projects/TRACK/image"`, `hx-post="/bradley/projects/TRACK/image"`, `hx-encoding="multipart/form-data"`, `accept="image/png,image/jpeg,image/gif,image/webp,image/bmp"`, `action="/bradley/projects/TRACK/image/delete"`, "Remove current image", `/bradley/projects/TRACK/image/thumbnail/content?v=` + projectThumbnailID.String(), `rounded-md object-cover`, "Owner", "@bradley", "Tags", "#Customer Beta", `aria-label="Manage tags"`, `hx-get="/bradley/projects/TRACK/tags"`, "Created", "Jun 1, 2026 09:30", "Updated", "Jun 2, 2026 10:45"} {
		if !strings.Contains(body, want) {
			t.Fatalf("project about view missing markup %q: %s", want, body)
		}
	}
	if strings.Contains(body, "No assigned issues.") {
		t.Fatalf("project about populated stats rendered empty assignee state: %s", body)
	}
	if strings.Contains(header, ">Manage tags</span>") {
		t.Fatalf("project title card should not render manage tags in overflow menu: %s", body)
	}
	requireMarkupOrder(t, body, ">Details</h2>", ">Tags</dt>")
	// The Change image action sits on the row's trailing edge, centred on the
	// image, rather than hanging off the image's top edge.
	imageRowStart := strings.Index(body, ">Project image</dt>")
	imageRowEnd := strings.Index(body, ">Key</dt>")
	if imageRowStart < 0 || imageRowEnd < imageRowStart {
		t.Fatalf("project about view missing the project image row: %s", body)
	}
	imageRow := body[imageRowStart:imageRowEnd]
	projectThumbnail := `/bradley/projects/TRACK/image/thumbnail/content?v=` + projectThumbnailID.String()
	requireMarkupOrder(t, imageRow, `<dd class="mt-2 flex items-center justify-between gap-3">`, projectThumbnail)
	requireMarkupOrder(t, imageRow, projectThumbnail, `data-modal-open="project-image-picker"`)
	if strings.Contains(body, ">Context</dt>") || strings.Contains(body, `aria-label="Manage context"`) {
		t.Fatalf("project about should not render context in details: %s", body)
	}
	for _, notWant := range []string{`aria-label="Assignee filter"`, `assignee_id=`} {
		if strings.Contains(header, notWant) {
			t.Fatalf("project title card preserved about filter state %q: %s", notWant, body)
		}
		if strings.Contains(body, notWant) {
			t.Fatalf("project about view rendered assignee filter state %q: %s", notWant, body)
		}
	}
	if strings.Contains(body, "Back to projects") {
		t.Fatalf("project breadcrumb uses verbose label: %s", body)
	}
	tabEnd := strings.Index(body[tabNav:], "</nav>")
	if tabEnd < 0 {
		t.Fatalf("project tabs missing nav close: %s", body)
	}
	tabMarkup := body[tabNav : tabNav+tabEnd]
	if strings.Contains(tabMarkup, "Deleted") || strings.Contains(tabMarkup, `/deleted`) {
		t.Fatalf("deleted rendered as project tab: %s", body)
	}
	aboutIdx := strings.Index(tabMarkup, `href="/bradley/projects/TRACK/about"`)
	sprintsIdx := strings.Index(tabMarkup, `href="/bradley/projects/TRACK/sprint"`)
	plannedIdx := strings.Index(tabMarkup, `href="/bradley/projects/TRACK/planned"`)
	allIdx := strings.Index(tabMarkup, `href="/bradley/projects/TRACK/all"`)
	contextIdx := strings.Index(tabMarkup, `href="/bradley/projects/TRACK/context"`)
	if aboutIdx < 0 || sprintsIdx < 0 || plannedIdx < 0 || allIdx < 0 || contextIdx < 0 || sprintsIdx > plannedIdx || plannedIdx > allIdx || allIdx > contextIdx || contextIdx > aboutIdx {
		t.Fatalf("project tabs not ordered sprint, planned, all, context, about: sprint=%d planned=%d all=%d context=%d about=%d body=%s", sprintsIdx, plannedIdx, allIdx, contextIdx, aboutIdx, body)
	}
	for _, overflowOnly := range []string{`href="/bradley/projects/TRACK/sprints"`, `href="/bradley/projects/TRACK/changelog"`} {
		if strings.Contains(tabMarkup, overflowOnly) {
			t.Fatalf("overflow-only view rendered in project tabs %q: %s", overflowOnly, body)
		}
	}
	for _, path := range []string{"context", "about"} {
		href := `href="/bradley/projects/TRACK/` + path + `"`
		if got := strings.Count(header, href); got != 2 {
			t.Fatalf("project %s view should render once as a desktop tab and once in the mobile overflow menu; got %d: %s", path, got, body)
		}
	}
	if got := strings.Count(header, `href="/bradley/projects/TRACK/changelog"`); got != 1 {
		t.Fatalf("changelog should render once in project overflow; got %d: %s", got, body)
	}
	if got := strings.Count(header, `href="/bradley/projects/TRACK/sprints"`); got != 1 {
		t.Fatalf("sprint history should render once in project overflow; got %d: %s", got, body)
	}
	requireMarkupOrder(t, header, `href="/bradley/projects/TRACK/sprints"`, `href="/bradley/projects/TRACK/changelog"`)
	for _, want := range []string{"flex flex-nowrap", "gap-4 sm:gap-6", "hidden lg:inline-flex", "lg:hidden"} {
		if !strings.Contains(header, want) {
			t.Fatalf("project header missing responsive tab overflow markup %q: %s", want, body)
		}
	}
	for _, want := range []string{"About", "Sprint", "Planned", "All", `data-lucide="person-standing"`, `data-lucide="calendar-range"`, `data-lucide="list-filter"`, "border-b-4", `aria-current="page"`, `href="/bradley/projects/TRACK/about"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project panel missing tab markup %q: %s", want, body)
		}
	}
}

func TestUIProjectPanelRendersSprintHistory(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	completedAt := time.Date(2026, 7, 14, 16, 30, 0, 0, time.UTC)
	completedSprintID := uuid.New()
	legacySprintID := uuid.New()
	var buf bytes.Buffer
	panel := &uiProjectPanelData{
		Project:     project,
		View:        "sprints",
		ProjectTabs: uiProjectTabs(project, "sprints", nil),
		SprintHistoryPage: uiProjectSprintHistoryPageData{
			Project: project,
			Sprints: []model.Sprint{
				{
					ID:          completedSprintID,
					ProjectID:   projectID,
					Number:      7,
					Ref:         "sprint-7",
					Name:        "Completed release",
					Goal:        "Visible **sprint direction**",
					StartDate:   datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
					EndDate:     datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
					CompletedAt: &completedAt,
				},
				{ID: legacySprintID, ProjectID: projectID, Number: 8, Ref: "sprint-8", Name: "Undated legacy sprint"},
			},
			Descriptions: map[uuid.UUID]uiSprintDescriptionData{
				completedSprintID: {
					Project:         project,
					Sprint:          model.Sprint{ID: completedSprintID, ProjectID: projectID, Number: 7, Ref: "sprint-7", Goal: "Visible **sprint direction**"},
					DescriptionHTML: template.HTML("<p>Visible <strong>sprint direction</strong></p>"),
				},
			},
			StatusCounts: map[uuid.UUID]model.ProjectIssueStatusCounts{
				completedSprintID: {Total: 7, Todo: 2, InProgress: 1, Done: 3, Closed: 1},
			},
			HasMore:    true,
			NextCursor: "cursor123",
		},
	}
	if err := uiTemplates.ExecuteTemplate(&buf, "project-panel", panel); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`href="/bradley/projects/TRACK/sprints"`,
		`hx-get="/bradley/projects/TRACK/sprints/panel"`,
		`data-lucide="archive"`,
		`aria-current="page"`,
		"Sprint history",
		"Completed release",
		"Jun 1-Jun 14, 2026",
		`Completed <time datetime="2026-07-14T16:30:00Z" data-local-time title="Jul 14, 2026 16:30 UTC">Jul 14, 2026 16:30 UTC</time>`,
		"Undated legacy sprint",
		"No scheduled dates",
		"Completion date unavailable",
		`aria-label="Show issues"`,
		`data-disclosure-label>Show issues</span>`,
		`class="grid grid-cols-[minmax(0,1fr)_auto] items-start`,
		`class="flex justify-end border-t`,
		`aria-controls="completed-sprint-sprint-7-issues"`,
		`hx-get="/bradley/projects/TRACK/sprints/sprint-7/history/issues"`,
		`hx-trigger="click once"`,
		`aria-label="Sprint issue outcome counts"`,
		`aria-label="Done: 3"`,
		`aria-label="Cancelled: 1"`,
		"<strong>sprint direction</strong>",
		`id="sprint-sprint-7-description"`,
		`hx-get="/bradley/projects/TRACK/sprints/sprint-7/description?expanded=1"`,
		`hx-target="#sprint-sprint-7-description"`,
		"See more",
		"Loading sprint issues...",
		`hx-get="/bradley/projects/TRACK/sprints/page?cursor=cursor123"`,
		"Load more",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sprint history missing %q: %s", want, body)
		}
	}
	historyCard := `<section class="overflow-hidden rounded-lg border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">`
	if got := strings.Count(body, historyCard); got != 2 {
		t.Fatalf("sprint history card count = %d, want 2: %s", got, body)
	}
	if !strings.Contains(body, `<div class="space-y-4">`) {
		t.Fatalf("sprint history cards should render with standalone spacing: %s", body)
	}
	firstCard := strings.Index(body, historyCard)
	firstHeaderEnd := strings.Index(body[firstCard:], "</header>")
	description := strings.Index(body[firstCard:], `id="sprint-sprint-7-description"`)
	footer := strings.Index(body[firstCard:], "<footer")
	if firstCard < 0 || firstHeaderEnd < 0 || description < 0 || footer < 0 || description < firstHeaderEnd || description > footer {
		t.Fatalf("sprint description should render full-width between the card header and footer: card=%d header=%d description=%d footer=%d body=%s", firstCard, firstHeaderEnd, description, footer, body)
	}
	for _, notWant := range []string{"Visible **sprint direction**", `aria-label="Edit sprint"`, `aria-label="Add issue to sprint"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("sprint history included %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{`aria-label="To do:`, `aria-label="In progress:`, `aria-label="Closed:`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("sprint history included hidden outcome count %q: %s", notWant, body)
		}
	}
	completedTimeIndex := strings.Index(body, `<time datetime="2026-07-14T16:30:00Z" data-local-time`)
	outcomeCountsIndex := strings.Index(body, `aria-label="Sprint issue outcome counts"`)
	if completedTimeIndex < 0 || outcomeCountsIndex < completedTimeIndex {
		t.Fatalf("sprint outcome counts should render below completion time: time=%d counts=%d body=%s", completedTimeIndex, outcomeCountsIndex, body)
	}
	tabStart := strings.Index(body, `aria-label="Project views"`)
	if tabStart < 0 {
		t.Fatalf("project tabs missing: %s", body)
	}
	tabEnd := strings.Index(body[tabStart:], "</nav>")
	if tabEnd < 0 {
		t.Fatalf("project tabs missing: %s", body)
	}
	if strings.Contains(body[tabStart:tabStart+tabEnd], `href="/bradley/projects/TRACK/sprints"`) {
		t.Fatalf("sprint history rendered as a primary tab: %s", body)
	}

	buf.Reset()
	panel.SprintHistoryPage = uiProjectSprintHistoryPageData{Project: project}
	if err := uiTemplates.ExecuteTemplate(&buf, "project-panel", panel); err != nil {
		t.Fatalf("ExecuteTemplate empty: %v", err)
	}
	if emptyBody := buf.String(); !strings.Contains(emptyBody, "No completed sprints yet.") || strings.Contains(emptyBody, "Load more") {
		t.Fatalf("sprint history empty state = %s", emptyBody)
	}
}

func TestUISprintHistoryIssuePageRendersSnapshotIssues(t *testing.T) {
	t.Parallel()
	project := model.Project{OwnerUsername: "bradley", Key: "TRACK"}
	sprint := model.Sprint{Number: 7, Ref: "sprint-7", Goal: "Original **sprint direction**"}
	issue := model.Issue{
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Number:        42,
		Identifier:    "TRACK-42",
		Title:         "Captured at completion",
		Status:        model.StatusDone,
		Priority:      model.PriorityP1,
	}
	data := uiProjectSprintHistoryIssuePageData{
		Project:   project,
		Sprint:    sprint,
		Issues:    []uiIssueItem{{Issue: issue, Project: project}},
		NextHXGet: "/bradley/projects/TRACK/sprints/sprint-7/history/issues?cursor=next",
	}
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "project-sprint-history-issues", data); err != nil {
		t.Fatalf("ExecuteTemplate issues: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"Captured at completion",
		"TRACK-42",
		`href="/bradley/issues/TRACK-42"`,
		`hx-get="/bradley/issues/TRACK-42/panel"`,
		`hx-get="/bradley/projects/TRACK/sprints/sprint-7/history/issues?cursor=next"`,
		`hx-target="#completed-sprint-sprint-7-issues-more"`,
		"Load more issues",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sprint history issues missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "sprint direction") {
		t.Fatalf("sprint history issue response included description: %s", body)
	}

	buf.Reset()
	data.Issues = nil
	data.NextHXGet = ""
	if err := uiTemplates.ExecuteTemplate(&buf, "project-sprint-history-issues", data); err != nil {
		t.Fatalf("ExecuteTemplate empty issues: %v", err)
	}
	if emptyBody := buf.String(); !strings.Contains(emptyBody, "No issues were included in this sprint.") || strings.Contains(emptyBody, "Load more issues") {
		t.Fatalf("empty sprint history issues = %s", emptyBody)
	}
}

func TestUISprintHistoryDateRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		sprint model.Sprint
		want   string
	}{
		{name: "undated", sprint: model.Sprint{}, want: "No scheduled dates"},
		{name: "same year", sprint: model.Sprint{StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)), EndDate: datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC))}, want: "Jun 1-Jun 14, 2026"},
		{name: "cross year", sprint: model.Sprint{StartDate: datePtr(time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)), EndDate: datePtr(time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC))}, want: "Dec 29, 2025-Jan 4, 2026"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := uiSprintHistoryDateRange(tc.sprint); got != tc.want {
				t.Fatalf("uiSprintHistoryDateRange = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUIProjectPanelRendersChangelog(t *testing.T) {
	t.Parallel()

	projectID := uuid.New()
	issueID := uuid.New()
	actorID := uuid.New()
	project := model.Project{
		ID:            projectID,
		OwnerUsername: "bradley",
		Key:           "TRACK",
		Name:          "Track Slash",
		CreatedAt:     time.Date(2026, 6, 1, 9, 30, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 6, 2, 10, 45, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "project-panel", uiProjectPanelData{
		CanWrite:    true,
		Project:     project,
		View:        "changelog",
		ProjectTabs: uiProjectTabs(project, "changelog", nil),
		ChangelogPage: uiProjectChangelogPageData{
			Project: project,
			Entries: []model.ProjectChangelogEntry{{
				ID:          uuid.New(),
				ProjectID:   projectID,
				ActorID:     &actorID,
				Actor:       &model.ProjectChangelogActor{ID: actorID, Username: "ada", Name: "Ada Lovelace"},
				Entity:      "issue",
				Op:          "update",
				EntityID:    issueID,
				IssueID:     &issueID,
				TargetRef:   "TRACK-7",
				TargetTitle: "Better changelog",
				Summary:     "Updated issue TRACK-7",
				Details: model.ProjectChangelogDetails{Changes: []model.ProjectChangelogChange{{
					Field: "status",
					Label: "Status",
					From:  "To do",
					To:    "Done",
				}}},
				CreatedAt: time.Date(2026, 6, 3, 11, 0, 0, 0, time.UTC),
			}},
			HasMore:    true,
			NextCursor: "cursor123",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`data-project-changelog`,
		`data-project-id="` + projectID.String() + `"`,
		`data-refresh-url="/bradley/projects/TRACK/changelog/panel"`,
		`href="/bradley/projects/TRACK/changelog"`,
		`aria-current="page"`,
		`Updated issue TRACK-7`,
		`href="/bradley/issues/TRACK-7"`,
		`hx-get="/bradley/issues/TRACK-7/panel"`,
		`Better changelog`,
		`Ada Lovelace`,
		`Status`,
		`To do`,
		`Done`,
		`hx-get="/bradley/projects/TRACK/changelog/page?cursor=cursor123"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("changelog view missing %q: %s", want, body)
		}
	}
	tabStart := strings.Index(body, `aria-label="Project views"`)
	if tabStart < 0 {
		t.Fatalf("project tabs missing: %s", body)
	}
	tabEnd := strings.Index(body[tabStart:], "</nav>")
	if tabEnd < 0 {
		t.Fatalf("project tabs missing: %s", body)
	}
	tabMarkup := body[tabStart : tabStart+tabEnd]
	if strings.Contains(tabMarkup, "Deleted") || strings.Contains(tabMarkup, `/deleted`) {
		t.Fatalf("deleted rendered as project tab: %s", body)
	}
}

func TestUINewIssuePanelRendersAllCreateFields(t *testing.T) {
	t.Parallel()

	project := model.Project{ID: uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16"), OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "new-issue-panel", &uiNewIssuePanelData{
		Project:        project,
		HasProject:     true,
		ProjectID:      project.ID.String(),
		ProjectOptions: []model.Project{project},
		Error:          "Use YYYY-MM-DD.",
		Title:          "Draft issue",
		Description:    "Draft body",
		Priority:       string(model.PriorityP1),
		DueDate:        "tomorrow",
		AssigneeInput:  "@ada",
		ReporterInput:  "@grace",
		MemberOptions: []model.User{
			{Username: "ada", Email: "ada@example.com", Name: "Ada Lovelace"},
			{Username: "grace", Email: "grace@example.com", Name: "Grace Hopper"},
		},
		BackHref:  "/bradley/projects/TRACK/all",
		BackHXGet: "/bradley/projects/TRACK/all/panel",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		"New issue",
		"Create issue",
		"Use YYYY-MM-DD.",
		`data-new-issue-panel`,
		`id="new-issue-form" method="post" action="/issues"`,
		`hx-post="/issues"`,
		`hx-push-url="false"`,
		`id="new-issue-project-form" method="get" action="/issues/new/panel"`,
		`hx-get="/issues/new/panel"`,
		`hx-include="#issue-title,#issue-description,input[name='priority']:checked,#issue-due-date,#issue-assignee,#issue-reporter"`,
		`data-search`,
		`data-project-search`,
		`data-search-collapsible`,
		`data-search-clear-target="project_id"`,
		`id="issue-project" name="project" value="TRACK - Track Slash"`,
		`data-search-input`,
		`hx-trigger="input changed delay:300ms"`,
		`hx-target="#new-issue-project-options"`,
		`hx-swap="outerHTML"`,
		`hx-push-url="false"`,
		`hx-include="#new-issue-project-form"`,
		`type="hidden" name="project_id" value="8cc21ed4-2d69-4d43-9f0c-402736e4aa16"`,
		`id="new-issue-project-options"`,
		`data-search-options hidden role="listbox" aria-label="Project suggestions"`,
		`data-search-option`,
		`data-target-name="project_id"`,
		`data-target-value="8cc21ed4-2d69-4d43-9f0c-402736e4aa16"`,
		`data-search-text="TRACK Track Slash bradley"`,
		`hx-get="/issues/new/projects"`,
		`TRACK - Track Slash`,
		`id="issue-title" name="title" value="Draft issue"`,
		`id="issue-description" name="description"`,
		"Draft body",
		`id="issue-priority-label">Priority</span>`,
		`role="listbox" aria-labelledby="issue-priority-label" data-priority-picker`,
		`type="radio" name="priority" value="P0"`,
		`type="radio" name="priority" value="P1" checked`,
		`type="radio" name="priority" value="P2"`,
		`aria-label="Priority P1"`,
		`opacity-40`,
		`peer-checked:opacity-100`,
		`data-checkbox-reveal`,
		`id="issue-due-date-toggle" type="checkbox" data-checkbox-reveal-toggle aria-controls="issue-due-date-field" aria-expanded="true" checked`,
		`id="issue-due-date-field" data-checkbox-reveal-panel`,
		`type="date" name="due_date" value="tomorrow"`,
		`id="issue-assignee" name="assignee" value="@ada"`,
		`id="issue-reporter" name="reporter" value="@grace"`,
		`list="new-issue-members"`,
		`<datalist id="new-issue-members">`,
		`<option value="@ada">Ada Lovelace - ada@example.com</option>`,
		`aria-label="Cancel creating issue"`,
		`href="/bradley/projects/TRACK/all"`,
		`hx-get="/bradley/projects/TRACK/all/panel"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("new issue panel missing %q: %s", want, body)
		}
	}
	css, err := uiTemplateFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	for _, want := range []string{
		"@keyframes priority-picker-item-enter",
		"@media (prefers-reduced-motion:no-preference)",
		"[data-priority-picker]>:is(label,button):nth-child(5){animation-delay:80ms}",
	} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("new issue priority picker stylesheet missing %q", want)
		}
	}
}

func TestUINewIssueProjectFilter(t *testing.T) {
	t.Parallel()

	projects := []model.Project{
		{OwnerUsername: "bradley", Key: "CORE", Name: "Core Workflow"},
		{OwnerUsername: "bradley", Key: "OPS", Name: "Operations Desk"},
		{OwnerUsername: "ada", Key: "APP", Name: "Mobile Companion"},
	}
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty", want: []string{"CORE", "OPS", "APP"}},
		{name: "key", raw: "ops", want: []string{"OPS"}},
		{name: "name", raw: "mobile", want: []string{"APP"}},
		{name: "owner", raw: "ada", want: []string{"APP"}},
		{name: "multi token", raw: "core workflow", want: []string{"CORE"}},
		{name: "missing", raw: "zzz", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uiFilterNewIssueProjects(projects, tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, want := range tt.want {
				if got[i].Key != want {
					t.Fatalf("got[%d] = %s, want %s", i, got[i].Key, want)
				}
			}
		})
	}
}

func TestUIProjectAboutStatsEmptyTopAssignees(t *testing.T) {
	t.Parallel()

	project := model.Project{
		ID:            uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16"),
		OwnerUsername: "bradley",
		Key:           "TRACK",
		Name:          "Track Slash",
	}
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "project-panel", &uiProjectPanelData{
		CanWrite:     true,
		Project:      project,
		View:         "about",
		ProjectTabs:  uiProjectTabs(project, "about", nil),
		ProjectStats: model.ProjectStats{ProjectID: project.ID},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{"Issue stats", "Top assignees", "No assigned issues.", `<td class="px-4 py-2 text-right tabular-nums text-slate-700 dark:text-slate-300">0</td>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty project about stats missing %q: %s", want, body)
		}
	}
}

func TestUIProjectContextSurfacesRenderCompactAboutAndManagerRows(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	contextID := uuid.MustParse("845bc7de-5238-4df2-a024-799f9dbeb5fe")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	when := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	project := model.Project{
		ID:            projectID,
		OwnerUsername: "bradley",
		Key:           "TRACK",
		Name:          "Track Slash",
	}
	contextItem := uiProjectContextItem{
		Context: model.ProjectContextSummary{
			ID:               contextID,
			ProjectID:        projectID,
			Number:           1,
			Ref:              "context-1",
			Scope:            model.ProjectContextScopeProject,
			Title:            "Architecture notes",
			Kind:             model.ProjectContextKindText,
			ContentType:      "text/plain; charset=utf-8",
			LinkedIssueCount: 1,
			CreatedAt:        when,
			UpdatedAt:        when,
		},
	}
	linkedIssue := model.Issue{
		ID:            issueID,
		ProjectID:     projectID,
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Identifier:    "TRACK-8",
		Title:         "Linked work",
		Status:        model.StatusTodo,
		CreatedAt:     when,
		UpdatedAt:     when,
	}
	renderProject := func(panel *uiProjectPanelData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := uiTemplates.ExecuteTemplate(&buf, "project-panel", panel); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}
	renderManager := func(panel *uiContextManagerData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := uiTemplates.ExecuteTemplate(&buf, "context-manager-panel", panel); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}

	body := renderProject(&uiProjectPanelData{
		CanWrite:     true,
		Project:      project,
		View:         "about",
		ProjectTabs:  uiProjectTabs(project, "about", nil),
		ContextItems: []uiProjectContextItem{contextItem},
	})
	if strings.Contains(body, ">Context</dt>") || strings.Contains(body, `aria-label="Manage context"`) || strings.Contains(body, "Architecture notes") {
		t.Fatalf("project about should not render project context: %s", body)
	}

	managerItem := uiContextManagerItem{
		ID:          contextID,
		Ref:         "context-1",
		Number:      1,
		Scope:       model.ProjectContextScopeProject,
		Title:       "Architecture notes",
		ContentType: "text/plain; charset=utf-8",
		UpdatedAt:   when,
	}
	manager := &uiContextManagerData{
		CanWrite: true,
		Mode:     "project",
		Project:  project,
		Items:    []uiContextManagerItem{managerItem},
	}
	body = renderManager(manager)
	for _, want := range []string{"Context", "Architecture notes", `aria-label="Link issue"`, `hx-get="/bradley/projects/TRACK/context/context-1/issues/new"`, `hx-push-url="/bradley/projects/TRACK/context/context-1/issues/new"`, `aria-label="Edit context"`, `hx-push-url="/bradley/projects/TRACK/context/context-1/edit"`, `aria-label="Delete context"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project context manager row missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `font-mono`) {
		t.Fatalf("project context manager row should not render context refs as badges: %s", body)
	}
	for _, notWant := range []string{`placeholder="TRACK-12"`, "Linked work", `aria-label="Unlink issue"`, "Use the body."} {
		if strings.Contains(body, notWant) {
			t.Fatalf("project context manager row should stay compact, found %q: %s", notWant, body)
		}
	}

	activeContext := model.ProjectContext{ID: contextID, ProjectID: projectID, Number: 1, Ref: "context-1", Scope: model.ProjectContextScopeProject, Title: "Architecture notes", Kind: model.ProjectContextKindText, ContentType: "text/plain; charset=utf-8", Body: "Use the body.", UpdatedAt: when}
	linkManager := *manager
	linkManager.Action = "link"
	linkManager.ActiveContextID = contextID
	linkManager.ActiveContext = activeContext
	linkManager.LinkedIssues = []model.Issue{linkedIssue}
	linkManager.LinkedIssueCount = 1
	linkManager.LinkIssueInput = "TRACK-9"
	linkManager.LinkIssueError = "Issue already linked."
	body = renderManager(&linkManager)
	for _, want := range []string{`name="issue" value="TRACK-9" placeholder="TRACK-12" autofocus`, "Issue already linked.", "Linked issues", "TRACK-8", "Linked work", `aria-label="Unlink issue"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project context issue link manager missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `role="dialog" aria-modal="true"`) {
		t.Fatalf("project context issue link manager should not render modal: %s", body)
	}

	editManager := *manager
	editManager.Action = "edit"
	editManager.ActiveContextID = contextID
	editManager.ActiveContext = activeContext
	editManager.ContextEditTitle = "Architecture notes"
	editManager.ContextEditBody = "Use the body."
	body = renderManager(&editManager)
	for _, want := range []string{`action="/bradley/projects/TRACK/context/context-1"`, `value="Architecture notes"`, "Use the body.", `aria-label="Save context"`, `aria-label="Cancel editing context"`, `hx-replace-url="/bradley/projects/TRACK/context"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project context edit manager missing %q: %s", want, body)
		}
	}
}

func TestUIProjectContextRendersIntegratedMarkdownPages(t *testing.T) {
	t.Parallel()
	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	contextID := uuid.MustParse("845bc7de-5238-4df2-a024-799f9dbeb5fe")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	position := int64(1)
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	contextItem := model.ProjectContext{
		ID: contextID, ProjectID: projectID, Number: 1, Ref: "context-1", Scope: model.ProjectContextScopeProject,
		Position: &position, Title: "Architecture notes", Kind: model.ProjectContextKindText,
		ContentType: "text/markdown; charset=utf-8", Body: "# Architecture\n\nUse transactions.", UpdatedAt: time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC),
	}
	sourceFilename := "architecture.md"
	contextItem.SourceFilename = &sourceFilename
	manager := &uiContextManagerData{
		CanWrite: true,
		Mode:     "project", Project: project, Action: "view", ActiveContextID: contextID, HasActiveContext: true, ActiveContext: contextItem,
		ActiveHTML: template.HTML("<h1>Architecture</h1><p>Use transactions.</p>"),
		LinkedIssues: []model.Issue{{
			ID: issueID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Number: 8,
			Identifier: "TRACK-8", Title: "Linked work", Status: model.StatusTodo,
		}},
		LinkedIssueCount: 1,
		Items:            []uiContextManagerItem{{ID: contextID, Ref: "context-1", Number: 1, Scope: model.ProjectContextScopeProject, Position: &position, Title: "Architecture notes", ContentType: contextItem.ContentType}},
	}
	renderProject := func(panel *uiProjectPanelData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := uiTemplates.ExecuteTemplate(&buf, "project-panel", panel); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}
	body := renderProject(&uiProjectPanelData{
		CanWrite: true,
		Project:  project, View: "context", ProjectTabs: uiProjectTabs(project, "context", nil), ContextManager: manager,
	})
	for _, want := range []string{
		`<aside class="min-w-0 self-start overflow-hidden`,
		`aria-current="page"`, `href="/bradley/projects/TRACK/context/context-1"`, "Architecture notes",
		"<h1>Architecture</h1>", "Use transactions.", `aria-label="Move context page up"`,
		`aria-label="Move context page down"`, `aria-label="Manage linked issues"`, `aria-label="Edit context page"`,
		`aria-label="Delete context page"`, `href="/bradley/projects/TRACK/changelog"`,
		"architecture.md", `aria-labelledby="context-linked-issues-title"`, "Linked issues", "TRACK-8", "Linked work",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("integrated context panel missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `href="/bradley/projects/TRACK/changelog" hx-get="/bradley/projects/TRACK/changelog/panel" hx-target="#main" hx-push-url="/bradley/projects/TRACK/changelog"  class="hidden`) {
		t.Fatalf("changelog rendered as a tab: %s", body)
	}
	if strings.Contains(body, "text/markdown; charset=utf-8") {
		t.Fatalf("integrated context panel should not display MIME metadata: %s", body)
	}
	for _, notWant := range []string{`placeholder="TRACK-12"`, `aria-label="Unlink issue"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("project context view should render linked issues read-only, found %q: %s", notWant, body)
		}
	}
	navStart := strings.Index(body, `<nav aria-label="Context pages"`)
	if navStart < 0 {
		t.Fatalf("context page navigation missing: %s", body)
	}
	navEnd := strings.Index(body[navStart:], `</nav>`)
	if navEnd < 0 {
		t.Fatalf("context page navigation missing: %s", body)
	}
	contextNav := body[navStart : navStart+navEnd]
	if strings.Contains(contextNav, `inline-flex shrink-0 items-center rounded-md border`) {
		t.Fatalf("context page row should not render a linked-issue count: %s", contextNav)
	}

	readOnlyManager := *manager
	readOnlyManager.CanWrite = false
	body = renderProject(&uiProjectPanelData{
		Project: project, View: "context", ProjectTabs: uiProjectTabs(project, "context", nil), ContextManager: &readOnlyManager,
	})
	for _, want := range []string{"Linked issues", "TRACK-8", "Linked work"} {
		if !strings.Contains(body, want) {
			t.Fatalf("read-only project context missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{`aria-label="Manage linked issues"`, `aria-label="Edit context page"`, `aria-label="Delete context page"`, `aria-label="Unlink issue"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("read-only project context rendered mutation %q: %s", notWant, body)
		}
	}

	emptyManager := *manager
	emptyManager.LinkedIssues = nil
	emptyManager.LinkedIssueCount = 0
	body = renderProject(&uiProjectPanelData{
		CanWrite: true,
		Project:  project, View: "context", ProjectTabs: uiProjectTabs(project, "context", nil), ContextManager: &emptyManager,
	})
	for _, want := range []string{"Linked issues", ">0</span>", "No linked issues."} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty project context linked-issue section missing %q: %s", want, body)
		}
	}
}

func TestUIProjectPanelRendersAssigneeFilterAndSprintGoal(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	selectedID := uuid.MustParse("23f14acb-6a57-4035-a046-33e93ffbd5bb")
	otherID := uuid.MustParse("ae77b9b8-9dcf-4a18-8b69-42b97bd4a4b5")
	project := model.Project{
		ID:            projectID,
		OwnerUsername: "bradley",
		Key:           "TRACK",
		Name:          "Track Slash",
	}
	selected := []uuid.UUID{selectedID}
	assignees := []model.ProjectAssignee{
		{ID: selectedID, Username: "ada", Name: "Ada Lovelace"},
		{ID: otherID, Username: "grace", Name: "Grace Hopper"},
	}
	tags := []model.IssueTag{
		uiTestIssueTag(projectID, 1, "Sprint Visible", model.TagColorGreen),
	}
	columns := uiIssueColumns()
	columns[0].Issues = append(columns[0].Issues, uiIssueItem{
		Issue:   model.Issue{ID: uuid.MustParse("adbf2723-a44d-4b43-a3d0-e12276fa59c0"), ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-10", Title: "Todo count issue", Status: model.StatusTodo, Tags: tags},
		Project: project,
	})
	columns[1].Issues = append(columns[1].Issues, uiIssueItem{
		Issue:   model.Issue{ID: uuid.MustParse("af63e70c-bf9d-4f80-999d-df145379ec6d"), ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-11", Title: "Progress count issue", Status: model.StatusInProgress},
		Project: project,
	})
	sprintQuery := uiIssueListQuery{AssigneeIDs: selected}
	assigneeFilters := uiProjectSprintAssigneeFilters(project, assignees, sprintQuery)
	clearAssigneeHref := uiProjectSprintViewPath(project, uiIssueListQuery{})
	clearAssigneeHXGet := uiProjectSprintPanelPath(project, uiIssueListQuery{})
	activeSprint := model.Sprint{
		ID:        uuid.MustParse("d7fc0dbf-845c-41b4-84ab-89f487cc4a08"),
		ProjectID: projectID,
		Ref:       "sprint-1",
		Name:      "Current Sprint",
		Goal:      "Ship filtering\nPolish sprint goals",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	}

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "project-panel", &uiProjectPanelData{
		CanWrite:             true,
		Project:              project,
		View:                 "sprint",
		ProjectTabs:          uiProjectTabs(project, "sprint", selected),
		AssigneeFilters:      assigneeFilters,
		AssigneeFilterActive: true,
		ClearAssigneeHref:    clearAssigneeHref,
		ClearAssigneeHXGet:   clearAssigneeHXGet,
		ClearAssigneeHXPush:  clearAssigneeHref,
		SprintControls:       uiProjectSprintIssueControls(project, sprintQuery, nil, assigneeFilters, true, clearAssigneeHref, clearAssigneeHXGet, clearAssigneeHref),
		ActiveSprint:         &activeSprint,
		ActiveSprintDescription: uiSprintDescriptionData{
			Project:         project,
			Sprint:          activeSprint,
			DescriptionHTML: renderSprintDescriptionMarkdown(project, activeSprint, nil),
		},
		SprintColumns: columns,
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`aria-label="Issue controls"`,
		`aria-pressed="true"`,
		`aria-pressed="false"`,
		`aria-label="Toggle Ada Lovelace"`,
		`aria-label="Toggle Grace Hopper"`,
		"Status",
		"Priority",
		"Assignee",
		"Sort",
		"Direction",
		"Due date",
		"Desc",
		`data-lucide="arrow-down"`,
		"AL",
		"GH",
		"Ship filtering",
		`data-sprint-ref`,
		`>sprint-1</span>`,
		"Todo count issue",
		"#Sprint Visible",
		"border-green-200 bg-green-50 text-green-700",
		"Progress count issue",
		"-mt-3 max-h-20 overflow-hidden",
		"See more",
		`href="/bradley/projects/TRACK/sprint?assignee_id=23f14acb-6a57-4035-a046-33e93ffbd5bb"`,
		`hx-get="/bradley/projects/TRACK/planned/panel"`,
		`hx-get="/bradley/projects/TRACK/all/panel"`,
		`href="/bradley/projects/TRACK/sprint"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("project panel missing %q: %s", want, body)
		}
	}
	if got := strings.Count(body, `data-sprint-ref`); got != 1 {
		t.Fatalf("active sprint ref badges = %d, want 1: %s", got, body)
	}
	filterIdx := strings.Index(body, `aria-label="Issue controls"`)
	tabIdx := strings.Index(body, `aria-label="Project views"`)
	sprintTitleIdx := strings.Index(body, "Current Sprint")
	boardIdx := strings.Index(body, `class="grid gap-4 lg:grid-cols-3"`)
	if filterIdx < 0 || tabIdx < 0 || filterIdx < tabIdx {
		t.Fatalf("issue controls should render below project tabs: filter=%d tabs=%d body=%s", filterIdx, tabIdx, body)
	}
	if sprintTitleIdx < 0 || boardIdx < 0 || filterIdx < sprintTitleIdx || filterIdx > boardIdx {
		t.Fatalf("issue controls should render below sprint title and above board: sprint=%d filter=%d board=%d body=%s", sprintTitleIdx, filterIdx, boardIdx, body)
	}
	for _, notWant := range []string{`href="/bradley/projects/TRACK/about?assignee_id=`, `hx-get="/bradley/projects/TRACK/about/panel?assignee_id=`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("about tab should not preserve assignee filter %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{`href="/bradley/projects/TRACK/planned?assignee_id=`, `href="/bradley/projects/TRACK/all?assignee_id=`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("project work tabs should not preserve sprint assignee filter %q: %s", notWant, body)
		}
	}
	requireInlineCountText(t, body, "Sprint", "2 Issues")
	requireInlineCount(t, body, "To do", 1)
	requireInlineCount(t, body, "In progress", 1)
	requireInlineCount(t, body, "Done", 0)
}

func TestUIProjectPanelRendersActiveSprintDates(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name      string
		startDate *time.Time
		endDate   *time.Time
		want      string
	}{
		{name: "no dates", want: "No scheduled dates"},
		{name: "start only", startDate: &start, want: "Jun 1"},
		{name: "end only", endDate: &end, want: "Jun 14"},
		{name: "both dates", startDate: &start, endDate: &end, want: "Jun 1-Jun 14"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			activeSprint := model.Sprint{
				Ref:       "sprint-1",
				Name:      "Current Sprint",
				StartDate: tt.startDate,
				EndDate:   tt.endDate,
			}
			var buf bytes.Buffer
			if err := uiTemplates.ExecuteTemplate(&buf, "project-panel-sprint", &uiProjectPanelData{
				Project:      model.Project{OwnerUsername: "bradley", Key: "TRACK"},
				ActiveSprint: &activeSprint,
			}); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}

			body := buf.String()
			want := `<p class="mt-1 text-sm text-slate-500 dark:text-slate-400">` + tt.want + `</p>`
			if !strings.Contains(body, want) {
				t.Fatalf("active sprint dates missing %q: %s", want, body)
			}
			if tt.startDate != nil || tt.endDate != nil {
				if strings.Contains(body, "No scheduled dates") {
					t.Fatalf("partial or complete active sprint dates rendered as unscheduled: %s", body)
				}
			}
		})
	}
}

func TestUIProjectPanelHidesIssueControlsWithoutActiveSprint(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "project-panel-sprint", &uiProjectPanelData{
		Project: model.Project{OwnerUsername: "bradley", Key: "TRACK"},
	}); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	if !strings.Contains(body, "No active sprint.") {
		t.Fatalf("sprint panel missing no-active guidance: %s", body)
	}
	for _, notWant := range []string{`aria-label="Issue controls"`, "No active sprint issues.", `class="grid gap-4 lg:grid-cols-3"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("sprint panel without an active sprint included %q: %s", notWant, body)
		}
	}
}
