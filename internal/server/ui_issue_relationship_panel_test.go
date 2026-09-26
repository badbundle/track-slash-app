package server

import (
	"bytes"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestUIIssuePanelCollapsesEmptyRelationshipSections(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	linkedID := uuid.MustParse("ae77b9b8-9dcf-4a18-8b69-42b97bd4a4b5")
	when := time.Date(2026, 6, 6, 12, 30, 0, 0, time.UTC)
	basePanel := func() uiIssuePanelData {
		return uiIssuePanelData{
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
			Project:   model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
			BackHref:  "/bradley/projects/TRACK/backlog",
			BackHXGet: "/bradley/projects/TRACK/backlog/panel",
			BackLabel: "Backlog",
		}
	}
	render := func(t *testing.T, panel uiIssuePanelData) string {
		t.Helper()
		var buf bytes.Buffer
		if err := uiTemplates.ExecuteTemplate(&buf, "issue-panel", &panel); err != nil {
			t.Fatalf("ExecuteTemplate: %v", err)
		}
		return buf.String()
	}
	requireHeadingOrder := func(t *testing.T, body, first, second string) {
		t.Helper()
		firstIndex := strings.Index(body, ">"+first+"</h2>")
		secondIndex := strings.Index(body, ">"+second+"</h2>")
		if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
			t.Fatalf("heading %q should render before %q: %s", first, second, body)
		}
	}
	requireTextOrder := func(t *testing.T, body, first, second string) {
		t.Helper()
		firstIndex := strings.Index(body, first)
		secondIndex := strings.Index(body, second)
		if firstIndex < 0 || secondIndex < 0 || firstIndex > secondIndex {
			t.Fatalf("%q should render before %q: %s", first, second, body)
		}
	}
	contextDetailBlock := func(t *testing.T, body string) string {
		t.Helper()
		contextLabel := strings.Index(body, ">Context</dt>")
		if contextLabel < 0 {
			t.Fatalf("missing context detail row: %s", body)
		}
		blockEnd := contextLabel + 1100
		if blockEnd > len(body) {
			blockEnd = len(body)
		}
		return body[contextLabel:blockEnd]
	}

	emptyBody := render(t, basePanel())
	for _, notWant := range []string{"No context.", "No sub-issues.", "No linked issues."} {
		if strings.Contains(emptyBody, notWant) {
			t.Fatalf("empty relationship section should not render %q: %s", notWant, emptyBody)
		}
	}
	if got := strings.Count(emptyBody, `class="flex flex-wrap gap-6"`); got != 1 {
		t.Fatalf("relationship sections should render one wrapping row, got %d: %s", got, emptyBody)
	}
	contextDetail := contextDetailBlock(t, emptyBody)
	for _, want := range []string{`aria-label="Manage context"`, `data-lucide="book-open"`, `hx-push-url="/bradley/issues/TRACK-7/context"`, `class="` + uiCountBadgeClass + `">0</span>`} {
		if !strings.Contains(contextDetail, want) {
			t.Fatalf("empty context detail row missing %q: %s", want, emptyBody)
		}
	}
	for _, notWant := range []string{`aria-label="Add context"`, `aria-label="Attach context"`, `aria-label="Remove context"`, `data-lucide="plus"`, `data-lucide="link"`} {
		if strings.Contains(contextDetail, notWant) {
			t.Fatalf("context detail row should show only count/view affordance, found %q: %s", notWant, emptyBody)
		}
	}
	if strings.Contains(emptyBody, `placeholder="Search context by title"`) {
		t.Fatalf("empty issue page should keep attach form in the manager only: %s", emptyBody)
	}
	if got := strings.Count(emptyBody, `w-full sm:w-auto sm:flex-1`); got != 2 {
		t.Fatalf("empty relationship sections should share the row, got %d: %s", got, emptyBody)
	}
	emptySubClass := sectionClassForHeading(t, emptyBody, "Sub-issues")
	emptyLinkClass := sectionClassForHeading(t, emptyBody, "Linked issues")
	for _, cls := range []string{emptySubClass, emptyLinkClass} {
		if !strings.Contains(cls, `w-full sm:w-auto sm:flex-1`) {
			t.Fatalf("empty relationship section should share the row, got class %q: %s", cls, emptyBody)
		}
	}
	requireHeadingOrder(t, emptyBody, "Sub-issues", "Linked issues")
	requireHeadingOrder(t, emptyBody, "Linked issues", "Comments")
	requireTextOrder(t, emptyBody, ">Comments</h2>", ">Details</h2>")
	requireTextOrder(t, emptyBody, ">Details</h2>", ">Context</dt>")

	populatedContextPanel := basePanel()
	populatedContextPanel.Contexts = []model.ProjectContext{{
		ID:          uuid.MustParse("845bc7de-5238-4df2-a024-799f9dbeb5fe"),
		ProjectID:   projectID,
		Number:      1,
		Ref:         "context-1",
		Title:       "Agent notes",
		Kind:        model.ProjectContextKindText,
		ContentType: "text/plain; charset=utf-8",
		Body:        "Use the compact path.",
		CreatedAt:   when,
		UpdatedAt:   when,
	}}
	populatedContextBody := render(t, populatedContextPanel)
	populatedContextDetail := contextDetailBlock(t, populatedContextBody)
	if !strings.Contains(populatedContextDetail, `class="`+uiCountBadgeClass+`">1</span>`) {
		t.Fatalf("populated context detail should show count only: %s", populatedContextBody)
	}
	for _, notWant := range []string{"Agent notes", "Use the compact path.", `aria-label="Remove context"`} {
		if strings.Contains(populatedContextBody, notWant) {
			t.Fatalf("populated issue page should keep context details in manager, found %q: %s", notWant, populatedContextBody)
		}
	}

	populatedLinksPanel := basePanel()
	populatedLinksPanel.Links = []uiIssueLinkItem{{
		Link:        model.IssueLink{ID: uuid.MustParse("48c98f2e-bad8-4054-89d7-5a45a68af54f"), ProjectID: projectID, Number: 1, Ref: "link-1", SourceID: issueID, TargetID: linkedID, LinkType: model.LinkTypeRelatesTo, CreatedAt: when, UpdatedAt: when},
		LinkedIssue: model.Issue{ID: linkedID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-8", Title: "Linked work", Status: model.StatusTodo},
		HasIssue:    true,
	}}
	populatedLinksBody := render(t, populatedLinksPanel)
	populatedSubClass := sectionClassForHeading(t, populatedLinksBody, "Sub-issues")
	populatedLinkClass := sectionClassForHeading(t, populatedLinksBody, "Linked issues")
	if !strings.Contains(populatedSubClass, `w-full sm:w-auto sm:flex-1`) {
		t.Fatalf("empty sub-issues section should sit below populated links at third width, got %q: %s", populatedSubClass, populatedLinksBody)
	}
	if !strings.Contains(populatedLinkClass, "w-full") || strings.Contains(populatedLinkClass, "sm:w-[calc") {
		t.Fatalf("populated linked issues section should remain full width above the empty one, got %q: %s", populatedLinkClass, populatedLinksBody)
	}
	requireHeadingOrder(t, populatedLinksBody, "Linked issues", "Sub-issues")
	requireHeadingOrder(t, populatedLinksBody, "Sub-issues", "Comments")
	requireTextOrder(t, populatedLinksBody, ">Comments</h2>", ">Details</h2>")
	requireTextOrder(t, populatedLinksBody, ">Details</h2>", ">Context</dt>")

	populatedSubIssuesPanel := basePanel()
	populatedSubIssuesPanel.SubIssues = []model.Issue{{
		ID:            uuid.MustParse("1e533f98-310a-4090-a8ff-7cc4c4a69df2"),
		ProjectID:     projectID,
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Identifier:    "TRACK-8",
		Title:         "Existing child",
		Status:        model.StatusTodo,
		Priority:      model.PriorityP2,
		CreatedAt:     when,
		UpdatedAt:     when,
	}}
	populatedSubIssuesBody := render(t, populatedSubIssuesPanel)
	populatedSubIssuesClass := sectionClassForHeading(t, populatedSubIssuesBody, "Sub-issues")
	populatedEmptyLinkClass := sectionClassForHeading(t, populatedSubIssuesBody, "Linked issues")
	if !strings.Contains(populatedSubIssuesClass, "w-full") || strings.Contains(populatedSubIssuesClass, "sm:w-[calc") {
		t.Fatalf("populated sub-issues section should remain full width above the empty one, got %q: %s", populatedSubIssuesClass, populatedSubIssuesBody)
	}
	if !strings.Contains(populatedEmptyLinkClass, `w-full sm:w-auto sm:flex-1`) {
		t.Fatalf("empty linked issues section should sit below populated sub-issues at third width, got %q: %s", populatedEmptyLinkClass, populatedSubIssuesBody)
	}
	requireHeadingOrder(t, populatedSubIssuesBody, "Sub-issues", "Linked issues")
	requireHeadingOrder(t, populatedSubIssuesBody, "Linked issues", "Comments")
	requireTextOrder(t, populatedSubIssuesBody, ">Comments</h2>", ">Details</h2>")
	requireTextOrder(t, populatedSubIssuesBody, ">Details</h2>", ">Context</dt>")
}

func TestUIIssuePanelRendersSubIssueComposerAtTop(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	childID := uuid.MustParse("1e533f98-310a-4090-a8ff-7cc4c4a69df2")
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
		Project:       model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		AddSubIssue:   true,
		SubIssueTitle: "Draft child",
		SubIssueError: "Title required, max 200 chars.",
		SubIssues: []model.Issue{{
			ID:            childID,
			ProjectID:     projectID,
			OwnerUsername: "bradley",
			ProjectKey:    "TRACK",
			Identifier:    "TRACK-8",
			Title:         "Existing child",
			Status:        model.StatusTodo,
			Priority:      model.PriorityP2,
			CreatedAt:     when,
			UpdatedAt:     when,
		}},
		BackHref:  "/bradley/projects/TRACK/backlog",
		BackHXGet: "/bradley/projects/TRACK/backlog/panel",
		BackLabel: "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`id="issue-sub-issue-create" data-client-modal class="fixed inset-0 z-50 grid`,
		`role="dialog" aria-modal="true" aria-labelledby="issue-sub-issue-create-title"`,
		`aria-label="Cancel adding sub-issue"`,
		`method="post" action="/bradley/issues/TRACK-7/sub-issues"`,
		`hx-post="/bradley/issues/TRACK-7/sub-issues"`,
		`hx-push-url="false"`,
		`name="title" value="Draft child" autofocus placeholder="Issue title"`,
		`Create sub-issue`,
		"Title required, max 200 chars.",
		"Existing child",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sub-issue composer missing %q: %s", want, body)
		}
	}
	formIndex := strings.Index(body, `name="title" value="Draft child"`)
	childIndex := strings.Index(body, "Existing child")
	if formIndex < 0 || childIndex < 0 || formIndex > childIndex {
		t.Fatalf("sub-issue composer should render before the list rows: %s", body)
	}
	for _, want := range []string{`data-modal-return="issue-sub-issue-create"`, `aria-label="Add sub-issue"`, `hx-get="/bradley/issues/TRACK-7/sub-issues/new"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("sub-issue modal missing persistent trigger %q: %s", want, body)
		}
	}
}

