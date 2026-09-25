package server_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

func sidebarNavMarkup(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<nav class="scrollbar-none`)
	if start < 0 {
		t.Fatalf("missing sidebar navigation: %s", body)
	}
	end := strings.Index(body[start:], `</nav>`)
	if end < 0 {
		t.Fatalf("unterminated sidebar navigation: %s", body)
	}
	return body[start : start+end]
}

func requireActiveSidebarDestination(t *testing.T, body, marker string) {
	t.Helper()
	nav := sidebarNavMarkup(t, body)
	wantCount := 0
	if marker != "" {
		wantCount = 1
	}
	if got := strings.Count(nav, `aria-current="page"`); got != wantCount {
		t.Fatalf("active sidebar destination count = %d, want %d: %s", got, wantCount, nav)
	}
	if marker == "" {
		return
	}
	markerIndex := strings.Index(nav, marker)
	if markerIndex < 0 {
		t.Fatalf("missing sidebar destination %q: %s", marker, nav)
	}
	start := strings.LastIndex(nav[:markerIndex], "<a ")
	end := strings.Index(nav[markerIndex:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("invalid sidebar destination markup for %q: %s", marker, nav)
	}
	if tag := nav[start : markerIndex+end]; !strings.Contains(tag, `aria-current="page"`) {
		t.Fatalf("sidebar destination %q is not active: %s", marker, tag)
	}
}

func TestUISidebarHighlightsOnlyActiveDestination(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-sidebar-active")

	requireActiveSidebarDestination(t, e.uiGet(t, "/projects", token), `data-sidebar-view="projects"`)
	for _, path := range []string{"/projects/new", "/issues/new", "/settings", "/tokens"} {
		requireActiveSidebarDestination(t, e.uiGet(t, path, token), "")
	}
	requireActiveSidebarDestination(t, e.uiGet(t, e.projectPath()+"/sprint", token), "")

	if err := e.store.FavoriteProject(e.ctx, user.ID, e.projectID); err != nil {
		t.Fatalf("FavoriteProject: %v", err)
	}
	projectMarker := `data-sidebar-project-id="` + e.projectID.String() + `"`
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "Sidebar child issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, path := range []string{
		e.projectPath() + "/sprint",
		e.issuePath(issue),
		e.projectPath() + "/context",
		e.projectPath() + "/issues/new",
	} {
		requireActiveSidebarDestination(t, e.uiGet(t, path, token), projectMarker)
	}
}

