package server

import (
	"bytes"
	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
	"os"
	"strings"
	"testing"
)

func TestUIPriorityBadgeRendersFilledCircle(t *testing.T) {
	t.Parallel()

	for _, priority := range []model.IssuePriority{model.PriorityP0, model.PriorityP1, model.PriorityP2, model.PriorityP3, model.PriorityP4} {
		var buf bytes.Buffer
		if err := uiTemplates.ExecuteTemplate(&buf, "priority-badge", priority); err != nil {
			t.Fatalf("ExecuteTemplate %s: %v", priority, err)
		}
		body := buf.String()
		for _, want := range []string{
			`aria-label="Priority ` + string(priority) + `"`,
			"h-5 w-5",
			"rounded-full",
			"font-bold",
			uiPriorityClass(priority),
			string(priority),
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("priority badge %s missing %q: %s", priority, want, body)
			}
		}
	}
}

func TestUIAccessBadgeRendersEachSettingState(t *testing.T) {
	t.Parallel()

	const (
		tinted  = "border-emerald-200 bg-emerald-50 text-emerald-700"
		neutral = "border-slate-300 bg-white text-slate-700"
	)
	for _, tc := range []struct {
		name  string
		badge uiAccessBadge
		hook  string
		icon  string
		label string
		tone  string
	}{
		{name: "public", badge: uiProjectVisibilityBadge(true), hook: `data-project-visibility="public"`, icon: "globe", label: "Public", tone: tinted},
		{name: "private", badge: uiProjectVisibilityBadge(false), hook: `data-project-visibility="private"`, icon: "lock", label: "Private", tone: neutral},
		{name: "public issue creation", badge: uiProjectIssueCreationBadge(true), hook: `data-project-issue-creation="public"`, icon: "users", label: "Any signed-in user", tone: tinted},
		{name: "members-only issue creation", badge: uiProjectIssueCreationBadge(false), hook: `data-project-issue-creation="members"`, icon: "user-round-check", label: "Members only", tone: neutral},
		{name: "sprints enabled", badge: uiProjectSprintModeBadge(true), hook: `data-project-sprint-mode="enabled"`, icon: "person-standing", label: "Enabled", tone: tinted},
		{name: "sprints disabled", badge: uiProjectSprintModeBadge(false), hook: `data-project-sprint-mode="disabled"`, icon: "list-checks", label: "Disabled", tone: neutral},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			if err := uiTemplates.ExecuteTemplate(&buf, "access-badge", tc.badge); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			body := buf.String()
			for _, want := range []string{
				`<span ` + tc.hook + ` class="inline-flex w-fit shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border`,
				tc.tone,
				`<i data-lucide="` + tc.icon + `" class="h-3.5 w-3.5" aria-hidden="true"></i>` + tc.label + `</span>`,
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("access badge missing %q: %s", want, body)
				}
			}
			other := tinted
			if tc.tone == tinted {
				other = neutral
			}
			if strings.Contains(body, other) {
				t.Fatalf("access badge mixes tinted and neutral tones: %s", body)
			}
		})
	}
}

func TestUISprintRefBadgePreservesCanonicalReference(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "sprint-ref", "sprint-23"); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`data-sprint-ref`,
		`shrink-0`,
		`whitespace-nowrap`,
		`font-mono`,
		`dark:border-slate-700`,
		`>sprint-23</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("sprint ref badge missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "uppercase") {
		t.Fatalf("sprint ref badge changes canonical casing: %s", body)
	}
}

func TestUIIssueSummaryRowReflowsForNarrowScreens(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "issue-summary-row", uiIssueItem{
		Issue: model.Issue{
			Identifier: "TRACK-12",
			Title:      "Responsive issue row",
			Status:     model.StatusDone,
			Priority:   model.PriorityP1,
		},
		SubIssueProgress: store.SubIssueProgress{Total: 5, Completed: 3},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{
		`data-issue-summary-row`,
		`grid min-w-0 gap-2 sm:grid-cols-[7rem_auto_minmax(0,1fr)_auto] sm:items-center sm:gap-3`,
		`sm:contents`,
		`break-words`,
		`sm:truncate`,
		`flex-wrap`,
		`sm:flex-nowrap`,
		`TRACK-12`,
		`Responsive issue row`,
		`aria-label="Priority P1"`,
		`role="progressbar" aria-label="Sub-issues completed" aria-valuemin="0" aria-valuemax="5" aria-valuenow="3"`,
		`pathLength="5" stroke-dasharray="3 5"`,
		`>3/5</span>`,
		`Done`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("responsive issue summary row missing %q: %s", want, body)
		}
	}
}

func TestUIIssueSummaryRowOmitsEmptySubIssueProgress(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "issue-summary-row", uiIssueItem{Issue: model.Issue{
		Identifier: "TRACK-13",
		Title:      "No sub-issues",
		Status:     model.StatusTodo,
		Priority:   model.PriorityP2,
	}})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	if body := buf.String(); strings.Contains(body, `role="progressbar"`) {
		t.Fatalf("empty sub-issue progress rendered: %s", body)
	}
}

func TestUIIssueListsUseSharedResponsiveSummaryRow(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		path string
		call string
	}{
		{name: "personal work", path: "templates/work_panel.html", call: `{{template "issue-summary-row" .}}`},
		{name: "project issue lists", path: "templates/project_issue_lists.html", call: `{{template "issue-summary-row" .}}`},
		{name: "planned sprint", path: "templates/project_panel_planned.html", call: `{{template "issue-summary-row" .}}`},
		{name: "sub-issues", path: "templates/issue_panel_relationships.html", call: `{{template "issue-summary-row" (issueItem .)}}`},
	} {
		src, err := uiTemplateFS.ReadFile(tt.path)
		if err != nil {
			t.Fatalf("%s: read template: %v", tt.name, err)
		}
		body := string(src)
		if !strings.Contains(body, tt.call) {
			t.Fatalf("%s missing shared responsive issue row call %q: %s", tt.name, tt.call, body)
		}
		if strings.Contains(body, `issue-summary-row-cells`) {
			t.Fatalf("%s still uses the fixed-grid issue row cells: %s", tt.name, body)
		}
	}
}

func TestUIWorkPanelRendersTabsAndIssueControls(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	query := uiIssueListQuery{
		Statuses:   []model.Status{model.StatusDone},
		Priorities: []model.IssuePriority{model.PriorityP0},
		Sort:       store.ListIssuesSortPriority,
	}
	issue := model.Issue{
		ID:            uuid.MustParse("138095fe-77d7-4644-b127-d0b995757ff2"),
		ProjectID:     projectID,
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Number:        12,
		Identifier:    "TRACK-12",
		Title:         "Done active issue",
		Status:        model.StatusDone,
		Priority:      model.PriorityP0,
	}

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "work-panel", &uiWorkPanelData{
		View:           "active",
		Title:          "Me",
		Subtitle:       "Active sprint issues assigned to you across accessible projects.",
		IssueListLabel: "Active sprint issues",
		ProjectCount:   1,
		WorkTabs:       uiWorkTabs("active", query),
		IssueControls:  uiWorkIssueControls("active", query),
		Issues:         []uiIssueItem{{Issue: issue, Project: project}},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "<header>") || !strings.Contains(body, `<section class="pt-4 pb-6">`) {
		t.Fatalf("work panel should keep compact spacing between tabs and controls: %s", body)
	}
	if strings.Contains(body, `<header class="pb-5">`) || strings.Contains(body, `<header class="border-b border-slate-200 pb-5`) || strings.Contains(body, `<section class="py-6">`) {
		t.Fatalf("work panel should not render extra spacing above controls: %s", body)
	}
	for _, want := range []string{
		"Active Sprints",
		"All",
		`aria-label="Me views"`,
		`<details data-issue-list-controls data-close-on-outside class="mb-4`,
		`aria-label="Issue controls"`,
		`data-lucide="sliders-horizontal"`,
		`class="` + uiCountBadgeClass + `">2</span>`,
		"Status",
		"Priority",
		"Sort",
		"Direction",
		"Asc",
		`data-lucide="arrow-up"`,
		"Due date",
		"Done",
		`aria-label="Priority P0"`,
		`href="/me/all?priority=P0&amp;sort=priority&amp;status=done"`,
		`hx-get="/me/all/panel?priority=P0&amp;sort=priority&amp;status=done"`,
		`href="/me?priority=P0&amp;sort=priority&amp;status=done"`,
		`hx-get="/me/panel?priority=P0&amp;sort=priority&amp;status=done"`,
		`aria-current="page"`,
		`aria-pressed="true"`,
		"Active sprint issues",
		"Done active issue",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("work panel missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Assignee") || strings.Contains(body, "Anyone") {
		t.Fatalf("work panel rendered assignee controls: %s", body)
	}
}

