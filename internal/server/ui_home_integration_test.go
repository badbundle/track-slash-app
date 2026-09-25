package server_test

import (
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUIRendersWorkSidebar(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-member")

	body := e.uiGet(t, "/me", token)
	for _, want := range []string{
		">Me<",
		">Projects<",
		"Create issue",
		`href="/issues/new"`,
		`hx-get="/issues/new/panel"`,
		`data-sidebar-action`,
		`<nav aria-label="Account" data-sidebar-account`,
		`href="/settings/profile"`,
		`href="/settings/login"`,
		`href="/settings/notifications"`,
		`href="/tokens"`,
		`data-lucide="plus"`,
		`data-lucide="user"`,
		`data-lucide="folder"`,
		`data-lucide="circle-user-round"`,
		`data-lucide="key-round"`,
		`data-lucide="bell"`,
		`data-lucide="braces"`,
		"data-nav-loader",
		`data-mobile-app-bar`,
		`data-mobile-sidebar-toggle`,
		`aria-controls="app-sidebar"`,
		`data-mobile-sidebar-backdrop`,
		`id="app-sidebar" data-mobile-sidebar`,
		`data-mobile-sidebar-close`,
		`data-sidebar-collapse-toggle`,
		`aria-label="Collapse sidebar"`,
		`data-sidebar-collapse-icon data-lucide="panel-left-close"`,
		`data-member-menu`,
		`data-close-on-outside`,
		`overflow-visible border-r`,
		`overflow-x-hidden overflow-y-auto`,
		`/static/app.css`,
		`/static/app.js`,
		`/static/preload.js`,
		`>@` + user.Username + `<`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
	toolbarStart := strings.Index(body, `data-mobile-app-bar`)
	mainStart := strings.Index(body, `<main id="main"`)
	if toolbarStart < 0 || mainStart < 0 || toolbarStart > mainStart {
		t.Fatalf("mobile app bar must remain outside and before the HTMX main target: %s", body)
	}

	css := e.uiGet(t, "/static/app.css", token)
	for _, want := range []string{
		`[data-mobile-sidebar]{visibility:hidden;transform:translateX(-100%)}`,
		`@media (min-width:768px)`,
		`html[data-sidebar-collapsed] .app-shell>aside`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("app stylesheet missing %q: %s", want, css)
		}
	}
	mediaStart := strings.Index(css, `@media (min-width:768px)`)
	desktopCollapseStart := strings.Index(css, `html[data-sidebar-collapsed] .app-shell>aside`)
	if mediaStart < 0 || desktopCollapseStart < mediaStart {
		t.Fatalf("desktop sidebar collapse CSS must be scoped to the md breakpoint: %s", css)
	}
	if strings.Contains(css, "#sidebar-toggle") {
		t.Fatalf("stylesheet retains the hidden-checkbox sidebar toggle: %s", css)
	}

	scripts := e.uiGet(t, "/static/app.js", token) + e.uiGet(t, "/static/preload.js", token)
	for _, want := range []string{
		`syncMobileSidebar`,
		`openMobileSidebar`,
		`closeMobileSidebar`,
		`mobileSidebar.inert = !visible`,
		`track-slash.sidebar.collapsed`,
		`sidebarToggle.addEventListener("click"`,
		`collapsed ? "Expand sidebar" : "Collapse sidebar"`,
		`collapsed ? "panel-left-open" : "panel-left-close"`,
		`closeOpenDropdowns`,
	} {
		if !strings.Contains(scripts, want) {
			t.Fatalf("app scripts missing %q: %s", want, scripts)
		}
	}
	if strings.Contains(body, `href="/settings"`) {
		t.Fatalf("body still links to the removed general Settings page: %s", body)
	}
	for _, notWant := range []string{`data-sidebar-legal`, `aria-label="Legal"`, `href="/terms"`, `href="/privacy"`, `href="/security"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("work shell still contains sidebar legal link %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{"Assigned to me", "Active work board", "Across projects"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("body still has sidebar subtitle %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{">Sprint<", ">Backlog<", e.projKey, `href="/sprint"`, `href="/backlog"`, `href="/projects/` + e.projectID.String() + `/sprint"`, `href="/projects/` + e.projectID.String() + `/backlog"`, `hx-get="/sprint/panel"`, `hx-get="/backlog/panel"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("body still has global work link %q: %s", notWant, body)
		}
	}
	if !strings.Contains(body, user.Name) {
		t.Fatalf("body missing current user: %s", body)
	}
	if strings.Contains(body, ">Member<") {
		t.Fatalf("account overlay should show @username instead of member role: %s", body)
	}
	for _, legacy := range []string{`href="/app`, `action="/app`, `hx-get="/app`, `hx-push-url="/app`} {
		if strings.Contains(body, legacy) {
			t.Fatalf("body contains legacy /app path %q: %s", legacy, body)
		}
	}
}