func TestUIProjectsPageListsVisibleProjectsAndCreatesProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-projects")
	hidden, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "Hidden UI Project", "")
	if err != nil {
		t.Fatalf("CreateProject hidden: %v", err)
	}

	body := e.uiGet(t, "/projects", token)
	for _, want := range []string{"Projects", "Projects you can access.", `aria-label="New project"`, `href="/projects/new"`, `hx-get="/projects/new/panel"`, e.projKey, "http-test", "inline-flex w-fit justify-self-start", `href="` + e.projectPath() + `/sprint"`, `hx-get="` + e.projectPath() + `/sprint/panel"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("projects body missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{`id="project-key"`, `>Create project<`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("projects body still included project form %q: %s", notWant, body)
		}
	}

	// Every row names its owner on the trailing edge, whether the viewer owns
	// the project or is a member of someone else's.
	ownProject, err := e.store.CreateProjectForUser(e.ctx, user.ID, uniqueProjectKey(t), "Viewer Owned Project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser own: %v", err)
	}
	body = e.uiGet(t, "/projects", token)
	for _, tc := range []struct {
		project  string
		username string
	}{
		{project: "http-test", username: e.ownerUsername},
		{project: ownProject.Name, username: user.Username},
	} {
		rowStart := strings.Index(body, ">"+tc.project+"</span>")
		if rowStart < 0 {
			t.Fatalf("projects body missing row %q: %s", tc.project, body)
		}
		rowEnd := strings.Index(body[rowStart:], "</a>")
		if rowEnd < 0 {
			t.Fatalf("projects row %q is not closed: %s", tc.project, body)
		}
		row := body[rowStart : rowStart+rowEnd]
		for _, want := range []string{
			`data-project-owner title="Owned by @` + tc.username + `"`,
			`<span class="sr-only">Owned by</span>`,
			`<span class="hidden min-w-0 truncate sm:block">@` + tc.username + `</span>`,
			`class="grid h-6 w-6 shrink-0 place-items-center border border-slate-300`,
		} {
			if !strings.Contains(row, want) {
				t.Fatalf("projects row %q missing owner markup %q: %s", tc.project, want, row)
			}
		}
		requireMarkupOrder(t, row, "data-project-owner", `data-lucide="chevron-right"`)
		if strings.Count(row, "<a ") != 0 {
			t.Fatalf("projects row %q nested a link inside the row link: %s", tc.project, row)
		}
	}
	if !strings.Contains(body, "grid-cols-[2.25rem_4.5rem_minmax(0,1fr)_auto_auto]") {
		t.Fatalf("projects list should reserve a column for the owner: %s", body)
	}
	if strings.Contains(body, `href="`+e.projectPath()+`/backlog"`) {
		t.Fatalf("projects body included backlog row action: %s", body)
	}
	if strings.Contains(body, hidden.Name) {
		t.Fatalf("projects body included inaccessible project: %s", body)
	}

	body = e.uiGet(t, "/projects/new", token)
	for _, want := range []string{"New project", "Create project", `action="/projects"`, `id="project-key"`, `id="project-name"`, `id="project-description"`, `placeholder="What is this project for? Markdown is supported."`} {
		if !strings.Contains(body, want) {
			t.Fatalf("new project body missing %q: %s", want, body)
		}
	}
	newProjectMain := mainContentBlock(t, body)
	for _, notWant := range []string{`data-lucide="arrow-left"`, `href="/projects"`, `hx-get="/projects/panel"`} {
		if strings.Contains(newProjectMain, notWant) {
			t.Fatalf("new project body should not render back button markup %q: %s", notWant, body)
		}
	}

	form := url.Values{"key": {"bad"}, "name": {"Bad"}}
	res := e.uiDoNoRedirect(t, http.MethodPost, "/projects", token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Key must match") || !strings.Contains(body, "New project") || !strings.Contains(body, `value="bad"`) {
		t.Fatalf("bad key code = %d body = %s", res.StatusCode, body)
	}

	dupKey := uniqueProjectKey(t)
	if _, err := e.store.CreateProjectForUser(e.ctx, user.ID, dupKey, "Duplicate Source", ""); err != nil {
		t.Fatalf("CreateProjectForUser duplicate source: %v", err)
	}
	form = url.Values{"key": {dupKey}, "name": {"Duplicate"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, "/projects", token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusConflict || !strings.Contains(body, "Project key already exists.") || !strings.Contains(body, "New project") {
		t.Fatalf("duplicate code = %d body = %s", res.StatusCode, body)
	}

	key := uniqueProjectKey(t)
	form = url.Values{"key": {key}, "name": {"Created UI Project"}, "description": {"from UI"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, "/projects", token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("create code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	loc := res.Header.Get("Location")
	if loc != "/"+user.Username+"/projects/"+key+"/sprint" {
		t.Fatalf("Location = %q", loc)
	}
	body = e.uiGet(t, loc, token)
	if !strings.Contains(body, "Created UI Project") {
		t.Fatalf("created project page missing values: %s", body)
	}
}

func TestUIOwnerProjectListingsAndBreadcrumbAccess(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, memberToken := e.mustProjectMemberToken(t, "ui-owner-list-member")
	readonly, readonlyToken := e.mustUserToken(t, "ui-owner-list-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}

	hidden, err := e.store.CreateProjectForUser(e.ctx, e.adminID, uniqueProjectKey(t), "Owner hidden project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser hidden: %v", err)
	}
	publicProject, err := e.store.CreateProjectForUser(e.ctx, e.adminID, uniqueProjectKey(t), "Owner public project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser public: %v", err)
	}
	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, publicProject.ID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings public: %v", err)
	}
	deleted, err := e.store.CreateProjectForUser(e.ctx, e.adminID, uniqueProjectKey(t), "Owner deleted project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser deleted: %v", err)
	}
	if err := e.store.DeleteProject(e.ctx, deleted.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	owned, err := e.store.CreateProjectForUser(e.ctx, member.ID, uniqueProjectKey(t), "Member own project", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser member: %v", err)
	}

	ownerPath := "/" + e.ownerUsername + "/projects"
	body := e.uiGet(t, ownerPath, memberToken)
	for _, want := range []string{"@" + e.ownerUsername + " projects", "Projects owned by @" + e.ownerUsername + " that you can access.", "http-test", publicProject.Name} {
		if !strings.Contains(body, want) {
			t.Fatalf("owner project listing missing %q: %s", want, body)
		}
	}
	// The owner listing already names its owner in the heading, so rows skip
	// the per-row owner and its column.
	for _, notWant := range []string{hidden.Name, deleted.Name, owned.Name, `aria-label="New project"`, "data-project-owner", "grid-cols-[2.25rem_4.5rem_minmax(0,1fr)_auto_auto]"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("owner project listing leaked or rendered %q: %s", notWant, body)
		}
	}
	panelBody := e.uiGet(t, ownerPath+"/panel", memberToken)
	if !strings.Contains(panelBody, publicProject.Name) || strings.Contains(panelBody, hidden.Name) {
		t.Fatalf("owner projects panel did not preserve access filtering: %s", panelBody)
	}

	for label, tc := range map[string]struct {
		path  string
		token string
	}{
		"writable member": {path: e.projectPath() + "/all", token: memberToken},
		"readonly member": {path: e.projectPath() + "/all", token: readonlyToken},
		"public viewer":   {path: "/" + publicProject.OwnerUsername + "/projects/" + publicProject.Key + "/all"},
	} {
		t.Run(label, func(t *testing.T) {
			res := e.uiDoNoRedirect(t, http.MethodGet, tc.path, tc.token, nil)
			defer res.Body.Close()
			page := readBody(t, res)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("project page code = %d body = %s", res.StatusCode, page)
			}
			for _, want := range []string{`href="` + ownerPath + `"`, `hx-get="` + ownerPath + `/panel"`, `>@` + e.ownerUsername + `</a>`} {
				if !strings.Contains(page, want) {
					t.Fatalf("project breadcrumb missing %q: %s", want, page)
				}
			}
		})
	}
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "Owner breadcrumb issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, path := range []string{e.issuePath(issue), e.issuePath(issue) + "/context"} {
		issueBody := e.uiGet(t, path, memberToken)
		if !strings.Contains(issueBody, `href="`+ownerPath+`"`) || !strings.Contains(issueBody, `>@`+e.ownerUsername+`</a>`) {
			t.Fatalf("issue hierarchy missing owner breadcrumb at %s: %s", path, issueBody)
		}
	}

	ownPath := "/" + owned.OwnerUsername + "/projects/" + owned.Key + "/all"
	ownBody := e.uiGet(t, ownPath, memberToken)
	if strings.Contains(mainContentBlock(t, ownBody), `href="/`+member.Username+`/projects"`) {
		t.Fatalf("own project rendered redundant owner breadcrumb: %s", ownBody)
	}

	res := e.uiDoNoRedirect(t, http.MethodGet, ownerPath, "", nil)
	defer res.Body.Close()
	publicBody := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(publicBody, publicProject.Name) || strings.Contains(publicBody, "http-test") || strings.Contains(publicBody, hidden.Name) {
		t.Fatalf("anonymous owner listing code = %d body = %s", res.StatusCode, publicBody)
	}
	res = e.uiDoNoRedirect(t, http.MethodGet, ownerPath+"/panel", "", nil)
	defer res.Body.Close()
	publicPanelBody := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(publicPanelBody, publicProject.Name) || strings.Contains(publicPanelBody, "http-test") {
		t.Fatalf("anonymous owner panel code = %d body = %s", res.StatusCode, publicPanelBody)
	}

	missingPath := "/missingowner" + strings.ToLower(uniqueProjectKey(t)) + "/projects"
	res = e.uiDoNoRedirect(t, http.MethodGet, missingPath, memberToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown owner code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	res = e.uiDoNoRedirect(t, http.MethodGet, missingPath+"/panel", memberToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown owner panel code = %d body = %s", res.StatusCode, readBody(t, res))
	}

	res = e.uiDoNoRedirect(t, http.MethodGet, "/not-valid!/projects", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Fatalf("invalid anonymous owner route code = %d location = %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestUIProjectAboutStats(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-stats")
	todoIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "about stats todo",
		AssigneeID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue todo: %v", err)
	}
	_ = todoIssue
	doneIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "about stats done",
		AssigneeID: &user.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, doneIssue.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("UpdateIssue done: %v", err)
	}

	body := e.uiGet(t, e.projectPath()+"/about", token)
	for _, want := range []string{"Issue stats", "All time", "Last 7 days", "Completion rate", "Weekly snapshot for the last 12 weeks", "Not enough history for a trend yet.", "Weekly ticket completion rate summary", "50%", "Top assignees", "ui-stats", ">2</td>", ">1</td>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("project about stats missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "No assigned issues.") {
		t.Fatalf("project about stats rendered empty assignee state: %s", body)
	}
}

func TestUIProjectAboutShowsAccessSettings(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	readonly, readonlyToken := e.mustUserToken(t, "ui-about-access-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	membersPath := e.projectPath() + "/members"
	manageLink := `href="` + membersPath + `" aria-label="Manage project access" hx-get="` + membersPath + `" hx-target="#main" hx-push-url="` + membersPath + `"`

	privateBody := e.uiGet(t, e.projectPath()+"/about", e.authToken)
	for _, want := range []string{"Access", `data-project-visibility="private"`, `data-lucide="lock"`, ">Private<", "Only members can view this project.", "Issue creation", "Members only", manageLink, `data-lucide="settings-2"`} {
		if !strings.Contains(privateBody, want) {
			t.Fatalf("private project about missing %q: %s", want, privateBody)
		}
	}
	for _, notWant := range []string{`data-project-visibility="public"`, "Any signed-in user"} {
		if strings.Contains(privateBody, notWant) {
			t.Fatalf("private project about rendered %q: %s", notWant, privateBody)
		}
	}

	readonlyBody := e.uiGet(t, e.projectPath()+"/about", readonlyToken)
	if !strings.Contains(readonlyBody, `data-project-visibility="private"`) || strings.Contains(readonlyBody, manageLink) {
		t.Fatalf("readonly member should see visibility without the manage link: %s", readonlyBody)
	}

	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings public: %v", err)
	}
	publicBody := e.uiGet(t, e.projectPath()+"/about", e.authToken)
	for _, want := range []string{`data-project-visibility="public"`, `data-lucide="globe"`, ">Public<", "Anyone can view this project, even without signing in.", "Members only", manageLink} {
		if !strings.Contains(publicBody, want) {
			t.Fatalf("public project about missing %q: %s", want, publicBody)
		}
	}
	if strings.Contains(publicBody, `data-project-visibility="private"`) {
		t.Fatalf("public project about still rendered private badge: %s", publicBody)
	}

	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true, PublicIssueCreation: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings public issue creation: %v", err)
	}
	res := e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/about", "", nil)
	defer res.Body.Close()
	anonymousBody := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("anonymous public about code = %d body = %s", res.StatusCode, anonymousBody)
	}
	for _, want := range []string{`data-project-visibility="public"`, "Any signed-in user"} {
		if !strings.Contains(anonymousBody, want) {
			t.Fatalf("anonymous public about missing %q: %s", want, anonymousBody)
		}
	}
	for _, notWant := range []string{"Members only", manageLink} {
		if strings.Contains(anonymousBody, notWant) {
			t.Fatalf("anonymous public about rendered %q: %s", notWant, anonymousBody)
		}
	}
}