func TestUIProjectIssueControlsRenderTagFiltersAndBadges(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("1f58d897-0e5d-4f72-bf4f-0f7be3d964f6")
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	tags := []model.IssueTag{
		uiTestIssueTag(projectID, 1, "Customer Beta", model.TagColorGreen),
		uiTestIssueTag(projectID, 2, "Q3 Launch", model.TagColorViolet),
		uiTestIssueTag(projectID, 3, "Ops Review", model.TagColorAmber),
		uiTestIssueTag(projectID, 4, "Escalated", model.TagColorRed),
	}
	query := uiProjectAllQuery{TagNames: []string{"Customer Beta"}}
	issue := model.Issue{
		ID:            uuid.MustParse("1ecf456e-3a8e-4d8f-a685-6388c058abcf"),
		ProjectID:     projectID,
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Number:        12,
		Identifier:    "TRACK-12",
		Title:         "Tagged issue",
		Status:        model.StatusTodo,
		Priority:      model.PriorityP2,
		Tags:          tags,
	}

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "project-panel", &uiProjectPanelData{
		CanWrite:    true,
		Project:     project,
		View:        "all",
		ProjectTabs: uiProjectTabs(project, "all", nil),
		AllIssuePage: uiProjectAllIssuePageData{
			Issues: []uiIssueItem{{Issue: issue, Project: project}},
		},
		AllControls: uiProjectAllIssueControls(project, query, tags[:2], nil, false, "", "", ""),
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		"Tags",
		"#Customer Beta",
		"#Q3 Launch",
		"border-green-200 bg-green-50 text-green-700",
		"border-violet-200 bg-violet-50 text-violet-700",
		`data-lucide="check"`,
		"border-indigo-200 bg-indigo-50/70",
		`href="/bradley/projects/TRACK/all?tag=Customer&#43;Beta"`,
		`href="/bradley/projects/TRACK/all?tag=Customer&#43;Beta&amp;tag=Q3&#43;Launch"`,
		"Tagged issue",
		"#Ops Review",
		`>&#43;1</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("project tag controls/badges missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Assignee") {
		t.Fatalf("project tag controls unexpectedly rendered assignee row: %s", body)
	}
}

func TestUITagManagerUsesTagNamesNotRefs(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("a5a29c23-62e6-4afa-a1f4-2329d9589787")
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	issue := model.Issue{
		ID:            uuid.MustParse("8df3db99-219a-4a89-9f5e-9727f033c4ea"),
		ProjectID:     projectID,
		OwnerUsername: "bradley",
		ProjectKey:    "TRACK",
		Number:        12,
		Identifier:    "TRACK-12",
		Title:         "Tagged issue",
	}
	tags := []model.IssueTag{
		uiTestIssueTag(projectID, 1, "Customer Beta", model.TagColorGreen),
		uiTestIssueTag(projectID, 2, "Q3 Launch", model.TagColorViolet),
	}

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "tag-manager-panel", &uiTagManagerData{
		CanWrite:  true,
		Mode:      "issue",
		Project:   project,
		Issue:     issue,
		HasIssue:  true,
		Tags:      tags[:1],
		Available: tags[1:],
		BackHref:  "/bradley/issues/TRACK-12",
		BackHXGet: "/bradley/issues/TRACK-12/panel",
		BackLabel: "Issue",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()
	for _, want := range []string{"#Customer Beta", "#Q3 Launch", `value="Q3 Launch"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("tag manager missing %q: %s", want, body)
		}
	}
	for _, unwanted := range []string{`>tag-1<`, `>tag-2<`, `value="tag-2"`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("tag manager rendered ref %q: %s", unwanted, body)
		}
	}
}