func TestUIRendersPersonalWorkViews(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-work")

	activeSprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Personal Active Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint active: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, activeSprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("UpdateSprint active: %v", err)
	}
	plannedSprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Personal Planned Sprint",
		StartDate: datePtr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}

	activeTodoP0, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "active assigned todo p0",
		Priority:   model.PriorityP0,
		AssigneeID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue active todo: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, activeTodoP0.ID, store.UpdateIssueParams{SprintID: &activeSprint.ID}); err != nil {
		t.Fatalf("assign active todo: %v", err)
	}
	activeDoneP1, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "active assigned done p1",
		Priority:   model.PriorityP1,
		AssigneeID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue active done: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, activeDoneP1.ID, store.UpdateIssueParams{SprintID: &activeSprint.ID}); err != nil {
		t.Fatalf("assign active done: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, activeDoneP1.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("set active done: %v", err)
	}
	activeUnassigned, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "active unassigned issue",
		Priority:  model.PriorityP0,
	})
	if err != nil {
		t.Fatalf("CreateIssue active unassigned: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, activeUnassigned.ID, store.UpdateIssueParams{SprintID: &activeSprint.ID}); err != nil {
		t.Fatalf("assign active unassigned: %v", err)
	}
	plannedAssigned, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "planned assigned issue",
		AssigneeID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue planned assigned: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, plannedAssigned.ID, store.UpdateIssueParams{SprintID: &plannedSprint.ID}); err != nil {
		t.Fatalf("assign planned: %v", err)
	}
	backlogAssigned, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "backlog assigned issue",
		AssigneeID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue backlog assigned: %v", err)
	}
	parent, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "parent with child"})
	if err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, parent.ID, store.UpdateIssueParams{SprintID: &activeSprint.ID}); err != nil {
		t.Fatalf("assign parent active: %v", err)
	}
	child, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         "assigned child issue",
		AssigneeID:    &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateSubIssue assigned child: %v", err)
	}
	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "Other Personal Project", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, otherProject.ID, user.ID); err != nil {
		t.Fatalf("GrantProjectAccess other: %v", err)
	}
	otherActive, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: otherProject.ID,
		Name:      "Other Active Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint other active: %v", err)
	}
	if _, err := e.store.UpdateSprint(e.ctx, otherActive.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("UpdateSprint other active: %v", err)
	}
	otherP0, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  otherProject.ID,
		Title:      "other project active p0",
		Priority:   model.PriorityP0,
		AssigneeID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue other active: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, otherP0.ID, store.UpdateIssueParams{SprintID: &otherActive.ID}); err != nil {
		t.Fatalf("assign other active: %v", err)
	}

	// Both work views default to open work, so the done issue is absent until
	// the status filter asks for it.
	meBody := e.uiGet(t, "/me", token)
	for _, want := range []string{"Active Sprints", "All", "Issue controls", "active assigned todo p0", "other project active p0"} {
		if !strings.Contains(meBody, want) {
			t.Fatalf("me body missing %q: %s", want, meBody)
		}
	}
	for _, notWant := range []string{activeDoneP1.Title, activeUnassigned.Title, plannedAssigned.Title, backlogAssigned.Title, child.Title} {
		if strings.Contains(meBody, notWant) {
			t.Fatalf("me body included %q: %s", notWant, meBody)
		}
	}

	meAnyBody := e.uiGet(t, "/me?status=any", token)
	if !strings.Contains(meAnyBody, activeDoneP1.Title) {
		t.Fatalf("me any-status body missing %q: %s", activeDoneP1.Title, meAnyBody)
	}

	allBody := e.uiGet(t, "/me/all", token)
	for _, want := range []string{"All assigned issues", activeTodoP0.Title, plannedAssigned.Title, backlogAssigned.Title, otherP0.Title} {
		if !strings.Contains(allBody, want) {
			t.Fatalf("me all body missing %q: %s", want, allBody)
		}
	}
	for _, notWant := range []string{activeDoneP1.Title, activeUnassigned.Title, child.Title} {
		if strings.Contains(allBody, notWant) {
			t.Fatalf("me all body included %q: %s", notWant, allBody)
		}
	}

	allAnyBody := e.uiGet(t, "/me/all?status=any", token)
	if !strings.Contains(allAnyBody, activeDoneP1.Title) {
		t.Fatalf("me all any-status body missing %q: %s", activeDoneP1.Title, allAnyBody)
	}

	filteredActive := e.uiGet(t, "/me?status=done&priority=P1", token)
	if !strings.Contains(filteredActive, "active assigned done p1") {
		t.Fatalf("filtered active missing done p1: %s", filteredActive)
	}
	for _, notWant := range []string{activeTodoP0.Title, otherP0.Title, plannedAssigned.Title} {
		if strings.Contains(filteredActive, notWant) {
			t.Fatalf("filtered active included %q: %s", notWant, filteredActive)
		}
	}

	filteredAll := e.uiGet(t, "/me/all?status=todo&priority=P0", token)
	for _, want := range []string{activeTodoP0.Title, otherP0.Title} {
		if !strings.Contains(filteredAll, want) {
			t.Fatalf("filtered all missing %q: %s", want, filteredAll)
		}
	}
	for _, notWant := range []string{activeDoneP1.Title, plannedAssigned.Title, backlogAssigned.Title, child.Title} {
		if strings.Contains(filteredAll, notWant) {
			t.Fatalf("filtered all included %q: %s", notWant, filteredAll)
		}
	}

	priorityBody := e.uiGet(t, "/me?status=any&sort=priority", token)
	otherIdx := strings.Index(priorityBody, "other project active p0")
	doneIdx := strings.Index(priorityBody, "active assigned done p1")
	if otherIdx < 0 || doneIdx < 0 || otherIdx > doneIdx {
		t.Fatalf("priority sort order wrong: other=%d done=%d body=%s", otherIdx, doneIdx, priorityBody)
	}
}

func TestUIHomeRedirectsToFirstProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-home")
	res := e.uiDoNoRedirect(t, http.MethodGet, "/", token, nil)
	defer res.Body.Close()

	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("code = %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != e.projectPath()+"/sprint" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestUIHomeRedirectsToProjectsWithoutAccessibleProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustUserToken(t, "ui-home-empty")
	res := e.uiDoNoRedirect(t, http.MethodGet, "/", token, nil)
	defer res.Body.Close()

	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("code = %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/projects" {
		t.Fatalf("Location = %q", loc)
	}
}