func TestUIProjectCompletionHistoryVisibleToReadonlyAndPublicReaders(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "completion reader issue"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	// The handler dates the chart from the Go clock while these rows are stamped
	// by Postgres, so a database clock running ahead would drop the issue from
	// the final point or replay its completion away. An hour of margin settles it.
	e.backdateCompletionRows(t, issue.ID, "1 hour")
	readonly, readonlyToken := e.mustUserToken(t, "ui-completion-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}

	readonlyBody := e.uiGet(t, e.projectPath()+"/about", readonlyToken)
	for _, want := range []string{"Completion rate", `role="img"`, "Weekly ticket completion rate summary", "100% (1 of 1 tickets completed)"} {
		if !strings.Contains(readonlyBody, want) {
			t.Fatalf("readonly completion chart missing %q: %s", want, readonlyBody)
		}
	}

	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings: %v", err)
	}
	res := e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/about", "", nil)
	defer res.Body.Close()
	publicBody := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(publicBody, "Completion rate") || !strings.Contains(publicBody, "100% (1 of 1 tickets completed)") {
		t.Fatalf("public completion chart code = %d body = %s", res.StatusCode, publicBody)
	}
}

// backdateCompletionRows moves an issue and its changelog out of the window
// where the Postgres and Go clocks have to agree for a completion chart to
// render the counts a test expects.
func (e *httpEnv) backdateCompletionRows(t *testing.T, issueID uuid.UUID, age string) {
	t.Helper()
	if _, err := e.pool.Exec(e.ctx,
		"UPDATE issues SET created_at = now() - $2::interval WHERE id = $1", issueID, age); err != nil {
		t.Fatalf("backdate issue created_at: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx,
		"UPDATE project_changelog_entries SET created_at = now() - $2::interval WHERE issue_id = $1", issueID, age); err != nil {
		t.Fatalf("backdate changelog entries: %v", err)
	}
}

func TestUIProjectMemberManagerAndReadonlyRendering(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	readonly, readonlyToken := e.mustUserToken(t, "ui-readonly")
	adminBody := e.uiGet(t, e.projectPath()+"/planned", e.authToken)
	membersPath := e.projectPath() + "/members"
	for _, want := range []string{"Members", `data-lucide="users"`, `href="` + membersPath + `"`, `hx-get="` + membersPath + `/panel"`, `hx-push-url="` + membersPath + `"`} {
		if !strings.Contains(adminBody, want) {
			t.Fatalf("owner project menu missing %q: %s", want, adminBody)
		}
	}

	pageBody := e.uiGet(t, membersPath, e.authToken)
	for _, want := range []string{"Project members", "Current access", "Add member", "Owner", "Search existing users", "Readonly", "Public access", "Public read-only access", "Allow public issue creation", "Blocked users", "Exact username", `data-modal-open="project-member-create"`, `id="project-member-create" data-client-modal class="fixed inset-0 z-50 hidden`, `data-modal-open="project-block-create"`, `id="project-block-create" data-client-modal class="fixed inset-0 z-50 hidden`, `role="dialog"`} {
		if !strings.Contains(pageBody, want) {
			t.Fatalf("member page missing %q: %s", want, pageBody)
		}
	}
	candidatesPath := e.projectPath() + "/member-candidates"
	for _, want := range []string{
		`name="username" value=""`,
		`hx-get="` + candidatesPath + `"`,
		`hx-trigger="input changed delay:300ms"`,
		`hx-target="#project-member-options"`,
		`hx-swap="outerHTML"`,
		`data-search data-search-collapsible data-project-search`,
		`id="project-member-options" data-search-options hidden`,
	} {
		if !strings.Contains(pageBody, want) {
			t.Fatalf("member search missing %q: %s", want, pageBody)
		}
	}
	if strings.Contains(pageBody, "@"+readonly.Username) {
		t.Fatalf("member page disclosed candidate before a search: %s", pageBody)
	}
	invalidMemberResponse := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/members", e.authToken, strings.NewReader(url.Values{"username": {""}, "role": {"member"}}.Encode()))
	invalidMemberBody := readBody(t, invalidMemberResponse)
	invalidMemberResponse.Body.Close()
	if invalidMemberResponse.StatusCode != http.StatusOK || !strings.Contains(invalidMemberBody, "Choose an existing user.") || !strings.Contains(invalidMemberBody, `id="project-member-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("invalid member should keep modal open, code = %d body = %s", invalidMemberResponse.StatusCode, invalidMemberBody)
	}
	for _, query := range []string{"", "   ", readonly.Username[:1]} {
		body := e.uiGet(t, candidatesPath+"?username="+url.QueryEscape(query), e.authToken)
		if strings.Contains(body, "@"+readonly.Username) || !strings.Contains(body, `id="project-member-options" data-search-options hidden`) {
			t.Fatalf("short member search %q returned candidates: %s", query, body)
		}
	}
	searchBody := e.uiGet(t, candidatesPath+"?username="+url.QueryEscape(readonly.Username), e.authToken)
	if !strings.Contains(searchBody, "@"+readonly.Username) || strings.Contains(searchBody, readonly.Email) || strings.Contains(searchBody, `data-search-options hidden`) {
		t.Fatalf("intentional member search response = %s", searchBody)
	}

	form := url.Values{"username": {readonly.Username}, "role": {"readonly"}}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/members", e.authToken, strings.NewReader(form.Encode()))
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "@"+readonly.Username) || !strings.Contains(body, `value="readonly" selected`) {
		t.Fatalf("add readonly response code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, `id="project-member-create" data-client-modal class="fixed inset-0 z-50 hidden`) {
		t.Fatalf("successful member add should close modal: %s", body)
	}

	accessForm := url.Values{"is_public": {"on"}, "public_issue_creation": {"on"}}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/member-access", e.authToken, strings.NewReader(accessForm.Encode()))
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `name="is_public" checked`) || !strings.Contains(body, `name="public_issue_creation" checked`) {
		t.Fatalf("update public access response code = %d body = %s", res.StatusCode, body)
	}
	settings, err := e.store.GetProjectAccessSettings(e.ctx, e.projectID)
	if err != nil || !settings.IsPublic || !settings.PublicIssueCreation {
		t.Fatalf("public access settings = %+v, %v", settings, err)
	}

	readonlyBody := e.uiGet(t, e.projectPath()+"/planned", readonlyToken)
	for _, notWant := range []string{"Members", `aria-label="New issue"`, `aria-label="Edit project name"`, `aria-label="New planned sprint"`} {
		if strings.Contains(readonlyBody, notWant) {
			t.Fatalf("readonly project rendered mutation control %q: %s", notWant, readonlyBody)
		}
	}
	if !strings.Contains(readonlyBody, `aria-label="Favorite project"`) {
		t.Fatalf("readonly project missing personal favorite: %s", readonlyBody)
	}
	res = e.uiDoNoRedirect(t, http.MethodGet, membersPath, readonlyToken, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("readonly member page code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	res.Body.Close()
	res = e.uiDoNoRedirect(t, http.MethodGet, candidatesPath+"?username="+url.QueryEscape(readonly.Username), readonlyToken, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("readonly member search code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	res.Body.Close()

	res = e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/name/edit", readonlyToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("readonly direct edit code = %d body = %s", res.StatusCode, readBody(t, res))
	}

	blocked, _ := e.mustUserToken(t, "ui-project-blocked")
	invalidBlockResponse := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/member-blocks", e.authToken, strings.NewReader(url.Values{"username": {""}}.Encode()))
	invalidBlockBody := readBody(t, invalidBlockResponse)
	invalidBlockResponse.Body.Close()
	if invalidBlockResponse.StatusCode != http.StatusOK || !strings.Contains(invalidBlockBody, "Enter an exact existing username.") || !strings.Contains(invalidBlockBody, `id="project-block-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("invalid block should keep modal open, code = %d body = %s", invalidBlockResponse.StatusCode, invalidBlockBody)
	}
	blockForm := url.Values{"username": {blocked.Username}}
	blockResponse := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/member-blocks", e.authToken, strings.NewReader(blockForm.Encode()))
	blockBody := readBody(t, blockResponse)
	blockResponse.Body.Close()
	if blockResponse.StatusCode != http.StatusOK || !strings.Contains(blockBody, "@"+blocked.Username) || !strings.Contains(blockBody, "Unblock") {
		t.Fatalf("block user response code = %d body = %s", blockResponse.StatusCode, blockBody)
	}
	if !strings.Contains(blockBody, `id="project-block-create" data-client-modal class="fixed inset-0 z-50 hidden`) {
		t.Fatalf("successful block should close modal: %s", blockBody)
	}
	unblockResponse := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/member-blocks/"+blocked.Username+"/delete", e.authToken, nil)
	unblockBody := readBody(t, unblockResponse)
	unblockResponse.Body.Close()
	if unblockResponse.StatusCode != http.StatusOK || strings.Contains(unblockBody, ">Unblock</button>") {
		t.Fatalf("unblock user response code = %d body = %s", unblockResponse.StatusCode, unblockBody)
	}
	blocks, err := e.store.ListProjectUserBlocks(e.ctx, e.projectID)
	if err != nil || len(blocks) != 0 {
		t.Fatalf("blocks after UI unblock = %+v, %v", blocks, err)
	}
}

func TestUIProjectNameAndDescriptionEditing(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-project-edit")

	body := e.uiGet(t, e.projectPath()+"/about", token)
	for _, want := range []string{`aria-label="Edit project name"`, `hx-get="` + e.projectPath() + `/name/edit?view=about"`, `aria-label="Edit project description"`, `hx-get="` + e.projectPath() + `/description/edit"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project edit body missing %q: %s", want, body)
		}
	}

	editName := e.uiGet(t, e.projectPath()+"/name/edit?view=about", token)
	for _, want := range []string{`method="post" action="` + e.projectPath() + `/name"`, `hx-post="` + e.projectPath() + `/name"`, `name="view" value="about"`, `name="name"`, `aria-label="Save project name"`, `aria-label="Cancel editing project name"`} {
		if !strings.Contains(editName, want) {
			t.Fatalf("project name edit missing %q: %s", want, editName)
		}
	}

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.projectPath()+"/name", token, strings.NewReader(url.Values{"view": {"about"}, "name": {"Renamed UI Project"}}.Encode()), map[string]string{
		"HX-Request":     "true",
		"HX-Current-URL": e.ts.URL + e.projectPath() + "/about",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Renamed UI Project") || strings.Contains(body, `name="name"`) {
		t.Fatalf("project name update code = %d body = %s", res.StatusCode, body)
	}
	if push := res.Header.Get("HX-Push-Url"); push != "" {
		t.Fatalf("project name update HX-Push-Url = %q, want empty", push)
	}
	project, err := e.store.GetProject(e.ctx, e.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if project.Name != "Renamed UI Project" {
		t.Fatalf("project name = %q", project.Name)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/name", token, strings.NewReader(url.Values{"view": {"about"}, "name": {" "}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Name required, max 200 chars.") || !strings.Contains(body, `name="name"`) {
		t.Fatalf("blank project name code = %d body = %s", res.StatusCode, body)
	}

	editDescription := e.uiGet(t, e.projectPath()+"/description/edit", token)
	for _, want := range []string{`method="post" action="` + e.projectPath() + `/description"`, `hx-post="` + e.projectPath() + `/description"`, `name="description"`, `aria-label="Save project description"`, `aria-label="Cancel editing project description"`} {
		if !strings.Contains(editDescription, want) {
			t.Fatalf("project description edit missing %q: %s", want, editDescription)
		}
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/description", token, strings.NewReader(url.Values{"description": {"**updated description**"}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<strong>updated description</strong>`) || !strings.Contains(body, `class="markdown-body`) || strings.Contains(body, `**updated description**`) || strings.Contains(body, `name="description"`) {
		t.Fatalf("project description update code = %d body = %s", res.StatusCode, body)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/description", token, strings.NewReader(url.Values{"description": {" \n\t "}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No description.") || strings.Contains(body, "updated description") {
		t.Fatalf("project description clear code = %d body = %s", res.StatusCode, body)
	}
}

func TestUIProjectFavorites(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-favorite")

	body := e.uiGet(t, e.projectPath()+"/about", token)
	for _, want := range []string{`id="project-favorite-action"`, `aria-label="Favorite project"`, `aria-pressed="false"`, `method="post" action="` + e.projectPath() + `/favorite"`, `hx-post="` + e.projectPath() + `/favorite"`, `name="view" value="about"`, `data-lucide="star"`, `id="sidebar-favorites"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("project favorite body missing %q: %s", want, body)
		}
	}

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.projectPath()+"/favorite", token, strings.NewReader(url.Values{"view": {"about"}}.Encode()), map[string]string{
		"HX-Request":     "true",
		"HX-Current-URL": e.ts.URL + e.projectPath() + "/about",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	for _, want := range []string{`id="project-favorite-action"`, `aria-label="Unfavorite project"`, `aria-pressed="true"`, `fill-current`, `id="sidebar-favorites"`, `hx-swap-oob="true"`, `border-t border-slate-200`, e.projKey, `href="` + e.projectPath() + `/sprint"`, `hx-get="` + e.projectPath() + `/sprint/panel"`, `aria-current="page"`} {
		if res.StatusCode != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("favorite response code = %d missing %q: %s", res.StatusCode, want, body)
		}
	}

	body = e.uiGet(t, "/me", token)
	for _, want := range []string{`id="sidebar-favorites"`, e.projKey, `href="` + e.projectPath() + `/sprint"`, `data-sidebar-favorite`} {
		if !strings.Contains(body, want) {
			t.Fatalf("favorite sidebar missing %q: %s", want, body)
		}
	}
	sidebarStart := strings.Index(body, `<nav class="scrollbar-none`)
	if sidebarStart < 0 {
		t.Fatalf("missing sidebar nav: %s", body)
	}
	requireMarkupOrder(t, body[sidebarStart:], ">Projects</span>", `data-sidebar-favorite`)

	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.projectPath()+"/favorite", token, strings.NewReader(url.Values{"view": {"about"}}.Encode()), map[string]string{
		"HX-Request":     "true",
		"HX-Current-URL": e.ts.URL + e.projectPath() + "/about",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `aria-label="Favorite project"`) || !strings.Contains(body, `hx-swap-oob="true"`) || !strings.Contains(body, `class="hidden"`) || strings.Contains(body, `aria-current="page"`) {
		t.Fatalf("unfavorite response code = %d body = %s", res.StatusCode, body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/favorite", token, strings.NewReader(url.Values{"view": {"unknown"}}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("favorite redirect code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if loc := res.Header.Get("Location"); loc != e.projectPath()+"/sprint" {
		t.Fatalf("favorite redirect Location = %q", loc)
	}
	body = e.uiGet(t, e.projectPath()+"/sprint", token)
	if !strings.Contains(body, `aria-label="Unfavorite project"`) || !strings.Contains(body, `aria-pressed="true"`) {
		t.Fatalf("favorite redirect did not persist active state: %s", body)
	}
}

func TestUIRendersProjectSprintBoard(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-board")
	sp, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Board Sprint",
		Goal:      "Focus current sprint goals\nShip board clarity",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sp.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("UpdateSprint active: %v", err)
	}
	earlyDue, err := model.ParseDate("2099-06-24")
	if err != nil {
		t.Fatalf("ParseDate early: %v", err)
	}
	lateDue, err := model.ParseDate("2099-06-26")
	if err != nil {
		t.Fatalf("ParseDate late: %v", err)
	}
	todo, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "board todo issue", AssigneeID: &user.ID, DueDate: &earlyDue})
	if err != nil {
		t.Fatalf("CreateIssue todo: %v", err)
	}
	laterTodo, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "board later todo issue", Priority: model.PriorityP1, DueDate: &lateDue})
	if err != nil {
		t.Fatalf("CreateIssue later todo: %v", err)
	}
	inProgress, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "board progress issue", Priority: model.PriorityP0})
	if err != nil {
		t.Fatalf("CreateIssue progress: %v", err)
	}
	closedIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "board closed issue"})
	if err != nil {
		t.Fatalf("CreateIssue closed: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, todo.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign todo: %v", err)
	}
	for i, status := range []model.Status{model.StatusTodo, model.StatusDone, model.StatusClosed} {
		child, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{ParentIssueID: todo.ID, Title: fmt.Sprintf("board sub-issue %d", i+1)})
		if err != nil {
			t.Fatalf("CreateSubIssue %d: %v", i+1, err)
		}
		params := store.UpdateIssueParams{Status: &status}
		if status == model.StatusClosed {
			reason := model.CloseReasonDuplicate
			params.CloseReason = &reason
		}
		if _, err := e.store.UpdateIssue(e.ctx, child.ID, params); err != nil {
			t.Fatalf("UpdateIssue sub-issue %d: %v", i+1, err)
		}
	}
	if _, err := e.store.UpdateIssue(e.ctx, laterTodo.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign later todo: %v", err)
	}
	inProgressStatus := model.StatusInProgress
	if _, err := e.store.UpdateIssue(e.ctx, inProgress.ID, store.UpdateIssueParams{SprintID: &sp.ID, Status: &inProgressStatus}); err != nil {
		t.Fatalf("assign progress: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, closedIssue.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign closed: %v", err)
	}
	closedStatus := model.StatusClosed
	closedReason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, closedIssue.ID, store.UpdateIssueParams{Status: &closedStatus, CloseReason: &closedReason}); err != nil {
		t.Fatalf("close issue: %v", err)
	}
	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "Other UI Project", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, otherProject.ID, user.ID); err != nil {
		t.Fatalf("GrantProjectAccess other: %v", err)
	}
	otherSprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: otherProject.ID,
		Name:      "Other Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint other: %v", err)
	}
	if _, err := e.store.UpdateSprint(e.ctx, otherSprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("UpdateSprint other active: %v", err)
	}
	otherIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: otherProject.ID, Title: "other project sprint issue"})
	if err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, otherIssue.ID, store.UpdateIssueParams{SprintID: &otherSprint.ID}); err != nil {
		t.Fatalf("assign other: %v", err)
	}

	body := e.uiGet(t, e.projectPath()+"/sprint", token)
	for _, want := range []string{"Sprint", "To do", "In progress", "Done", "Closed", "board todo issue", "board later todo issue", "board progress issue", "board closed issue", "Board Sprint", "Focus current sprint goals\nShip board clarity", `aria-label="ui-board"`, `aria-label="Issue controls"`, "Status", "Priority", "Sort", "Direction", "Due date", "Asc", "Desc", `data-lucide="arrow-up"`, `data-lucide="arrow-down"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("sprint body missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{"other project sprint issue", "Active sprint issues across accessible projects.", `aria-label="Remove issue from sprint"`, `data-lucide="unlink"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("sprint body included %q: %s", notWant, body)
		}
	}
	for _, want := range []string{`role="progressbar" aria-label="Sub-issues completed" aria-valuemin="0" aria-valuemax="3" aria-valuenow="2"`, `pathLength="3" stroke-dasharray="2 3"`, ">2/3</span>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sprint body missing sub-issue progress %q: %s", want, body)
		}
	}
	if got := strings.Count(body, `role="progressbar" aria-label="Sub-issues completed"`); got != 1 {
		t.Fatalf("sub-issue progress count = %d, want 1: %s", got, body)
	}

	filteredBody := e.uiGet(t, e.projectPath()+"/sprint?status=in_progress&priority=P0", token)
	if !strings.Contains(filteredBody, "board progress issue") {
		t.Fatalf("filtered sprint missing progress issue: %s", filteredBody)
	}
	for _, notWant := range []string{"board todo issue", "board later todo issue", "board closed issue"} {
		if strings.Contains(filteredBody, notWant) {
			t.Fatalf("filtered sprint included %q: %s", notWant, filteredBody)
		}
	}

	dueDescBody := e.uiGet(t, e.projectPath()+"/sprint?sort=due&direction=desc", token)
	laterIdx := strings.Index(dueDescBody, "board later todo issue")
	earlyIdx := strings.Index(dueDescBody, "board todo issue")
	if laterIdx < 0 || earlyIdx < 0 || laterIdx > earlyIdx {
		t.Fatalf("due desc sprint order wrong: later=%d early=%d body=%s", laterIdx, earlyIdx, dueDescBody)
	}
}