func TestUIShellRendersResponsiveAccessibleSidebar(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{
		User:          model.User{Name: "Demo User", Username: "demo"},
		SidebarActive: uiSidebarState{View: "projects"},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	css, err := os.ReadFile("../../frontend/tailwind.css")
	if err != nil {
		t.Fatalf("read Tailwind source: %v", err)
	}
	appJS, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app asset: %v", err)
	}
	preloadJS, err := uiTemplateFS.ReadFile("static/preload.js")
	if err != nil {
		t.Fatalf("read preload asset: %v", err)
	}
	body := strings.Join([]string{buf.String(), string(css), string(appJS), string(preloadJS)}, "\n")
	for _, want := range []string{
		`data-mobile-app-bar class="flex h-14`,
		`data-mobile-sidebar-toggle`,
		`aria-controls="app-sidebar"`,
		`aria-expanded="false"`,
		`data-mobile-sidebar-backdrop`,
		`hidden aria-hidden="true" tabindex="-1"`,
		`id="app-sidebar" data-mobile-sidebar`,
		`data-mobile-sidebar aria-hidden="true" inert`,
		`w-72 max-w-[calc(100vw-2rem)]`,
		`[data-mobile-sidebar] { visibility: hidden; transform: translateX(-100%); }`,
		`data-mobile-sidebar-close`,
		`data-mobile-sidebar-open`,
		`syncMobileSidebar`,
		`openMobileSidebar`,
		`closeMobileSidebar`,
		`let mobileSidebarOpen = false`,
		`mobileSidebar.setAttribute("aria-hidden", visible ? "false" : "true")`,
		`mobileSidebar.inert = !visible`,
		`mobileAppBar.inert = open`,
		`mainContent.inert = open`,
		`mobileSidebarToggle.setAttribute("aria-expanded", open ? "true" : "false")`,
		`mobileSidebarBackdrop.hidden = !open`,
		`mobileSidebarClose.focus()`,
		`mobileSidebarReturnFocus.focus()`,
		`mobileSidebarToggle.addEventListener("click", openMobileSidebar)`,
		`mobileSidebarClose.addEventListener("click", () => closeMobileSidebar())`,
		`mobileSidebarBackdrop.addEventListener("click", () => closeMobileSidebar())`,
		`mobileSidebar.addEventListener("click"`,
		`event.target.closest("a[href]")`,
		`event.key !== "Escape" || !mobileSidebarOpen`,
		`mobileSidebarBreakpoint.addEventListener("change", handleMobileSidebarBreakpoint)`,
		`focusFirstDesktopSidebarControl`,
		`focusWasInSidebar`,
		`focusWasInAppBar`,
		`(min-width: 768px)`,
		`@supports (height: 100dvh)`,
		`height: 100vh`,
		`height: 100dvh`,
		`.modal-panel { max-height: calc(100vh - 3rem); max-height: calc(100dvh - 3rem); }`,
		`overflow-x-hidden overflow-y-auto`,
		`data-sidebar-collapse-toggle`,
		`aria-label="Collapse sidebar"`,
		`aria-controls="app-sidebar" aria-expanded="true"`,
		`data-sidebar-collapse-icon data-lucide="panel-left-close"`,
		`html[data-sidebar-collapsed] .app-shell > aside`,
		`html[data-sidebar-collapsed] .app-shell .wide-only`,
		`html[data-sidebar-collapsed] .app-shell .wide-only { display: none; }`,
		`track-slash.sidebar.collapsed`,
		`document.documentElement.toggleAttribute("data-sidebar-collapsed", collapsed)`,
		`sidebarToggle.setAttribute("aria-expanded", collapsed ? "false" : "true")`,
		`sidebarToggle.setAttribute("aria-label", collapsed ? "Expand sidebar" : "Collapse sidebar")`,
		`icon.setAttribute("data-lucide", collapsed ? "panel-left-open" : "panel-left-close")`,
		`sidebarToggle.addEventListener("click"`,
		`const collapsed = !document.documentElement.hasAttribute("data-sidebar-collapsed")`,
		`[data-member-menu] { bottom: 0.5rem; left: calc(100% + 0.5rem); right: auto; width: 12rem; }`,
		`overflow-visible border-r`,
		`data-member-summary`,
		`data-member-label`,
		`data-member-menu`,
		`>@demo<`,
		`data-sidebar-link data-sidebar-view="projects"`,
		`const syncSidebarActive = () =>`,
		`mainContent.querySelector("[data-sidebar-view]")`,
		`event.detail.successful === false`,
		`document.body.addEventListener("htmx:historyRestore", () => {`,
		`link.removeAttribute("aria-current")`,
		`next.setAttribute("aria-current", "page")`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing sidebar behavior %q: %s", want, body)
		}
	}
	mediaStart := strings.Index(body, `@media (min-width: 768px)`)
	desktopCollapseStart := strings.Index(body, `html[data-sidebar-collapsed] .app-shell > aside`)
	if mediaStart < 0 || desktopCollapseStart < mediaStart {
		t.Fatalf("desktop sidebar collapse CSS must be scoped to the md breakpoint: %s", body)
	}
	mobileStateStart := strings.Index(body, `const mobileSidebar =`)
	mobileStateEnd := strings.Index(body, `let reopenIssueListControls =`)
	if mobileStateStart < 0 || mobileStateEnd <= mobileStateStart {
		t.Fatalf("shell missing isolated mobile sidebar state: %s", body)
	}
	if strings.Contains(body[mobileStateStart:mobileStateEnd], `localStorage`) {
		t.Fatalf("mobile drawer state must not share desktop collapse persistence: %s", body[mobileStateStart:mobileStateEnd])
	}
	if strings.Contains(body, `id="sidebar-toggle"`) || strings.Contains(body, "#sidebar-toggle") {
		t.Fatalf("shell retains the hidden-checkbox sidebar toggle: %s", body)
	}
	if strings.Contains(body, "data-main-view") {
		t.Fatalf("shell still contains legacy sidebar state markers: %s", body)
	}
	menuStart := strings.Index(body, `<div data-member-menu`)
	if menuStart < 0 {
		t.Fatalf("shell missing member menu: %s", body)
	}
	menuEnd := strings.Index(body[menuStart:], `>`)
	if menuEnd < 0 {
		t.Fatalf("shell member menu has invalid markup: %s", body)
	}
	if strings.Contains(body[menuStart:menuStart+menuEnd], "wide-only") {
		t.Fatalf("member menu should remain visible when the sidebar is collapsed: %s", body)
	}
	if strings.Contains(body, `data-sidebar-legal`) || strings.Contains(body, `aria-label="Legal"`) {
		t.Fatalf("shell sidebar must not render legal links: %s", body)
	}
	// The account pages live only in the account menu. The sidebar used to
	// repeat them as an Account group, which crowded out project navigation.
	if strings.Contains(body, `data-sidebar-account`) {
		t.Fatalf("sidebar still renders the account group: %s", body)
	}
	menuHTML := markupFromForTest(t, body, `<div data-member-menu`, `</details>`)
	for _, page := range []struct{ view, path, label, icon string }{
		{view: "profile", path: "/settings/profile", label: "Profile", icon: "circle-user-round"},
		{view: "login", path: "/settings/login", label: "Login", icon: "key-round"},
		{view: "notifications", path: "/settings/notifications", label: "Notifications", icon: "bell"},
		{view: "tokens", path: "/tokens", label: "Tokens", icon: "braces"},
	} {
		link := markupFromForTest(t, menuHTML, `<a data-account-menu-link data-account-view="`+page.view+`"`, `</a>`)
		for _, want := range []string{
			`href="` + page.path + `" hx-get="` + page.path + `" hx-target="#main" hx-push-url="` + page.path + `"`,
			`class="flex items-center gap-2 rounded-md px-2 py-2 text-sm`,
			`<i data-lucide="` + page.icon + `" class="h-4 w-4" aria-hidden="true"></i>`,
			`<span>` + page.label + `</span>`,
		} {
			if !strings.Contains(link, want) {
				t.Fatalf("account menu link for %s missing %q: %s", page.label, want, link)
			}
		}
	}
	pagesEnd := strings.LastIndex(menuHTML, `data-account-menu-link`)
	signOutStart := strings.Index(menuHTML, `<form method="post" action="/logout" data-sign-out class="mt-1 border-t border-slate-100 pt-1 dark:border-slate-800">`)
	if signOutStart < 0 || signOutStart < pagesEnd {
		t.Fatalf("sign out must sit below a divider after the account pages: %s", menuHTML)
	}
	signOut := menuHTML[signOutStart:]
	for _, want := range []string{
		`font-semibold text-rose-600`,
		`dark:text-rose-300`,
		`<i data-lucide="log-out" class="h-4 w-4" aria-hidden="true"></i>`,
		`<span>Sign out</span>`,
	} {
		if !strings.Contains(signOut, want) {
			t.Fatalf("sign out missing %q: %s", want, signOut)
		}
	}
	for _, want := range []string{
		`const syncAccountMenuActive = (state) =>`,
		`document.querySelectorAll("[data-account-menu-link]")`,
		`link.dataset.accountView === view`,
		`syncAccountMenuActive(state);`,
		`event.target.closest("[data-account-menu-link]")`,
		`menu.removeAttribute("open")`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing account menu behavior %q: %s", want, body)
		}
	}
	if strings.Contains(body, `href="/settings"`) {
		t.Fatalf("shell still links to the removed general Settings page: %s", body)
	}
	for _, roleLabel := range []string{">Member<", ">Admin<"} {
		if strings.Contains(body, roleLabel) {
			t.Fatalf("member menu should show @username instead of role label %q: %s", roleLabel, body)
		}
	}
	for _, want := range []string{`[data-submit-shortcut='meta-enter']`, `event.metaKey`, `event.ctrlKey`, `form.requestSubmit()`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing comment submit shortcut %q: %s", want, body)
		}
	}
	for _, want := range []string{`[data-autogrow-textarea]`, `resizeTextarea`, `textarea.scrollHeight`, `resizeTextareas(root)`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing autogrowing textarea behavior %q: %s", want, body)
		}
	}
	for _, want := range []string{`[data-search-input]`, `[data-search-option]`, `filterSearchOptions`, `option.dataset.value`, `option.dataset.targetName`, `option.dataset.targetValue`, `data-search-collapsible`, `setSearchOptionsOpen`, `options.hidden = !open`, `search.dataset.searchClearTarget`, `data-project-search`, `!search.hasAttribute("data-project-search")`, `focusin`, `event.key === "Escape"`, `input.form || input.closest("form")`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing search component behavior %q: %s", want, body)
		}
	}
	for _, want := range []string{`[data-checkbox-reveal]`, `syncCheckboxReveal`, `data-checkbox-reveal-toggle`, `data-checkbox-reveal-panel`, `panel.hidden = !open`, `control.disabled = !open`, `control.value = ""`, `aria-expanded`, `syncCheckboxReveals(root)`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing checkbox reveal behavior %q: %s", want, body)
		}
	}
	for _, want := range []string{`data-close-on-outside`, `closeOpenDropdowns`, `details[data-close-on-outside][open]`, `details.removeAttribute("open")`, `data-option-dropdown-root`, `toggle.click()`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing dropdown outside-click behavior %q: %s", want, body)
		}
	}
	for _, want := range []string{`data-issue-list-controls`, `rememberIssueListControls(event.target)`, `restoreIssueListControls(event.target)`, `controls.setAttribute("open", "")`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing issue control reopen behavior %q: %s", want, body)
		}
	}
	for _, want := range []string{`data-disclosure-toggle`, `setDisclosureOpen`, `aria-controls`, `panel.hidden = !open`, `aria-expanded`, `data-disclosure-icon`, `data-disclosure-label`, `label.textContent = text`, `toggle.setAttribute("aria-label", text)`, `Hide issues`, `Show issues`, `chevron-up`, `chevron-down`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing disclosure behavior %q: %s", want, body)
		}
	}
	for _, want := range []string{`attachmentMarkdownSnippet`, `data-attachment-copy-markdown`, `writeClipboardText`, `navigator.clipboard.writeText`, `data-copy-label`, `contentUrl}?inline=1`} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing attachment copy/preview behavior %q: %s", want, body)
		}
	}
	for _, want := range []string{
		`.markdown-body { min-width: 0; overflow-wrap: anywhere; }`,
		`.markdown-body h1, .markdown-body h2, .markdown-body h3, .markdown-body h4, .markdown-body h5, .markdown-body h6 { margin: 1rem 0 0.5rem; font-weight: 600; line-height: 1.25; color: rgb(15 23 42); }`,
		`.markdown-body h1 { font-size: 1.5rem; letter-spacing: -0.025em; }`,
		`.markdown-body h2 { font-size: 1.25rem; letter-spacing: -0.025em; }`,
		`.markdown-body h3 { font-size: 1.125rem; }`,
		`.markdown-body table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }`,
		`.markdown-body table { display: block; max-width: 100%; overflow-x: auto; }`,
		`.markdown-body th, .markdown-body td { border: 1px solid rgb(203 213 225); padding: 0.375rem 0.5rem; text-align: left; vertical-align: top; }`,
		`@media (prefers-color-scheme: dark) {`,
		`.markdown-body { color: rgb(226 232 240); }`,
		`.markdown-body h1, .markdown-body h2, .markdown-body h3, .markdown-body h4, .markdown-body h5, .markdown-body h6 { color: rgb(241 245 249); }`,
		`.markdown-body th, .markdown-body td { border-color: rgb(51 65 85); }`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing markdown CSS %q: %s", want, body)
		}
	}
	if strings.Contains(body, `.dark .markdown-body`) {
		t.Fatalf("shell markdown dark-mode CSS should follow the system media query: %s", body)
	}
}