func TestUIIssuePanelRendersAddLinkForm(t *testing.T) {
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
			Status:        model.StatusTodo,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project:      model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		AddLink:      true,
		LinkTarget:   "TRACK-8",
		LinkRelation: "blocked_by",
		LinkError:    "Linked issue required.",
		BackHref:     "/bradley/projects/TRACK/backlog",
		BackHXGet:    "/bradley/projects/TRACK/backlog/panel",
		BackLabel:    "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`id="issue-link-create" data-client-modal class="fixed inset-0 z-50 grid`,
		`role="dialog" aria-modal="true" aria-labelledby="issue-link-create-title"`,
		`method="post" action="/bradley/issues/TRACK-7/links"`,
		`hx-post="/bradley/issues/TRACK-7/links"`,
		`hx-target="#main"`,
		`hx-push-url="false"`,
		`name="relation" class=`,
		`value="relates_to"`,
		`value="blocks"`,
		`value="blocked_by" selected`,
		`value="duplicates"`,
		`value="duplicated_by"`,
		`value="clones"`,
		`value="cloned_by"`,
		`name="target_issue" value="TRACK-8" placeholder="TRACK-12"`,
		`aria-label="Cancel adding issue link"`,
		`>Add link</button>`,
		"Linked issue required.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("add link form missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{
		"No linked issues.",
		`title="Save link"`,
		`title="Cancel adding link"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("add link form included %q: %s", notWant, body)
		}
	}
	for _, want := range []string{`data-modal-return="issue-link-create"`, `hx-get="/bradley/issues/TRACK-7/links/new"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("add link modal missing persistent trigger %q: %s", want, body)
		}
	}
}

func TestUIIssuePanelRendersLinkEditForm(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	linkedID := uuid.MustParse("ae77b9b8-9dcf-4a18-8b69-42b97bd4a4b5")
	linkID := uuid.MustParse("48c98f2e-bad8-4054-89d7-5a45a68af54f")
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
			Status:        model.StatusTodo,
			CreatedAt:     when,
			UpdatedAt:     when,
		},
		Project: model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"},
		Links: []uiIssueLinkItem{{
			Link:        model.IssueLink{ID: linkID, ProjectID: projectID, Number: 3, Ref: "link-3", SourceID: linkedID, TargetID: issueID, LinkType: model.LinkTypeClones, CreatedAt: when, UpdatedAt: when},
			LinkedIssue: model.Issue{ID: linkedID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-8", Title: "Linked work", Status: model.StatusInProgress},
			HasIssue:    true,
		}},
		EditLinkID:   linkID,
		LinkTarget:   "TRACK-8",
		LinkRelation: "cloned_by",
		LinkError:    "Link already exists or cannot be updated.",
		BackHref:     "/bradley/projects/TRACK/backlog",
		BackHXGet:    "/bradley/projects/TRACK/backlog/panel",
		BackLabel:    "Backlog",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`method="post" action="/bradley/issues/TRACK-7/links/link-3"`,
		`hx-post="/bradley/issues/TRACK-7/links/link-3"`,
		`hx-target="#main"`,
		`hx-push-url="false"`,
		`name="relation" aria-label="Link relationship"`,
		`value="cloned_by" selected`,
		`name="target_issue" value="TRACK-8" placeholder="TRACK-12"`,
		`aria-label="Save link"`,
		`aria-label="Cancel editing link"`,
		`aria-label="Remove link"`,
		`hx-post="/bradley/issues/TRACK-7/links/link-3/delete"`,
		`data-lucide="trash-2"`,
		"Link already exists or cannot be updated.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("edit link form missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{
		`hx-get="/bradley/issues/TRACK-7/links/link-3/edit"`,
		`title="Remove link"`,
		`title="Cancel editing link"`,
	} {
		if strings.Contains(body, notWant) {
			t.Fatalf("edit link form included %q: %s", notWant, body)
		}
	}
}

func TestUIIssueBackLink(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	sprintID := uuid.MustParse("d7fc0dbf-845c-41b4-84ab-89f487cc4a08")
	parentID := uuid.MustParse("2eeaf29c-ad20-4513-af41-edbb2c9abc2c")
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash", SprintsEnabled: true}
	withoutSprints := project
	withoutSprints.SprintsEnabled = false
	baseIssue := model.Issue{ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-7", SprintID: &sprintID}

	tests := []struct {
		name      string
		project   *model.Project
		issue     model.Issue
		sprint    *model.Sprint
		parent    *model.Issue
		wantHref  string
		wantHXGet string
		wantLabel string
	}{
		{
			name:      "active sprint",
			issue:     baseIssue,
			sprint:    &model.Sprint{ID: sprintID, ProjectID: projectID, Status: model.SprintStatusActive},
			wantHref:  "/bradley/projects/TRACK/sprint",
			wantHXGet: "/bradley/projects/TRACK/sprint/panel",
			wantLabel: "Sprint",
		},
		{
			name:      "planned sprint",
			issue:     baseIssue,
			sprint:    &model.Sprint{ID: sprintID, ProjectID: projectID, Status: model.SprintStatusPlanned},
			wantHref:  "/bradley/projects/TRACK/planned",
			wantHXGet: "/bradley/projects/TRACK/planned/panel",
			wantLabel: "Planned",
		},
		{
			// Planned is hidden without sprint mode, so the issue goes back to All.
			name:      "planned sprint without sprint mode",
			project:   &withoutSprints,
			issue:     baseIssue,
			sprint:    &model.Sprint{ID: sprintID, ProjectID: projectID, Status: model.SprintStatusPlanned},
			wantHref:  "/bradley/projects/TRACK/all",
			wantHXGet: "/bradley/projects/TRACK/all/panel",
			wantLabel: "All issues",
		},
		{
			name:      "backlog issue",
			issue:     model.Issue{ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-8"},
			wantHref:  "/bradley/projects/TRACK/all",
			wantHXGet: "/bradley/projects/TRACK/all/panel",
			wantLabel: "All issues",
		},
		{
			name:      "completed sprint",
			issue:     baseIssue,
			sprint:    &model.Sprint{ID: sprintID, ProjectID: projectID, Status: model.SprintStatusCompleted},
			wantHref:  "/bradley/projects/TRACK/all",
			wantHXGet: "/bradley/projects/TRACK/all/panel",
			wantLabel: "All issues",
		},
		{
			name:      "missing sprint",
			issue:     baseIssue,
			wantHref:  "/bradley/projects/TRACK/all",
			wantHXGet: "/bradley/projects/TRACK/all/panel",
			wantLabel: "All issues",
		},
		{
			name:      "parent issue",
			issue:     model.Issue{ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-9", ParentIssueID: &parentID},
			parent:    &model.Issue{ID: parentID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Identifier: "TRACK-1"},
			wantHref:  "/bradley/issues/TRACK-1",
			wantHXGet: "/bradley/issues/TRACK-1/panel",
			wantLabel: "Parent issue",
		},
	}

	for _, tt := range tests {
		issueProject := project
		if tt.project != nil {
			issueProject = *tt.project
		}
		href, hxGet, label := uiIssueBackLink(issueProject, tt.issue, tt.parent, tt.sprint)
		if href != tt.wantHref || hxGet != tt.wantHXGet || label != tt.wantLabel {
			t.Fatalf("%s: got (%q, %q, %q), want (%q, %q, %q)", tt.name, href, hxGet, label, tt.wantHref, tt.wantHXGet, tt.wantLabel)
		}
	}
}

func TestUIIssueLinkLabel(t *testing.T) {
	t.Parallel()

	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	otherID := uuid.MustParse("ae77b9b8-9dcf-4a18-8b69-42b97bd4a4b5")

	tests := []struct {
		name string
		link model.IssueLink
		want string
	}{
		{name: "blocks outgoing", link: model.IssueLink{SourceID: issueID, TargetID: otherID, LinkType: model.LinkTypeBlocks}, want: "Blocks"},
		{name: "blocks incoming", link: model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeBlocks}, want: "Blocked by"},
		{name: "duplicates outgoing", link: model.IssueLink{SourceID: issueID, TargetID: otherID, LinkType: model.LinkTypeDuplicates}, want: "Duplicates"},
		{name: "duplicates incoming", link: model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeDuplicates}, want: "Duplicated by"},
		{name: "relates outgoing", link: model.IssueLink{SourceID: issueID, TargetID: otherID, LinkType: model.LinkTypeRelatesTo}, want: "Relates to"},
		{name: "relates incoming", link: model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeRelatesTo}, want: "Relates to"},
		{name: "clones outgoing", link: model.IssueLink{SourceID: issueID, TargetID: otherID, LinkType: model.LinkTypeClones}, want: "Clones"},
		{name: "clones incoming", link: model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeClones}, want: "Cloned by"},
		{name: "unknown", link: model.IssueLink{SourceID: issueID, TargetID: otherID, LinkType: model.LinkType("custom")}, want: "custom"},
	}

	for _, tt := range tests {
		if got := uiIssueLinkLabel(tt.link, issueID); got != tt.want {
			t.Fatalf("%s: uiIssueLinkLabel = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestUIIssueLinkRelationParams(t *testing.T) {
	t.Parallel()

	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	otherID := uuid.MustParse("ae77b9b8-9dcf-4a18-8b69-42b97bd4a4b5")

	tests := []struct {
		name       string
		relation   string
		wantSource uuid.UUID
		wantTarget uuid.UUID
		wantType   model.LinkType
	}{
		{name: "blocks", relation: "blocks", wantSource: issueID, wantTarget: otherID, wantType: model.LinkTypeBlocks},
		{name: "blocked by", relation: "blocked_by", wantSource: otherID, wantTarget: issueID, wantType: model.LinkTypeBlocks},
		{name: "duplicates", relation: "duplicates", wantSource: issueID, wantTarget: otherID, wantType: model.LinkTypeDuplicates},
		{name: "duplicated by", relation: "duplicated_by", wantSource: otherID, wantTarget: issueID, wantType: model.LinkTypeDuplicates},
		{name: "relates", relation: "relates_to", wantSource: issueID, wantTarget: otherID, wantType: model.LinkTypeRelatesTo},
		{name: "clones", relation: "clones", wantSource: issueID, wantTarget: otherID, wantType: model.LinkTypeClones},
		{name: "cloned by", relation: "cloned_by", wantSource: otherID, wantTarget: issueID, wantType: model.LinkTypeClones},
	}

	for _, tt := range tests {
		sourceID, targetID, linkType, ok := uiIssueLinkRelationParams(issueID, otherID, tt.relation)
		if !ok || sourceID != tt.wantSource || targetID != tt.wantTarget || linkType != tt.wantType {
			t.Fatalf("%s: got (%s, %s, %s, %v), want (%s, %s, %s, true)", tt.name, sourceID, targetID, linkType, ok, tt.wantSource, tt.wantTarget, tt.wantType)
		}
	}
	if _, _, _, ok := uiIssueLinkRelationParams(issueID, otherID, "blocks_by_magic"); ok {
		t.Fatalf("invalid relation unexpectedly accepted")
	}

	if got := uiIssueLinkRelation(model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeBlocks}, issueID); got != "blocked_by" {
		t.Fatalf("incoming blocks relation = %q, want blocked_by", got)
	}
	if got := uiIssueLinkRelation(model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeDuplicates}, issueID); got != "duplicated_by" {
		t.Fatalf("incoming duplicates relation = %q, want duplicated_by", got)
	}
	if got := uiIssueLinkRelation(model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeClones}, issueID); got != "cloned_by" {
		t.Fatalf("incoming clones relation = %q, want cloned_by", got)
	}
	if got := uiIssueLinkRelation(model.IssueLink{SourceID: otherID, TargetID: issueID, LinkType: model.LinkTypeRelatesTo}, issueID); got != "relates_to" {
		t.Fatalf("incoming relates relation = %q, want relates_to", got)
	}
	if got := uiIssueLinkRelation(model.IssueLink{SourceID: issueID, TargetID: otherID, LinkType: model.LinkTypeBlocks}, issueID); got != "blocks" {
		t.Fatalf("outgoing blocks relation = %q, want blocks", got)
	}
}