func TestUIProjectSprintPlanningLifecycle(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint-plan")

	body := e.uiGet(t, e.projectPath()+"/planned", token)
	if !strings.Contains(body, `aria-label="New planned sprint"`) || !strings.Contains(body, `hx-get="`+e.projectPath()+`/sprints/new"`) {
		t.Fatalf("planned body missing new sprint action: %s", body)
	}
	body = e.uiGet(t, e.projectPath()+"/sprints/new", token)
	for _, want := range []string{`id="planned-sprint-create" data-client-modal class="fixed inset-0 z-50 grid`, `role="dialog" aria-modal="true" aria-labelledby="planned-sprint-create-title"`, `action="` + e.projectPath() + `/sprints"`, `name="start_date"`, `name="end_date"`, `name="goal"`, `aria-label="Create planned sprint"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("new sprint form missing %q: %s", want, body)
		}
	}

	res := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints", token, strings.NewReader(url.Values{
		"name":       {"Sprint A"},
		"goal":       {"first goal"},
		"start_date": {"2026-06-01"},
		"end_date":   {"2026-06-14"},
	}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Sprint A") || !strings.Contains(body, "first goal") {
		t.Fatalf("create sprint code = %d body = %s", res.StatusCode, body)
	}
	sp, err := e.store.GetSprintByProjectNumber(e.ctx, e.projectID, 1)
	if err != nil {
		t.Fatalf("GetSprintByProjectNumber: %v", err)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints", token, strings.NewReader(url.Values{
		"name":       {"Bad dates"},
		"start_date": {"2026-06-14"},
		"end_date":   {"2026-06-01"},
	}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "End date must be on or after start date.") {
		t.Fatalf("bad sprint dates code = %d body = %s", res.StatusCode, body)
	}
	if !strings.Contains(body, `id="planned-sprint-create" data-client-modal class="fixed inset-0 z-50 grid`) {
		t.Fatalf("invalid sprint should keep modal open: %s", body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints", token, strings.NewReader(url.Values{
		"name":       {"Second planned"},
		"start_date": {"2026-06-15"},
		"end_date":   {"2026-06-30"},
	}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("create second code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	second, err := e.store.GetSprintByProjectNumber(e.ctx, e.projectID, 2)
	if err != nil {
		t.Fatalf("GetSprint second: %v", err)
	}

	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "scheduled from sprint ui"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	body = e.uiGet(t, e.projectPath()+"/sprints/"+sp.Ref+"/issues/new", token)
	if !strings.Contains(body, `placeholder="`+e.projKey+`-12"`) || !strings.Contains(body, `aria-label="Add issue to sprint"`) || !strings.Contains(body, `data-client-modal class="fixed inset-0 z-50 grid`) || !strings.Contains(body, `role="dialog" aria-modal="true"`) {
		t.Fatalf("add issue form missing: %s", body)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref+"/issues", token, strings.NewReader(url.Values{"issue": {issue.Identifier}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "scheduled from sprint ui") || strings.Contains(body, `aria-label="Remove issue from sprint"`) {
		t.Fatalf("add issue code = %d body = %s", res.StatusCode, body)
	}
	gotIssue, err := e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if gotIssue.SprintID == nil || *gotIssue.SprintID != sp.ID {
		t.Fatalf("issue sprint = %v, want %s", gotIssue.SprintID, sp.ID)
	}

	body = e.uiGet(t, e.projectPath()+"/sprints/"+sp.Ref+"/edit", token)
	if !strings.Contains(body, `value="Sprint A"`) || !strings.Contains(body, `value="2026-06-01"`) {
		t.Fatalf("edit sprint form missing values: %s", body)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref, token, strings.NewReader(url.Values{
		"name":       {"Sprint A edited"},
		"goal":       {"edited goal"},
		"start_date": {"2026-06-03"},
		"end_date":   {"2026-06-16"},
	}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Sprint A edited") || !strings.Contains(body, "edited goal") {
		t.Fatalf("edit sprint code = %d body = %s", res.StatusCode, body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref+"/move-down", token, nil)
	defer res.Body.Close()
	body = readBody(t, res)
	secondIdx := strings.Index(body, "Second planned")
	firstIdx := strings.Index(body, "Sprint A edited")
	if res.StatusCode != http.StatusOK || secondIdx < 0 || firstIdx < 0 || secondIdx > firstIdx {
		t.Fatalf("move down order wrong: second=%d first=%d body=%s", secondIdx, firstIdx, body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref+"/activate", token, nil)
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Sprint A edited") || !strings.Contains(body, `aria-label="Complete sprint"`) {
		t.Fatalf("activate sprint code = %d body = %s", res.StatusCode, body)
	}
	active, err := e.store.GetSprint(e.ctx, sp.ID)
	if err != nil {
		t.Fatalf("GetSprint active: %v", err)
	}
	if active.Status != model.SprintStatusActive {
		t.Fatalf("active status = %s", active.Status)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/sprint", token, strings.NewReader(url.Values{"sprint": {" "}}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `class="min-w-0 truncate text-slate-900 dark:text-slate-100">None</span>`) {
		t.Fatalf("clear sprint code = %d body = %s", res.StatusCode, body)
	}
	gotIssue, err = e.store.GetIssue(e.ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue after remove: %v", err)
	}
	if gotIssue.SprintID != nil {
		t.Fatalf("issue sprint after remove = %v, want nil", gotIssue.SprintID)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref+"/complete", token, nil)
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No active sprint.") {
		t.Fatalf("complete sprint code = %d body = %s", res.StatusCode, body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+second.Ref+"/delete", token, nil)
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || strings.Contains(body, "Second planned") {
		t.Fatalf("delete planned code = %d body = %s", res.StatusCode, body)
	}
}

func TestUIProjectSprintOptionalDates(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-sprint-dates")

	res := e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints", token, strings.NewReader(url.Values{
		"name":       {"No date sprint"},
		"start_date": {""},
		"end_date":   {""},
	}.Encode()))
	defer res.Body.Close()
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No date sprint") || strings.Contains(body, "No dates") {
		t.Fatalf("create no-date sprint code = %d body = %s", res.StatusCode, body)
	}
	sp, err := e.store.GetSprintByProjectNumber(e.ctx, e.projectID, 1)
	if err != nil {
		t.Fatalf("GetSprint: %v", err)
	}
	if sp.StartDate != nil || sp.EndDate != nil {
		t.Fatalf("dates = %v..%v, want nil..nil", sp.StartDate, sp.EndDate)
	}

	body = e.uiGet(t, e.projectPath()+"/sprints/"+sp.Ref+"/edit", token)
	if !strings.Contains(body, `name="start_date" value=""`) || !strings.Contains(body, `name="end_date" value=""`) {
		t.Fatalf("edit form missing blank date values: %s", body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref, token, strings.NewReader(url.Values{
		"name":       {"No date sprint"},
		"start_date": {"2026-07-01"},
		"end_date":   {""},
	}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Start and end dates must both be set, or both left blank.") {
		t.Fatalf("partial-date edit code = %d body = %s", res.StatusCode, body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref, token, strings.NewReader(url.Values{
		"name":       {"No date sprint"},
		"start_date": {"2026-07-01"},
		"end_date":   {"2026-07-14"},
	}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Jul 1-Jul 14") {
		t.Fatalf("schedule sprint code = %d body = %s", res.StatusCode, body)
	}

	res = e.uiDoNoRedirect(t, http.MethodPost, e.projectPath()+"/sprints/"+sp.Ref, token, strings.NewReader(url.Values{
		"name":       {"No date sprint"},
		"start_date": {""},
		"end_date":   {""},
	}.Encode()))
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "No date sprint") || strings.Contains(body, "No dates") {
		t.Fatalf("clear sprint dates code = %d body = %s", res.StatusCode, body)
	}
}

func TestUIProjectAssigneeFilterAppliesAcrossProjectSections(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	alice, token := e.mustProjectMemberToken(t, "ui-filter-alice")
	bob, _ := e.mustProjectMemberToken(t, "ui-filter-bob")
	var err error
	alice, err = e.store.UpdateUserProfile(e.ctx, alice.ID, "Alice Filter", alice.Email)
	if err != nil {
		t.Fatalf("UpdateUserProfile alice: %v", err)
	}
	bob, err = e.store.UpdateUserProfile(e.ctx, bob.ID, "Bob Filter", bob.Email)
	if err != nil {
		t.Fatalf("UpdateUserProfile bob: %v", err)
	}
	activeSprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Filtered Active Sprint",
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
		Name:      "Filtered Planned Sprint",
		StartDate: datePtr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}
	aliceSprint := createAssignedIssueForUI(t, e, "alice sprint issue", alice.ID)
	bobSprint := createAssignedIssueForUI(t, e, "bob sprint issue", bob.ID)
	unassignedSprint, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "unassigned sprint issue"})
	if err != nil {
		t.Fatalf("CreateIssue unassigned sprint: %v", err)
	}
	for _, issue := range []model.Issue{aliceSprint, bobSprint, unassignedSprint} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &activeSprint.ID}); err != nil {
			t.Fatalf("assign active %s: %v", issue.Identifier, err)
		}
	}
	alicePlanned := createAssignedIssueForUI(t, e, "alice planned issue", alice.ID)
	bobPlanned := createAssignedIssueForUI(t, e, "bob planned issue", bob.ID)
	for _, issue := range []model.Issue{alicePlanned, bobPlanned} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &plannedSprint.ID}); err != nil {
			t.Fatalf("assign planned %s: %v", issue.Identifier, err)
		}
	}
	aliceBacklog := createAssignedIssueForUI(t, e, "alice backlog issue", alice.ID)
	bobBacklog := createAssignedIssueForUI(t, e, "bob backlog issue", bob.ID)
	doneStatus := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, bobBacklog.ID, store.UpdateIssueParams{Status: &doneStatus}); err != nil {
		t.Fatalf("set bob backlog done: %v", err)
	}

	aliceQuery := "?assignee_id=" + alice.ID.String()
	body := e.uiGet(t, e.projectPath()+"/sprint"+aliceQuery, token)
	for _, want := range []string{
		`aria-label="Issue controls"`,
		`aria-label="Toggle Alice Filter"`,
		`aria-label="Toggle Bob Filter"`,
		`aria-pressed="true"`,
		"AF",
		"BF",
		"alice sprint issue",
		`href="` + e.projectPath() + `/planned"`,
		`href="` + e.projectPath() + `/all"`,
		`assignee_id=` + bob.ID.String(),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("filtered sprint missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{"bob sprint issue", "unassigned sprint issue"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("filtered sprint included %q: %s", notWant, body)
		}
	}

	body = e.uiGet(t, e.projectPath()+"/all"+aliceQuery, token)
	for _, want := range []string{"alice sprint issue", "alice planned issue", "alice backlog issue"} {
		if !strings.Contains(body, want) {
			t.Fatalf("filtered all issues missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{"bob sprint issue", "bob planned issue", "bob backlog issue", "unassigned sprint issue"} {
		if strings.Contains(body, notWant) {
			t.Fatalf("filtered all issues included %q: %s", notWant, body)
		}
	}

	multiAllQuery := "?assignee_id=" + alice.ID.String() + "&assignee_id=" + bob.ID.String() + "&status=todo&status=done"
	body = e.uiGet(t, e.projectPath()+"/all"+multiAllQuery, token)
	for _, want := range []string{
		`aria-label="Issue controls"`,
		`aria-pressed="true"`,
		"alice sprint issue",
		"bob sprint issue",
		"alice planned issue",
		"bob planned issue",
		"alice backlog issue",
		"bob backlog issue",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("multi-filter all issues missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "unassigned sprint issue") {
		t.Fatalf("multi-filter all issues included unassigned issue: %s", body)
	}

	body = e.uiGet(t, e.projectPath()+"/sprint"+aliceQuery+"&assignee_id="+bob.ID.String(), token)
	for _, want := range []string{"alice sprint issue", "bob sprint issue"} {
		if !strings.Contains(body, want) {
			t.Fatalf("multi-filter sprint missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "unassigned sprint issue") || aliceBacklog.ID == uuid.Nil || bobBacklog.ID == uuid.Nil {
		t.Fatalf("multi-filter sprint included wrong issue or setup failed: %s", body)
	}
}

func TestUIRendersProjectPlannedAndAll(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-backlog")
	backlogIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "issue still in backlog"})
	if err != nil {
		t.Fatalf("CreateIssue backlog: %v", err)
	}
	firstPlanned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "First Planned Sprint",
		StartDate: datePtr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint first planned: %v", err)
	}
	secondPlanned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Second Planned Sprint",
		StartDate: datePtr(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint second planned: %v", err)
	}
	if _, err := e.store.ReorderPlannedSprints(e.ctx, store.ReorderPlannedSprintsParams{
		ProjectID: e.projectID,
		SprintIDs: []uuid.UUID{secondPlanned.ID, firstPlanned.ID},
	}); err != nil {
		t.Fatalf("ReorderPlannedSprints: %v", err)
	}
	firstPlannedIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "scheduled first issue"})
	if err != nil {
		t.Fatalf("CreateIssue first planned: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, firstPlannedIssue.ID, store.UpdateIssueParams{SprintID: &firstPlanned.ID}); err != nil {
		t.Fatalf("assign first planned: %v", err)
	}
	progressTodo, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{ParentIssueID: firstPlannedIssue.ID, Title: "planned progress todo"})
	if err != nil {
		t.Fatalf("CreateSubIssue progress todo: %v", err)
	}
	progressDone, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{ParentIssueID: firstPlannedIssue.ID, Title: "planned progress done"})
	if err != nil {
		t.Fatalf("CreateSubIssue progress done: %v", err)
	}
	doneStatus := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, progressDone.ID, store.UpdateIssueParams{Status: &doneStatus}); err != nil {
		t.Fatalf("complete progress sub-issue: %v", err)
	}
	secondPlannedIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "scheduled second issue"})
	if err != nil {
		t.Fatalf("CreateIssue second planned: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, secondPlannedIssue.ID, store.UpdateIssueParams{SprintID: &secondPlanned.ID}); err != nil {
		t.Fatalf("assign second planned: %v", err)
	}

	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "Other Backlog Project", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, otherProject.ID, user.ID); err != nil {
		t.Fatalf("GrantProjectAccess other: %v", err)
	}
	if _, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: otherProject.ID, Title: "other project backlog issue"}); err != nil {
		t.Fatalf("CreateIssue other backlog: %v", err)
	}
	otherPlanned, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: otherProject.ID,
		Name:      "Other Planned Sprint",
		StartDate: datePtr(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint other planned: %v", err)
	}
	otherPlannedIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: otherProject.ID, Title: "other project planned issue"})
	if err != nil {
		t.Fatalf("CreateIssue other planned: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, otherPlannedIssue.ID, store.UpdateIssueParams{SprintID: &otherPlanned.ID}); err != nil {
		t.Fatalf("assign other planned: %v", err)
	}

	body := e.uiGet(t, e.projectPath()+"/planned", token)
	for _, want := range []string{"Planned", "Second Planned Sprint", "First Planned Sprint", "scheduled second issue", "scheduled first issue"} {
		if !strings.Contains(body, want) {
			t.Fatalf("planned body missing %q: %s", want, body)
		}
	}
	secondIdx := strings.Index(body, "scheduled second issue")
	firstIdx := strings.Index(body, "scheduled first issue")
	if secondIdx < 0 || firstIdx < 0 || secondIdx > firstIdx {
		t.Fatalf("planned order wrong: second=%d first=%d body=%s", secondIdx, firstIdx, body)
	}
	if strings.Contains(body, backlogIssue.Title) {
		t.Fatalf("planned body included unscheduled issue: %s", body)
	}
	for _, want := range []string{`role="progressbar" aria-label="Sub-issues completed" aria-valuemin="0" aria-valuemax="2" aria-valuenow="1"`, `pathLength="2" stroke-dasharray="1 2"`, `>1/2</span>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("planned body missing sub-issue progress %q: %s", want, body)
		}
	}
	for _, notWant := range []string{"other project backlog issue", "other project planned issue", "Other Planned Sprint", "Backlog issues across accessible projects.", `aria-label="Remove issue from sprint"`, `data-lucide="unlink"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("planned body included %q: %s", notWant, body)
		}
	}

	body = e.uiGet(t, e.projectPath()+"/all", token)
	for _, want := range []string{"All issues", "Issue controls", "Status", "Priority", "Sort", "Updated", "Any", backlogIssue.Title, "scheduled first issue", "scheduled second issue"} {
		if !strings.Contains(body, want) {
			t.Fatalf("all body missing %q: %s", want, body)
		}
	}
	backlogIdx := strings.Index(body, backlogIssue.Title)
	firstIdx = strings.Index(body, "scheduled first issue")
	secondIdx = strings.Index(body, "scheduled second issue")
	if backlogIdx < 0 || firstIdx < 0 || secondIdx < 0 || secondIdx > firstIdx || firstIdx > backlogIdx {
		t.Fatalf("all issue order wrong: backlog=%d first=%d second=%d body=%s", backlogIdx, firstIdx, secondIdx, body)
	}
	for _, notWant := range []string{"Other Planned Sprint", "other project backlog issue", "other project planned issue", progressTodo.Title, progressDone.Title} {
		if strings.Contains(body, notWant) {
			t.Fatalf("all body included %q: %s", notWant, body)
		}
	}
	if got := strings.Count(body, `role="progressbar" aria-label="Sub-issues completed"`); got != 1 {
		t.Fatalf("all issue progress count = %d, want 1: %s", got, body)
	}
	if _, err := e.store.UpdateIssue(e.ctx, progressTodo.ID, store.UpdateIssueParams{Status: &doneStatus}); err != nil {
		t.Fatalf("complete remaining progress sub-issue: %v", err)
	}
	updatedBody := e.uiGet(t, e.projectPath()+"/all", token)
	for _, want := range []string{`aria-valuemax="2" aria-valuenow="2"`, `pathLength="2" stroke-dasharray="2 2"`, `>2/2</span>`} {
		if !strings.Contains(updatedBody, want) {
			t.Fatalf("updated all body missing sub-issue progress %q: %s", want, updatedBody)
		}
	}
}