func TestUIShellRemovesCollapsedLabelsFromLoggedOutSidebar(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{Anonymous: true}); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	css, err := os.ReadFile("../../frontend/tailwind.css")
	if err != nil {
		t.Fatalf("read Tailwind source: %v", err)
	}
	body := buf.String()
	for _, want := range []string{
		`data-sidebar-collapse-toggle aria-label="Collapse sidebar"`,
		`<a data-nav-link href="/login" aria-label="Sign in"`,
		`<a data-nav-link href="/signup" aria-label="Create account"`,
		`<span class="wide-only font-medium">Sign in</span>`,
		`<span class="wide-only font-medium">Create account</span>`,
		`html[data-sidebar-collapsed] .app-shell .wide-only { display: none; }`,
		`html[data-sidebar-collapsed] .app-shell [data-nav-link] { justify-content: center; gap: 0; }`,
	} {
		if !strings.Contains(body+string(css), want) {
			t.Fatalf("logged-out sidebar missing collapsed-label behavior %q: %s", want, body)
		}
	}
	if strings.Contains(string(css), `.wide-only { transition: opacity`) || strings.Contains(string(css), `.wide-only { opacity: 0`) {
		t.Fatalf("collapsed labels must be removed from layout instead of hidden with opacity: %s", css)
	}
	for _, notWant := range []string{`data-sidebar-legal`, `aria-label="Legal"`, `href="/terms"`, `href="/privacy"`, `href="/security"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("logged-out sidebar still contains legal link %q: %s", notWant, body)
		}
	}
}

func TestUIShellProvidesAccessibleIconTooltips(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{
		User: model.User{Name: "Demo User", Username: "demo"},
		SidebarFavorites: uiSidebarFavoritesData{Projects: []model.Project{{
			ID:            projectID,
			OwnerUsername: "bradley",
			Key:           "TRACK",
			Name:          "Track Slash",
		}}},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	css, err := os.ReadFile("../../frontend/tailwind.css")
	if err != nil {
		t.Fatalf("read Tailwind source: %v", err)
	}
	appJS, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app asset: %v", err)
	}
	projectPanel, err := uiTemplateFS.ReadFile("templates/project_panel.html")
	if err != nil {
		t.Fatalf("read project panel template: %v", err)
	}
	projectsPanel, err := uiTemplateFS.ReadFile("templates/projects_panel.html")
	if err != nil {
		t.Fatalf("read projects panel template: %v", err)
	}
	body := strings.Join([]string{buf.String(), string(css), string(appJS), string(projectPanel), string(projectsPanel)}, "\n")
	for _, want := range []string{
		`aria-label="Collapse sidebar"`,
		`aria-label="Me"`,
		`aria-label="Projects"`,
		`data-member-summary data-tooltip="Account menu"`,
		`<span class="sr-only">Account menu:</span>`,
		`aria-label="Track Slash"`,
		`const tooltipSelector = "[data-tooltip], button[aria-label], a[aria-label], summary[aria-label], label[aria-label]`,
		`appTooltip.setAttribute("role", "tooltip")`,
		`appTooltip.setAttribute("data-app-tooltip", "")`,
		`const hasVisibleControlText = (control) =>`,
		`element.getAttribute("aria-hidden") === "true"`,
		`target.getAttribute("aria-label")`,
		`target.setAttribute("aria-describedby"`,
		`match.target.setAttribute("data-app-tooltip-target", "")`,
		`document.body.addEventListener("pointerover"`,
		`document.body.addEventListener("pointerout"`,
		`document.body.addEventListener("focusin"`,
		`document.body.addEventListener("focusout"`,
		`event.pointerType === "touch"`,
		`window.queueMicrotask`,
		`target.hasAttribute("data-tooltip-when-truncated")`,
		`target.querySelectorAll("[data-tooltip-truncates]")`,
		`element.scrollWidth > element.clientWidth`,
		`if (hasVisibleControlText(target) && !tooltipTextIsTruncated(target)) return null;`,
		`[data-app-tooltip-target] { anchor-name: --app-tooltip-target; }`,
		`position-anchor: --app-tooltip-target;`,
		`position-area: bottom center;`,
		`position-try-fallbacks: flip-block, flip-inline, flip-block flip-inline;`,
		`width: max-content;`,
		`max-width: min(18rem, calc(100vw - 1rem));`,
		`pointer-events: none;`,
		`@media (prefers-color-scheme: dark) {`,
		`@media (prefers-reduced-motion: reduce) {`,
		`[data-app-tooltip] { transform: none; transition: none; }`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("shell missing tooltip behavior %q: %s", want, body)
		}
	}
	for _, forbidden := range []string{
		`title="Collapse sidebar"`,
		`title="Expand sidebar"`,
		`title="Account menu"`,
		`title="New project"`,
		`title="{{if .Favorite}}Unfavorite project`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("shell retained duplicate native tooltip %q: %s", forbidden, body)
		}
	}
}

