package server

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
)

func renderWhiteboardPanelForTest(t *testing.T, whiteboard *uiWhiteboardData) string {
	t.Helper()
	panel := &uiProjectPanelData{
		CSRFToken:   "csrf",
		Project:     whiteboard.Project,
		View:        "whiteboard",
		CanWrite:    whiteboard.CanWrite,
		ProjectTabs: uiProjectTabs(whiteboard.Project, "whiteboard", nil),
		Whiteboard:  whiteboard,
	}
	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "project-panel", panel); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	return buf.String()
}

func whiteboardFixture(canWrite bool) *uiWhiteboardData {
	project := model.Project{ID: uuid.New(), OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	when := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	older := model.WhiteboardPageSummary{ID: uuid.New(), ProjectID: project.ID, Number: 1, Ref: "whiteboard-1", Title: "Launch ideas", UpdatedAt: when}
	newer := model.WhiteboardPageSummary{ID: uuid.New(), ProjectID: project.ID, Number: 2, Ref: "whiteboard-2", Title: "Retro scratchpad", UpdatedAt: when.Add(time.Hour)}
	active := model.WhiteboardPage{ID: older.ID, ProjectID: project.ID, Number: 1, Ref: "whiteboard-1", Title: "Launch ideas", Body: "Ship **soon**.", UpdatedAt: when}
	return &uiWhiteboardData{
		CSRFToken:  "csrf",
		Project:    project,
		CanWrite:   canWrite,
		Action:     "view",
		Items:      []model.WhiteboardPageSummary{newer, older},
		HasActive:  true,
		Active:     active,
		ActiveHTML: renderWhiteboardMarkdown(active),
	}
}

func TestUIWhiteboardRendersListAndSelectedPage(t *testing.T) {
	t.Parallel()

	body := renderWhiteboardPanelForTest(t, whiteboardFixture(true))
	for _, want := range []string{
		`<nav aria-label="Whiteboard pages"`,
		`href="/bradley/projects/TRACK/whiteboard/whiteboard-2"`,
		`hx-get="/bradley/projects/TRACK/whiteboard/whiteboard-1/panel"`,
		`aria-current="page" class="flex min-w-0 items-center gap-2 px-3 py-2.5 text-sm`,
		`data-lucide="sticky-note"`,
		"<strong>soon</strong>",
		`aria-label="New whiteboard page"`,
		`aria-label="Edit whiteboard page"`,
		`hx-push-url="/bradley/projects/TRACK/whiteboard/whiteboard-1/edit"`,
		`aria-label="Delete whiteboard page"`,
		`hx-confirm="Delete this whiteboard page?"`,
		`action="/bradley/projects/TRACK/whiteboard/whiteboard-1/delete"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("whiteboard panel missing %q: %s", want, body)
		}
	}
	// Titles only in the list, newest first, and only the selected page's body.
	requireMarkupOrder(t, body, ">Retro scratchpad</span>", ">Launch ideas</span>")
	if got := strings.Count(body, `aria-current="page" class="flex min-w-0`); got != 1 {
		t.Fatalf("selected page rows = %d, want 1: %s", got, body)
	}
	for _, notWant := range []string{"Linked issues", `aria-label="Link issue"`, `aria-label="Manage linked issues"`, "No linked issues.", "data-attachment-dropzone", "Import document"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("whiteboard panel rendered issue-linking or attachment UI %q: %s", notWant, body)
		}
	}
}