// The flat lists answer "what is left", not "everything ever filed", so they
// default to open work. The sprint board is the deliberate exception: its
// columns are the statuses, and a Done column that can never fill is useless.
func TestUIProjectIssueListsDefaultToOpenWork(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-open-default")

	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: e.projectID,
		Name:      "Open Default Sprint",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	activeSprint := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sprint.ID, store.UpdateSprintParams{Status: &activeSprint}); err != nil {
		t.Fatalf("UpdateSprint active: %v", err)
	}

	todoIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "still to do issue"})
	if err != nil {
		t.Fatalf("CreateIssue todo: %v", err)
	}
	progressIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "underway issue"})
	if err != nil {
		t.Fatalf("CreateIssue progress: %v", err)
	}
	doneIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "finished issue"})
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	cancelledIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "abandoned issue"})
	if err != nil {
		t.Fatalf("CreateIssue cancelled: %v", err)
	}
	// Sprint membership has to land before completion: a completed issue cannot
	// change sprints.
	for _, issue := range []model.Issue{todoIssue, doneIssue} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sprint.ID}); err != nil {
			t.Fatalf("assign %s to sprint: %v", issue.Title, err)
		}
	}
	inProgress := model.StatusInProgress
	if _, err := e.store.UpdateIssue(e.ctx, progressIssue.ID, store.UpdateIssueParams{Status: &inProgress}); err != nil {
		t.Fatalf("start progress issue: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, doneIssue.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("complete done issue: %v", err)
	}
	closed := model.StatusClosed
	closedReason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, cancelledIssue.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &closedReason}); err != nil {
		t.Fatalf("cancel issue: %v", err)
	}

	for _, tt := range []struct {
		name    string
		path    string
		want    []string
		notWant []string
	}{
		{
			name:    "all list hides completed work",
			path:    e.projectPath() + "/all",
			want:    []string{todoIssue.Title, progressIssue.Title},
			notWant: []string{doneIssue.Title, cancelledIssue.Title},
		},
		{
			name: "any status brings it back",
			path: e.projectPath() + "/all?status=any",
			want: []string{todoIssue.Title, progressIssue.Title, doneIssue.Title, cancelledIssue.Title},
		},
		{
			name:    "an explicit status still narrows",
			path:    e.projectPath() + "/all?status=done",
			want:    []string{doneIssue.Title},
			notWant: []string{todoIssue.Title, progressIssue.Title, cancelledIssue.Title},
		},
		{
			name:    "the board keeps every status",
			path:    e.projectPath() + "/sprint",
			want:    []string{todoIssue.Title, doneIssue.Title},
			notWant: []string{progressIssue.Title},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := e.uiGet(t, tt.path, token)
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Fatalf("%s missing %q: %s", tt.path, want, body)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(body, notWant) {
					t.Fatalf("%s included %q: %s", tt.path, notWant, body)
				}
			}
		})
	}
}