// sidebarSection returns the markup of the sidebar section with the given id.
func sidebarSection(t *testing.T, body, id string) string {
	t.Helper()
	start := strings.Index(body, `<div id="`+id+`"`)
	if start < 0 {
		t.Fatalf("missing sidebar section %q: %s", id, body)
	}
	end := strings.Index(body[start:], "\n</div>")
	if end < 0 {
		t.Fatalf("unterminated sidebar section %q: %s", id, body)
	}
	return body[start : start+end]
}

func TestUIShellRendersSidebarFavoritesAndRecents(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	first := model.Issue{ID: uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b"), ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Number: 85, Identifier: "TRACK-85", Title: "Show a small sprint badge"}
	second := model.Issue{ID: uuid.MustParse("1f2e7c1a-5a47-4bd3-8a4c-1b9b0fd3d2a1"), ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Number: 12, Identifier: "TRACK-12", Title: "Older work"}
	heading := `text-[11px] font-semibold uppercase tracking-wide text-slate-500 dark:text-slate-400">`

	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{
		User:             model.User{Name: "Demo User", Username: "demo"},
		SidebarFavorites: uiSidebarFavoritesData{Projects: []model.Project{project}},
		SidebarRecents:   uiSidebarRecentsData{Issues: []model.Issue{first, second}},
	}); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	body := buf.String()

	favorites := sidebarSection(t, body, "sidebar-favorites")
	if !strings.Contains(favorites, `<h2 class="wide-only px-2 pb-1 pt-1 `+heading+`Favorites</h2>`) {
		t.Fatalf("favorites are missing their heading: %s", favorites)
	}
	recents := sidebarSection(t, body, "sidebar-recents")
	for _, want := range []string{
		`<div id="sidebar-recents"  class="wide-only mb-2 mt-2 space-y-1 border-t`,
		`<h2 class="px-2 pb-1 pt-1 ` + heading + `Recents</h2>`,
		`<a data-sidebar-recent data-sidebar-link data-sidebar-view="issue" data-sidebar-issue-id="` + first.ID.String() + `" href="/bradley/issues/TRACK-85" hx-get="/bradley/issues/TRACK-85/panel" hx-target="#main" hx-push-url="/bradley/issues/TRACK-85" data-tooltip="TRACK-85: Show a small sprint badge" data-tooltip-when-truncated  class="flex min-w-0 items-center gap-2`,
		`text-slate-600 dark:text-slate-300`,
		`>TRACK-85</span>`,
		`<span data-tooltip-truncates class="min-w-0 truncate font-medium">Show a small sprint badge</span>`,
	} {
		if !strings.Contains(recents, want) {
			t.Fatalf("recents missing %q: %s", want, recents)
		}
	}
	if strings.Index(recents, "TRACK-85") > strings.Index(recents, "TRACK-12") {
		t.Fatalf("recents are not newest first: %s", recents)
	}
	if strings.Contains(recents, `aria-current`) {
		t.Fatalf("recents mark an issue current without an active issue: %s", recents)
	}

	buf.Reset()
	if err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{User: model.User{Name: "Demo User", Username: "demo"}}); err != nil {
		t.Fatalf("ExecuteTemplate empty: %v", err)
	}
	empty := buf.String()
	for _, id := range []string{"sidebar-favorites", "sidebar-recents"} {
		section := sidebarSection(t, empty, id)
		if !strings.Contains(section, `class="hidden"`) || strings.Contains(section, "<h2") {
			t.Fatalf("empty %s should be hidden without a heading: %s", id, section)
		}
	}

	buf.Reset()
	if err := uiTemplates.ExecuteTemplate(&buf, "sidebar-recents", uiSidebarRecentsData{Issues: []model.Issue{first}, ActiveIssueID: first.ID, OOB: true}); err != nil {
		t.Fatalf("ExecuteTemplate OOB: %v", err)
	}
	oob := buf.String()
	for _, want := range []string{`<div id="sidebar-recents" hx-swap-oob="true"`, `aria-current="page"`, "bg-indigo-50 text-indigo-700 ring-1 ring-indigo-100"} {
		if !strings.Contains(oob, want) {
			t.Fatalf("out-of-band recents missing %q: %s", want, oob)
		}
	}

	appJS, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app asset: %v", err)
	}
	for _, want := range []string{
		`if (state.dataset.sidebarIssueId) {`,
		`link.dataset.sidebarIssueId === state.dataset.sidebarIssueId && link.getClientRects().length > 0`,
		"applySidebarCollapsed(collapsed);\n      syncSidebarActive();",
	} {
		if !strings.Contains(string(appJS), want) {
			t.Fatalf("app.js missing Recents active-state behavior %q", want)
		}
	}
}