func TestUIWhiteboardHidesWriteControlsFromReaders(t *testing.T) {
	t.Parallel()

	body := renderWhiteboardPanelForTest(t, whiteboardFixture(false))
	if !strings.Contains(body, "<strong>soon</strong>") {
		t.Fatalf("reader should see the page body: %s", body)
	}
	for _, notWant := range []string{`aria-label="New whiteboard page"`, `aria-label="Edit whiteboard page"`, `aria-label="Delete whiteboard page"`, "/whiteboard/new"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("reader saw write control %q: %s", notWant, body)
		}
	}

	empty := whiteboardFixture(false)
	empty.Items, empty.HasActive, empty.Active, empty.ActiveHTML = nil, false, model.WhiteboardPage{}, ""
	body = renderWhiteboardPanelForTest(t, empty)
	if !strings.Contains(body, "No whiteboard pages yet") || !strings.Contains(body, "No pages yet.") {
		t.Fatalf("reader empty state missing: %s", body)
	}
	if strings.Contains(body, ">New page</a>") {
		t.Fatalf("reader empty state offered a create action: %s", body)
	}

	empty.CanWrite = true
	body = renderWhiteboardPanelForTest(t, empty)
	for _, want := range []string{"No whiteboard pages yet", ">New page</a>", `hx-push-url="/bradley/projects/TRACK/whiteboard/new"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("writer empty state missing %q: %s", want, body)
		}
	}
}

func TestUIWhiteboardCreateAndEditForms(t *testing.T) {
	t.Parallel()

	create := whiteboardFixture(true)
	create.Action = "create"
	create.TitleInput = "Draft"
	create.BodyInput = "draft body"
	create.Error = "title required, max 200 chars"
	create.HasMore = true
	body := renderWhiteboardPanelForTest(t, create)
	for _, want := range []string{
		">New page</h2>",
		`action="/bradley/projects/TRACK/whiteboard"`,
		`name="title" value="Draft"`,
		`placeholder="` + uiWhiteboardBodyPlaceholder + `"`,
		">draft body</textarea>",
		"title required, max 200 chars",
		"Create page",
		`hx-get="/bradley/projects/TRACK/whiteboard/panel"`,
		"Showing the 200 most recent",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("create form missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `aria-current="page" class="flex min-w-0`) || strings.Contains(body, "<strong>soon</strong>") {
		t.Fatalf("create form should not select or show a page: %s", body)
	}

	edit := whiteboardFixture(true)
	edit.Action = "edit"
	edit.TitleInput = edit.Active.Title
	edit.BodyInput = edit.Active.Body
	body = renderWhiteboardPanelForTest(t, edit)
	for _, want := range []string{
		`action="/bradley/projects/TRACK/whiteboard/whiteboard-1"`,
		`name="title" value="Launch ideas"`,
		">Ship **soon**.</textarea>",
		">Save</button>",
		`hx-get="/bradley/projects/TRACK/whiteboard/whiteboard-1/panel"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("edit form missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{`aria-label="Edit whiteboard page"`, `aria-label="Delete whiteboard page"`, "<strong>soon</strong>"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("edit form rendered %q: %s", notWant, body)
		}
	}
}

func TestRenderWhiteboardMarkdownKeepsExternalImagesInert(t *testing.T) {
	t.Parallel()

	html := string(renderWhiteboardMarkdown(model.WhiteboardPage{Body: "![board](https://example.com/board.png)\n\n![shot](object-1)\n\n<script>alert(1)</script>"}))
	if strings.Contains(html, "<img") {
		t.Fatalf("whiteboard Markdown rendered an image: %s", html)
	}
	for _, want := range []string{`<a href="https://example.com/board.png" rel="noreferrer" referrerpolicy="no-referrer">board</a>`} {
		if !strings.Contains(html, want) {
			t.Fatalf("whiteboard Markdown missing %q: %s", want, html)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, `href="object-1"`) {
		t.Fatalf("whiteboard Markdown kept raw HTML or resolved an object ref: %s", html)
	}
	if got := renderWhiteboardMarkdown(model.WhiteboardPage{Body: "  "}); got != "" {
		t.Fatalf("blank whiteboard body rendered %q, want empty", got)
	}
}

func TestUIWhiteboardChangelogIconAndPaths(t *testing.T) {
	t.Parallel()

	if got := uiChangelogIcon(model.ProjectChangelogEntry{Entity: "whiteboard_page"}); got != "presentation" {
		t.Fatalf("whiteboard changelog icon = %q, want presentation", got)
	}
	project := model.Project{OwnerUsername: "bradley", Key: "TRACK"}
	for got, want := range map[string]string{
		uiProjectWhiteboardPath(project):                           "/bradley/projects/TRACK/whiteboard",
		uiProjectWhiteboardNewPath(project):                        "/bradley/projects/TRACK/whiteboard/new",
		uiProjectWhiteboardPagePath(project, "whiteboard-3"):       "/bradley/projects/TRACK/whiteboard/whiteboard-3",
		uiProjectWhiteboardPagePanelPath(project, "whiteboard-3"):  "/bradley/projects/TRACK/whiteboard/whiteboard-3/panel",
		uiProjectWhiteboardPageEditPath(project, "whiteboard-3"):   "/bradley/projects/TRACK/whiteboard/whiteboard-3/edit",
		uiProjectWhiteboardPageDeletePath(project, "whiteboard-3"): "/bradley/projects/TRACK/whiteboard/whiteboard-3/delete",
	} {
		if got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
	}
	tabs := uiProjectTabs(project, "whiteboard", nil)
	var whiteboardTab *uiTabItem
	for i := range tabs.Items {
		if tabs.Items[i].Label == "Whiteboard" {
			whiteboardTab = &tabs.Items[i]
		}
	}
	if whiteboardTab == nil || !whiteboardTab.Active || !whiteboardTab.MobileOverflow || whiteboardTab.Icon != "presentation" || whiteboardTab.HXGet != "/bradley/projects/TRACK/whiteboard/panel" {
		t.Fatalf("whiteboard tab = %+v", whiteboardTab)
	}
}