func TestUIProjectDeletedPageListsAndRestoresIssues(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, token := e.mustProjectMemberToken(t, "ui-deleted")
	live, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "live issue outside deleted tab",
	})
	if err != nil {
		t.Fatalf("CreateIssue live: %v", err)
	}
	deleted, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "deleted tab target issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue deleted: %v", err)
	}
	parent, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "deleted tab parent issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	child, err := e.store.CreateSubIssue(e.ctx, store.CreateSubIssueParams{
		ParentIssueID: parent.ID,
		Title:         "deleted tab child issue",
	})
	if err != nil {
		t.Fatalf("CreateSubIssue child: %v", err)
	}
	otherProject, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "Other Deleted Project", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, otherProject.ID, user.ID); err != nil {
		t.Fatalf("GrantProjectAccess other: %v", err)
	}
	otherDeleted, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: otherProject.ID,
		Title:     "other project deleted issue",
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

	projectBody := e.uiGet(t, e.projectPath()+"/planned", token)
	for _, want := range []string{
		`aria-label="Project actions"`,
		`data-lucide="more-horizontal"`,
		`href="` + e.projectPath() + `/deleted"`,
		`hx-get="` + e.projectPath() + `/deleted/panel"`,
		"Deleted issues",
	} {
		if !strings.Contains(projectBody, want) {
			t.Fatalf("project body missing deleted menu affordance %q: %s", want, projectBody)
		}
	}
	tabStart := strings.Index(projectBody, `aria-label="Project views"`)
	if tabStart < 0 {
		t.Fatalf("project body missing tab nav: %s", projectBody)
	}
	tabEnd := strings.Index(projectBody[tabStart:], "</nav>")
	if tabEnd < 0 {
		t.Fatalf("project body missing tab nav close: %s", projectBody)
	}
	tabMarkup := projectBody[tabStart : tabStart+tabEnd]
	if strings.Contains(tabMarkup, "Deleted") || strings.Contains(tabMarkup, `/deleted`) {
		t.Fatalf("deleted rendered as project tab: %s", projectBody)
	}

	body := e.uiGet(t, e.projectPath()+"/deleted", token)
	for _, want := range []string{
		"Deleted issues",
		e.projKey,
		deleted.Identifier,
		deleted.Title,
		`href="` + e.issuePath(deleted) + `"`,
		`hx-get="` + e.issuePath(deleted) + `/panel"`,
		parent.Title,
		`method="post" action="` + e.issuePath(deleted) + `/restore"`,
		`hx-post="` + e.issuePath(deleted) + `/restore"`,
		`data-lucide="rotate-ccw"`,
		"Restore",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("deleted body missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{`data-lucide="arrow-left"`, `href="` + e.projectPath() + `/sprint"`, `hx-get="` + e.projectPath() + `/sprint/panel"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("deleted body should not render back button markup %q: %s", notWant, body)
		}
	}
	for _, notWant := range []string{live.Title, otherDeleted.Title, child.Title, `method="post" action="` + e.issuePath(child) + `/restore"`, "Issue deleted", `aria-label="Project views"`, `aria-label="Project actions"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("deleted body included %q: %s", notWant, body)
		}
	}

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, e.issuePath(child)+"/restore", token, nil, map[string]string{
		"HX-Request": "true",
	})
	defer res.Body.Close()
	body = readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("deleted restore code = %d body = %s", res.StatusCode, body)
	}
	if push := res.Header.Get("HX-Push-Url"); push != e.issuePath(child) {
		t.Fatalf("restore HX-Push-Url = %q", push)
	}
	if strings.Contains(body, "<!doctype html>") || !strings.Contains(body, child.Title) || !strings.Contains(body, "Sub-issue of") || !strings.Contains(body, parent.Title) || !strings.Contains(body, `aria-label="Issue actions"`) {
		t.Fatalf("deleted restore should render restored issue panel: %s", body)
	}
	if _, err := e.store.GetIssue(e.ctx, child.ID); err != nil {
		t.Fatalf("GetIssue child restored from deleted tab: %v", err)
	}
	if _, err := e.store.GetIssue(e.ctx, parent.ID); err != nil {
		t.Fatalf("GetIssue parent restored with child: %v", err)
	}
	body = e.uiGet(t, e.projectPath()+"/deleted", token)
	if strings.Contains(body, parent.Title) || strings.Contains(body, child.Title) {
		t.Fatalf("deleted body kept restored parent/child: %s", body)
	}
	if !strings.Contains(body, deleted.Title) {
		t.Fatalf("deleted body lost remaining deleted issue: %s", body)
	}
}