func TestUIShellRendersOneActiveSidebarDestination(t *testing.T) {
	t.Parallel()

	projectID := uuid.MustParse("8cc21ed4-2d69-4d43-9f0c-402736e4aa16")
	project := model.Project{ID: projectID, OwnerUsername: "bradley", Key: "TRACK", Name: "Track Slash"}
	issueID := uuid.MustParse("9480828a-47f3-4661-bb64-b21b4f02f27b")
	issue := model.Issue{ID: issueID, ProjectID: projectID, OwnerUsername: "bradley", ProjectKey: "TRACK", Number: 7, Identifier: "TRACK-7", Title: "Recent work"}
	tests := []struct {
		name        string
		active      uiSidebarState
		favorites   uiSidebarFavoritesData
		recents     uiSidebarRecentsData
		wantMarker  string
		wantCount   int
		accountMenu bool
	}{
		{name: "me", active: uiSidebarState{View: "me"}, wantMarker: `data-sidebar-view="me"`, wantCount: 1},
		{name: "projects", active: uiSidebarState{View: "projects"}, wantMarker: `data-sidebar-view="projects"`, wantCount: 1},
		{
			name:       "favorite project",
			active:     uiSidebarState{View: "project", ProjectID: projectID},
			favorites:  uiSidebarFavoritesData{Projects: []model.Project{project}, ActiveProjectID: projectID},
			wantMarker: `data-sidebar-project-id="` + projectID.String() + `"`,
			wantCount:  1,
		},
		{
			// renderUIShell hands the highlight to the issue's Recents entry.
			name:       "recent issue",
			active:     uiSidebarState{View: "project", ProjectID: projectID, IssueID: issueID},
			favorites:  uiSidebarFavoritesData{Projects: []model.Project{project}},
			recents:    uiSidebarRecentsData{Issues: []model.Issue{issue}, ActiveIssueID: issueID},
			wantMarker: `data-sidebar-issue-id="` + issueID.String() + `"`,
			wantCount:  1,
		},
		// Account pages are marked in the account menu, not the sidebar.
		{name: "profile", active: uiSidebarState{View: "profile"}, wantMarker: `data-account-view="profile"`, wantCount: 1, accountMenu: true},
		{name: "login", active: uiSidebarState{View: "login"}, wantMarker: `data-account-view="login"`, wantCount: 1, accountMenu: true},
		{name: "notifications", active: uiSidebarState{View: "notifications"}, wantMarker: `data-account-view="notifications"`, wantCount: 1, accountMenu: true},
		{name: "tokens", active: uiSidebarState{View: "tokens"}, wantMarker: `data-account-view="tokens"`, wantCount: 1, accountMenu: true},
		{name: "project without favorite", active: uiSidebarState{View: "project", ProjectID: projectID}},
		{name: "no active destination"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{
				User:             model.User{Name: "Demo User", Username: "demo"},
				SidebarActive:    tt.active,
				SidebarFavorites: tt.favorites,
				SidebarRecents:   tt.recents,
			})
			if err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			body := buf.String()
			if got := strings.Count(body, `aria-current="page"`); got != tt.wantCount {
				t.Fatalf("aria-current count = %d, want %d: %s", got, tt.wantCount, body)
			}
			wantSidebar, wantMenu := tt.wantCount, 0
			if tt.accountMenu {
				wantSidebar, wantMenu = 0, tt.wantCount
			}
			if got := strings.Count(body, "bg-indigo-50 text-indigo-700 ring-1 ring-indigo-100"); got != wantSidebar {
				t.Fatalf("sidebar active class count = %d, want %d: %s", got, wantSidebar, body)
			}
			if got := strings.Count(body, "bg-indigo-50 text-indigo-700 dark:bg-indigo-950/50 dark:text-indigo-200"); got != wantMenu {
				t.Fatalf("account menu active class count = %d, want %d: %s", got, wantMenu, body)
			}
			if tt.wantMarker == "" {
				return
			}
			marker := strings.Index(body, tt.wantMarker)
			if marker < 0 {
				t.Fatalf("missing active sidebar marker %q: %s", tt.wantMarker, body)
			}
			start := strings.LastIndex(body[:marker], "<a ")
			end := strings.Index(body[marker:], ">")
			if start < 0 || end < 0 {
				t.Fatalf("invalid active sidebar link markup: %s", body)
			}
			tag := body[start : marker+end]
			if !strings.Contains(tag, `aria-current="page"`) {
				t.Fatalf("active sidebar link missing aria-current: %s", tag)
			}
		})
	}
}

func TestUIPanelsUseConsistentResponsivePageFrame(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		path string
	}{
		{name: "work", path: "templates/work_panel.html"},
		{name: "projects", path: "templates/projects_panel.html"},
		{name: "new project", path: "templates/new_project_panel.html"},
		{name: "new issue", path: "templates/new_issue_panel.html"},
		{name: "project", path: "templates/project_panel.html"},
		{name: "deleted issues", path: "templates/project_deleted_panel.html"},
		{name: "deleted issue", path: "templates/deleted_issue_panel.html"},
		{name: "issue", path: "templates/issue_panel.html"},
		{name: "context manager", path: "templates/context_manager.html"},
		{name: "tag manager", path: "templates/tag_manager.html"},
		{name: "account pages", path: "templates/settings.html"},
		{name: "tokens", path: "templates/tokens.html"},
		{name: "empty shell", path: "templates/shell_main.html"},
	} {
		src, err := uiTemplateFS.ReadFile(tt.path)
		if err != nil {
			t.Fatalf("%s: read template: %v", tt.name, err)
		}
		body := string(src)
		wantClass := `class="mx-auto max-w-6xl px-4 py-4 sm:px-6 sm:py-6"`
		if !strings.Contains(body, wantClass) {
			t.Fatalf("%s panel missing responsive page frame %q: %s", tt.name, wantClass, body)
		}
		if strings.Contains(body, "max-w-5xl") {
			t.Fatalf("%s panel still uses narrower page width: %s", tt.name, body)
		}
		if tt.name != "issue" && tt.name != "tag manager" && strings.Contains(body, `data-lucide="arrow-left"`) {
			t.Fatalf("%s panel should not render app-level back buttons: %s", tt.name, body)
		}
	}
}

func TestUIModalAndSpecialRowsStayContainedOnNarrowScreens(t *testing.T) {
	t.Parallel()

	var modal bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&modal, "modal-open", uiModalData{
		ID:         "responsive-modal",
		Title:      "Responsive modal",
		WidthClass: "max-w-lg",
	}); err != nil {
		t.Fatalf("render modal: %v", err)
	}
	for _, want := range []string{
		`role="dialog"`,
		`aria-modal="true"`,
		`tabindex="-1"`,
		`modal-panel`,
		`overflow-y-auto`,
	} {
		if !strings.Contains(modal.String(), want) {
			t.Fatalf("responsive modal missing %q: %s", want, modal.String())
		}
	}

	modal.Reset()
	if err := uiTemplates.ExecuteTemplate(&modal, "modal-open", uiModalData{
		ID:               "client-modal",
		Title:            "Client modal",
		WidthClass:       "max-w-md",
		CancelLabel:      "Close client modal",
		ClientControlled: true,
	}); err != nil {
		t.Fatalf("render client modal: %v", err)
	}
	for _, want := range []string{
		`id="client-modal" data-client-modal`,
		`class="fixed inset-0 z-50 hidden`,
		`data-modal-close`,
		`aria-label="Close client modal"`,
	} {
		if !strings.Contains(modal.String(), want) {
			t.Fatalf("client modal missing %q: %s", want, modal.String())
		}
	}

	for _, tt := range []struct {
		name string
		path string
		want []string
	}{
		{
			name: "linked issue",
			path: "templates/issue_panel_relationships.html",
			want: []string{
				`grid-cols-[minmax(0,1fr)_auto]`,
				`sm:grid-cols-[4.75rem_minmax(0,1fr)_auto]`,
				`col-span-2 row-start-2`,
				`sm:row-start-1`,
				`flex-wrap`,
				`sm:flex-nowrap`,
			},
		},
		{
			name: "deleted issue",
			path: "templates/project_deleted_panel.html",
			want: []string{
				`class="grid gap-3`,
				`sm:grid-cols-[7rem_auto_minmax(0,1fr)_auto_auto]`,
				`sm:contents`,
			},
		},
	} {
		src, err := uiTemplateFS.ReadFile(tt.path)
		if err != nil {
			t.Fatalf("%s: read template: %v", tt.name, err)
		}
		body := string(src)
		for _, want := range tt.want {
			if !strings.Contains(body, want) {
				t.Fatalf("%s row missing responsive marker %q: %s", tt.name, want, body)
			}
		}
	}
}

func TestUIShellIncludesClientModalBehavior(t *testing.T) {
	t.Parallel()

	body, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app asset: %v", err)
	}
	for _, want := range []string{
		`const setClientModalOpen = (modal, open, trigger = null) => {`,
		`event.target.closest("[data-modal-open]")`,
		`event.target.closest("[data-modal-close]")`,
		`event.target.closest("[data-client-modal]")`,
		`document.querySelector("[data-client-modal]:not(.hidden)")`,
		`const focusClientModal = (modal) => {`,
		`const trapModalFocus = (event, modal) => {`,
		`event.key !== "Tab"`,
		`document.activeElement === last`,
		`document.activeElement === first`,
		`[data-modal-return="${modal.id}"]`,
		`modal.querySelector("[autofocus]") || modal.querySelector("input:not([type='hidden']), select, textarea, button:not([disabled]), [href]")`,
		`returnFocus.focus()`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("shell missing client modal behavior %q", want)
		}
	}
}

func TestUIShellLocalizesSemanticTimes(t *testing.T) {
	t.Parallel()

	body, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app asset: %v", err)
	}
	for _, want := range []string{
		`const localTimeFormatter = new Intl.DateTimeFormat(undefined, {`,
		`timeZoneName: "short"`,
		`const value = new Date(element.getAttribute("datetime") || "")`,
		`root.querySelectorAll("[data-local-time]").forEach(localizeTime)`,
		`localizeTimes(root)`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("shell missing local-time behavior %q", want)
		}
	}
}

func TestUIChangelogRealtimeRefetchesOnOpenAndResync(t *testing.T) {
	t.Parallel()

	body, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app asset: %v", err)
	}
	for _, want := range []string{
		`socket.addEventListener("open", () => {`,
		`socket.send(JSON.stringify({ action: "subscribe", topic: activeTopic }));
      scheduleChangelogRefresh(panel);`,
		`if (msg && msg.type === "resync") {
        scheduleChangelogRefresh(panel);
        return;
      }`,
		`changelogReconnect = window.setTimeout(syncChangelogRealtime, 1000)`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("changelog recovery behavior missing %q", want)
		}
	}
}

func TestUIRenderedPagesUseOnlySelfHostedExecutableAssets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data any
		want []string
	}{
		{name: "login", data: uiLoginData{}, want: []string{"/static/app.css", "/static/lucide.min.js", "/static/auth.js"}},
		{name: "signup", data: uiSignupData{}, want: []string{"/static/app.css", "/static/lucide.min.js", "/static/auth.js"}},
		{name: "shell", data: uiShellData{User: model.User{Username: "demo"}}, want: []string{"/static/app.css", "/static/preload.js", "/static/htmx.min.js", "/static/lucide.min.js", "/static/app.js"}},
		{name: "oauth-consent", data: uiOAuthConsentData{ClientName: "Claude"}, want: []string{"/static/app.css", "/static/lucide.min.js", "/static/auth.js"}},
		{name: "oauth-error", data: uiOAuthErrorData{Title: "Unknown client"}, want: []string{"/static/app.css", "/static/lucide.min.js", "/static/auth.js"}},
		{name: "legal", data: uiLegalData{Title: "Terms"}, want: []string{"/static/app.css"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := uiTemplates.ExecuteTemplate(&buf, tt.name, tt.data); err != nil {
				t.Fatalf("render %s: %v", tt.name, err)
			}
			body := buf.String()
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing local asset %q", tt.name, want)
				}
			}
			for _, forbidden := range []string{"https://cdn.tailwindcss.com", "https://unpkg.com", "<script>", "<style>"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("%s contains forbidden executable asset %q", tt.name, forbidden)
				}
			}
		})
	}
}

// The app shell and the legal pages carry the login page's brand at a calmer
// strength: the shared head links, the still ambient backdrop, and the icon and
// wordmark in their chrome. The shell also shows the navigation progress bar.
func TestUIAppPagesCarryTheAmbientBrand(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		template string
		data     any
		want     []string
	}{
		{name: "signed-in shell", template: "shell", data: uiShellData{User: model.User{Username: "demo"}}, want: []string{`<div data-nav-progress class="nav-progress" aria-hidden="true"></div>`}},
		{name: "anonymous shell", template: "shell", data: uiShellData{Anonymous: true}, want: []string{`<div data-nav-progress class="nav-progress" aria-hidden="true"></div>`}},
		{name: "legal", template: "legal", data: uiLegalData{Title: "Terms"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := uiTemplates.ExecuteTemplate(&buf, tt.template, tt.data); err != nil {
				t.Fatalf("render %s: %v", tt.template, err)
			}
			body := buf.String()
			for _, want := range append([]string{
				`<link rel="icon" href="/static/icon.svg" type="image/svg+xml">`,
				`<link rel="apple-touch-icon" href="/static/icon-192.png">`,
				`<link rel="manifest" href="/manifest.webmanifest">`,
				`<meta name="theme-color" content="#4f46e5">`,
				`<div id="brand-backdrop" data-brand-backdrop class="brand-backdrop brand-backdrop-ambient" aria-hidden="true">`,
				`<span data-brand-mark class="flex min-w-0 items-center gap-2.5">`,
				`<img src="/static/icon.svg" alt="" width="28" height="28"`,
				`<span class="truncate text-base font-semibold tracking-tight text-slate-950 dark:text-white">trackslash</span>`,
			}, tt.want...) {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing %q", tt.name, want)
				}
			}
			if strings.Contains(body, " style=") {
				t.Fatalf("%s uses an inline style, which CSP forbids", tt.name)
			}
		})
	}
}

// Cards that sit straight on the page must be opaque, or the brand backdrop
// shows through them. #main itself stays transparent so the backdrop shows in
// the gutters.
func TestUIShellMainLetsTheBrandBackdropShowThrough(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{User: model.User{Username: "demo"}}); err != nil {
		t.Fatalf("render shell: %v", err)
	}
	if !strings.Contains(buf.String(), `<main id="main" hx-history-elt class="min-w-0 flex-1 overflow-x-hidden overflow-y-auto">`) {
		t.Fatalf("#main should have no background of its own: %s", buf.String())
	}
}

// Back and Forward restore only #main. When htmx restored the whole body, the
// sidebar and app bar came back as fresh elements without the listeners app.js
// attached at load, so the sidebar toggle stopped working until a reload.
func TestUIShellRestoresHistoryIntoMain(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := uiTemplates.ExecuteTemplate(&buf, "shell", uiShellData{User: model.User{Username: "demo"}}); err != nil {
		t.Fatalf("render shell: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `<main id="main" hx-history-elt `) {
		t.Fatalf("#main should be the htmx history element: %s", body)
	}
	if got := strings.Count(body, "hx-history-elt"); got != 1 {
		t.Fatalf("shell has %d history elements, want 1: %s", got, body)
	}
	// A cache miss would otherwise fetch the page as an htmx request, which
	// answers with only #main's content.
	if !strings.Contains(body, `"refreshOnHistoryMiss":true`) {
		t.Fatalf("shell HTMX config should reload on a history cache miss: %s", body)
	}

	script, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	start := strings.Index(string(script), `document.body.addEventListener("htmx:historyRestore", () => {`)
	if start < 0 {
		t.Fatal("app.js does not handle htmx:historyRestore")
	}
	end := strings.Index(string(script[start:]), "\n  });")
	if end < 0 {
		t.Fatal("app.js htmx:historyRestore handler is not closed")
	}
	handler := string(script[start : start+end])
	for _, want := range []string{
		// A snapshot saved while the body was the history element holds the
		// whole shell, so restoring it into #main reloads the page instead.
		`if (mainContent && mainContent.querySelector("[data-mobile-sidebar]")) {`,
		`window.location.reload();`,
		// The restored content gets the setup a swap gives it, and a tooltip
		// whose control the restore removed is hidden.
		`hideAppTooltip();`,
		`setUpContent(mainContent || document);`,
	} {
		if !strings.Contains(handler, want) {
			t.Fatalf("htmx:historyRestore handler missing %q: %s", want, handler)
		}
	}
}

// Content that arrives in #main is set up the same way on load, after a swap
// and after a history restore, so nothing works only until the first Back.
func TestUIShellSetsUpContentTheSameWayEverywhere(t *testing.T) {
	t.Parallel()

	script, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	body := string(script)
	start := strings.Index(body, "const setUpContent = (root = document) => {")
	if start < 0 {
		t.Fatal("app.js has no shared content setup")
	}
	end := strings.Index(body[start:], "\n  };")
	if end < 0 {
		t.Fatal("app.js content setup is not closed")
	}
	setup := body[start : start+end]
	for _, want := range []string{
		"createIcons();",
		"localizeTimes(root);",
		"resizeTextareas(root);",
		"syncCheckboxReveals(root);",
		"syncRepeatTimeZones(root);",
		"syncSidebarActive();",
		"syncChangelogRealtime();",
		"syncPushNotifications(root);",
		"initInsightCharts(root);",
	} {
		if !strings.Contains(setup, want) {
			t.Fatalf("content setup missing %q: %s", want, setup)
		}
	}
	for _, want := range []string{
		"setUpContent(event.target);",
		"setUpContent(mainContent || document);",
		"setUpContent();",
	} {
		if strings.Count(body, want) != 1 {
			t.Fatalf("app.js should call %q exactly once", want)
		}
	}
	// A history snapshot keeps a chart's attributes but not its listeners, so
	// readiness can't be an attribute, and the legend's hidden series carry
	// over into the restored chart's state.
	for _, want := range []string{
		"const readyInsightCharts = new WeakSet();",
		"if (readyInsightCharts.has(chart)) return;",
		`if (button.getAttribute("aria-pressed") === "false") state.hidden.add(`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("app.js missing restorable insight chart setup %q", want)
		}
	}
	if strings.Contains(body, "data-insight-ready") {
		t.Fatal("app.js marks charts ready with an attribute, which a history snapshot keeps")
	}
}

// Only requests that swap #main are page navigations, and the bar counts each
// in-flight request so an overlapping click cannot clear it early.
func TestUIShellTracksNavigationProgress(t *testing.T) {
	t.Parallel()

	script, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	for _, want := range []string{
		`document.documentElement.toggleAttribute("data-nav-busy", navRequestCount > 0);`,
		`if (detail.xhr && detail.target && detail.target.id === "main" && !navRequests.has(detail.xhr)) {`,
		`if (xhr && navRequests.has(xhr)) {`,
	} {
		if !strings.Contains(string(script), want) {
			t.Fatalf("app.js missing %q", want)
		}
	}
	css, err := uiTemplateFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read stylesheet: %v", err)
	}
	if !strings.Contains(string(css), "html[data-nav-busy] .nav-progress{opacity:1;transition-delay:.15s}") {
		t.Fatal("stylesheet does not show the progress bar while a navigation is busy")
	}
}

func TestUIActionMenusStayInsideNarrowHeaders(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		path string
	}{
		{name: "project", path: "templates/project_panel.html"},
		{name: "issue", path: "templates/issue_panel_header.html"},
	} {
		src, err := uiTemplateFS.ReadFile(tt.path)
		if err != nil {
			t.Fatalf("%s: read template: %v", tt.name, err)
		}
		body := string(src)
		for _, want := range []string{
			`grid-cols-[minmax(0,1fr)_auto]`,
			`justify-self-end`,
			`absolute right-0 top-11 z-20 w-48`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s action menu missing narrow-screen alignment %q: %s", tt.name, want, body)
			}
		}
	}
}

func TestUITabBarComponentRendersReusableTabs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := uiTemplates.ExecuteTemplate(&buf, "tab-bar", uiTabBarData{
		Label: "Example views",
		Items: []uiTabItem{
			{Label: "One", Icon: "circle", Href: "/one", HXGet: "/one/panel", HXTarget: "#main", HXPushURL: "/one", Active: true},
			{Label: "Two", Icon: "square", Href: "/two", HXGet: "/two/panel", HXTarget: "#main", HXPushURL: "/two"},
			{Label: "Three", Icon: "triangle", Href: "/three", HXGet: "/three/panel", HXTarget: "#main", HXPushURL: "/three", MobileOverflow: true},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}

	body := buf.String()
	for _, want := range []string{`aria-label="Example views"`, "flex flex-nowrap", "gap-4 sm:gap-6", "border-b-4", `data-lucide="circle"`, `href="/one"`, `hx-get="/one/panel"`, `aria-current="page"`, `href="/two"`, `href="/three"`, `hidden lg:inline-flex`} {
		if !strings.Contains(body, want) {
			t.Fatalf("tab bar missing markup %q: %s", want, body)
		}
	}
	for _, unwanted := range []string{"flex-wrap", "overflow-x-auto"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("tab bar should stay on one line instead of using %q: %s", unwanted, body)
		}
	}
}
