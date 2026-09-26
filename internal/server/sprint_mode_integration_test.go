package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

type sprintModeProjectResponse struct {
	model.Project
	Favorite bool `json:"favorite"`
}

func (e *httpEnv) sprintModePath() string {
	return e.projectPath() + "/sprint-mode"
}

func (e *httpEnv) mustSetFixtureSprintsEnabled(t *testing.T, enabled bool) {
	t.Helper()
	if _, err := e.store.SetProjectSprintsEnabled(e.ctx, e.projectID, enabled); err != nil {
		t.Fatalf("SetProjectSprintsEnabled(%t): %v", enabled, err)
	}
}

func (e *httpEnv) mustFixtureSprintsEnabled(t *testing.T) bool {
	t.Helper()
	project, err := e.store.GetProject(e.ctx, e.projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	return project.SprintsEnabled
}

func (e *httpEnv) mustPlannedSprint(t *testing.T, name string) model.Sprint {
	t.Helper()
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: name})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	return sprint
}

func (e *httpEnv) mustSprintStatus(t *testing.T, id uuid.UUID) model.SprintStatus {
	t.Helper()
	sprint, err := e.store.GetSprint(e.ctx, id)
	if err != nil {
		t.Fatalf("GetSprint: %v", err)
	}
	return sprint.Status
}

func TestHTTPProjectsReportSprintsEnabled(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	code, body := e.do(t, http.MethodGet, e.projectPath(), nil)
	if code != http.StatusOK {
		t.Fatalf("get project code = %d body = %s", code, body)
	}
	if got := decode[sprintModeProjectResponse](t, body); !got.SprintsEnabled {
		t.Fatalf("fixture project sprints_enabled = false: %s", body)
	}

	code, body = e.do(t, http.MethodPost, "/projects", map[string]any{"key": uniqueProjectKey(t), "name": "Fresh"})
	if code != http.StatusCreated {
		t.Fatalf("create project code = %d body = %s", code, body)
	}
	if !strings.Contains(string(body), `"sprints_enabled":false`) {
		t.Fatalf("new project body lacks sprints_enabled=false: %s", body)
	}
}

func TestHTTPUpdateProjectSprintMode(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, memberToken := e.mustProjectMemberToken(t, "sprint-mode-member")
	_, outsiderToken := e.mustUserToken(t, "sprint-mode-outsider")

	for _, tc := range []struct {
		name  string
		token string
		body  any
		code  int
	}{
		{name: "write member cannot change sprint mode", token: memberToken, body: map[string]any{"sprints_enabled": false}, code: http.StatusForbidden},
		{name: "outsider cannot change sprint mode", token: outsiderToken, body: map[string]any{"sprints_enabled": false}, code: http.StatusForbidden},
		{name: "missing field", token: e.authToken, body: map[string]any{}, code: http.StatusBadRequest},
		{name: "wrong type", token: e.authToken, body: map[string]any{"sprints_enabled": "no"}, code: http.StatusBadRequest},
	} {
		code, body := e.doWithToken(t, tc.token, http.MethodPatch, e.sprintModePath(), tc.body)
		if code != tc.code {
			t.Fatalf("%s: code = %d body = %s, want %d", tc.name, code, body, tc.code)
		}
		if !e.mustFixtureSprintsEnabled(t) {
			t.Fatalf("%s: sprints were disabled", tc.name)
		}
	}
	if code, body := e.do(t, http.MethodPatch, "/"+e.ownerUsername+"/projects/NOPE0/sprint-mode", map[string]any{"sprints_enabled": false}); code != http.StatusNotFound {
		t.Fatalf("missing project code = %d body = %s", code, body)
	}

	running := e.mustPlannedSprint(t, "Running")
	if code, body := e.do(t, http.MethodPatch, e.sprintPath(running), map[string]any{"status": "active"}); code != http.StatusOK {
		t.Fatalf("start sprint code = %d body = %s", code, body)
	}
	code, body := e.do(t, http.MethodPatch, e.sprintModePath(), map[string]any{"sprints_enabled": false})
	if code != http.StatusConflict || !strings.Contains(string(body), "complete the active sprint to disable sprints") {
		t.Fatalf("disable with active sprint code = %d body = %s, want 409", code, body)
	}
	if !e.mustFixtureSprintsEnabled(t) {
		t.Fatal("rejected disable turned sprints off")
	}
	if code, body := e.do(t, http.MethodPost, e.sprintPath(running)+"/complete", nil); code != http.StatusOK {
		t.Fatalf("complete sprint code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPatch, e.sprintModePath(), map[string]any{"sprints_enabled": false})
	if code != http.StatusOK {
		t.Fatalf("disable code = %d body = %s", code, body)
	}
	if got := decode[sprintModeProjectResponse](t, body); got.SprintsEnabled || got.Key != e.projKey {
		t.Fatalf("disable response = %+v", got)
	}

	waiting := e.mustPlannedSprint(t, "Waiting")
	code, body = e.do(t, http.MethodPatch, e.sprintPath(waiting), map[string]any{"status": "active"})
	if code != http.StatusConflict || !strings.Contains(string(body), "sprints are disabled for this project") {
		t.Fatalf("start while disabled code = %d body = %s, want 409", code, body)
	}
	if status := e.mustSprintStatus(t, waiting.ID); status != model.SprintStatusPlanned {
		t.Fatalf("rejected start left sprint %s", status)
	}
	// Planning keeps working without sprint mode.
	if code, body := e.do(t, http.MethodPatch, e.sprintPath(waiting), map[string]any{"name": "Still planned"}); code != http.StatusOK {
		t.Fatalf("rename planned sprint while disabled code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPatch, e.sprintModePath(), map[string]any{"sprints_enabled": true})
	if code != http.StatusOK || !decode[sprintModeProjectResponse](t, body).SprintsEnabled {
		t.Fatalf("enable code = %d body = %s", code, body)
	}
	if code, body := e.do(t, http.MethodPatch, e.sprintPath(waiting), map[string]any{"status": "active"}); code != http.StatusOK {
		t.Fatalf("start after enabling code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodGet, e.projectPath()+"/changelog", nil)
	if code != http.StatusOK {
		t.Fatalf("changelog code = %d body = %s", code, body)
	}
	for _, want := range []string{"Disabled sprints for project " + e.projKey, "Enabled sprints for project " + e.projKey} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("changelog missing %q: %s", want, body)
		}
	}
}

func TestMCPProjectSprintMode(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	ownerSession := mcpConnect(t, e, e.authToken)
	_, memberToken := e.mustProjectMemberToken(t, "mcp-sprint-mode-member")
	memberSession := mcpConnect(t, e, memberToken)
	projectArgs := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
	modeArgs := func(enabled bool) map[string]any {
		return map[string]any{"owner": e.ownerUsername, "key": e.projKey, "sprints_enabled": enabled}
	}

	project := decodeMCPField[model.Project](t, mcpCall(t, e, ownerSession, "track_get_project", projectArgs), "project")
	if !project.SprintsEnabled {
		t.Fatal("track_get_project sprints_enabled = false for the fixture project")
	}
	requireMCPErrorCode(t, mcpCallExpectError(t, e, memberSession, "track_update_project_sprint_mode", modeArgs(false)), "forbidden")
	requireMCPErrorCode(t, mcpCallExpectError(t, e, ownerSession, "track_update_project_sprint_mode", map[string]any{
		"owner": e.ownerUsername, "key": "NOPE0", "sprints_enabled": false,
	}), "not_found")

	running, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Running"})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	mcpCall(t, e, ownerSession, "track_update_sprint", map[string]any{"owner": e.ownerUsername, "key": e.projKey, "sprint": running.Ref, "status": "active"})
	requireMCPErrorCode(t, mcpCallExpectError(t, e, ownerSession, "track_update_project_sprint_mode", modeArgs(false)), "conflict")
	mcpCall(t, e, ownerSession, "track_complete_sprint", map[string]any{"owner": e.ownerUsername, "key": e.projKey, "sprint": running.Ref})

	disabled := decodeMCPField[model.Project](t, mcpCall(t, e, ownerSession, "track_update_project_sprint_mode", modeArgs(false)), "project")
	if disabled.SprintsEnabled {
		t.Fatal("track_update_project_sprint_mode(false) returned sprints_enabled = true")
	}
	waiting, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Waiting"})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	requireMCPErrorCode(t, mcpCallExpectError(t, e, ownerSession, "track_update_sprint", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "sprint": waiting.Ref, "status": "active",
	}), "conflict")

	enabled := decodeMCPField[model.Project](t, mcpCall(t, e, ownerSession, "track_update_project_sprint_mode", modeArgs(true)), "project")
	if !enabled.SprintsEnabled {
		t.Fatal("track_update_project_sprint_mode(true) returned sprints_enabled = false")
	}
	started := decodeMCPField[model.Sprint](t, mcpCall(t, e, ownerSession, "track_update_sprint", map[string]any{
		"owner": e.ownerUsername, "key": e.projKey, "sprint": waiting.Ref, "status": "active",
	}), "sprint")
	if started.Status != model.SprintStatusActive {
		t.Fatalf("started sprint status = %s", started.Status)
	}
}

func TestUIProjectWithoutSprintsLandsOnAll(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	e.mustSetFixtureSprintsEnabled(t, false)
	allPath := e.projectPath() + "/all"

	for _, tc := range []struct{ path, want string }{
		{path: e.projectPath(), want: allPath},
		{path: e.projectPath() + "/sprint", want: allPath},
		{path: e.projectPath() + "/sprint?status=any", want: allPath + "?status=any"},
		{path: "/", want: allPath},
	} {
		res := e.uiDoNoRedirect(t, http.MethodGet, tc.path, e.authToken, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != tc.want {
			t.Fatalf("GET %s = %d Location %q, want 303 to %q", tc.path, res.StatusCode, res.Header.Get("Location"), tc.want)
		}
	}

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, e.projectPath()+"/sprint/panel?status=any", e.authToken, nil, map[string]string{"HX-Request": "true"})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("sprint panel code = %d body = %s", res.StatusCode, body)
	}
	if got := res.Header.Get("HX-Push-Url"); got != allPath+"?status=any" {
		t.Fatalf("sprint panel HX-Push-Url = %q, want %q", got, allPath+"?status=any")
	}

	page := e.uiGet(t, allPath, e.authToken)
	if strings.Contains(page, `href="`+e.projectPath()+`/sprint"`) || strings.Contains(page, "person-standing") {
		t.Fatal("project without sprints still links to the Sprint tab")
	}
	for _, want := range []string{`href="` + e.projectPath() + `/progress"`, `href="` + allPath + `"`, `href="` + e.projectPath() + `/sprints"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("All page missing %q", want)
		}
	}
	if strings.Contains(page, `href="`+e.projectPath()+`/planned"`) {
		t.Fatal("project without sprints still links to the Planned tab")
	}

	if err := e.store.FavoriteProject(e.ctx, e.adminID, e.projectID); err != nil {
		t.Fatalf("FavoriteProject: %v", err)
	}
	projects := e.uiGet(t, "/projects", e.authToken)
	if !strings.Contains(projects, `href="`+allPath+`"`) || strings.Contains(projects, `href="`+e.projectPath()+`/sprint"`) {
		t.Fatal("project list and favorites should open the All view for a project without sprints")
	}

	about := e.uiGet(t, e.projectPath()+"/about", e.authToken)
	if !strings.Contains(about, `data-project-sprint-mode="disabled"`) || !strings.Contains(about, "Issues are picked up one at a time.") {
		t.Fatal("About Access card does not show sprints as disabled")
	}
	e.mustSetFixtureSprintsEnabled(t, true)
	about = e.uiGet(t, e.projectPath()+"/about", e.authToken)
	if !strings.Contains(about, `data-project-sprint-mode="enabled"`) || !strings.Contains(about, "Work runs sprint by sprint.") {
		t.Fatal("About Access card does not show sprints as enabled")
	}
}

func TestUIProjectWithoutSprintsHidesTheSprintBoardFromOutsiders(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	e.mustSetFixtureSprintsEnabled(t, false)
	_, outsiderToken := e.mustUserToken(t, "sprint-mode-ui-outsider")

	res := e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/sprint", outsiderToken, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider sprint board code = %d Location %q, want 403 without a redirect", res.StatusCode, res.Header.Get("Location"))
	}
}

// Without sprint mode there is nothing to plan, so In progress takes Planned's
// place. Planned sprints are kept and come back when sprints are turned on.
func TestUIProjectWithoutSprintsSwapsPlannedForInProgress(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	planned := e.mustPlannedSprint(t, "Later")
	activatePath := e.projectPath() + "/sprints/" + planned.Ref + "/activate"
	plannedPath := e.projectPath() + "/planned"
	progressPath := e.projectPath() + "/progress"
	sprintPath := e.projectPath() + "/sprint"

	withSprints := e.uiGet(t, plannedPath, e.authToken)
	if !strings.Contains(withSprints, activatePath) || strings.Contains(withSprints, `href="`+progressPath+`"`) {
		t.Fatal("sprint mode should show Planned with Activate sprint and no In progress tab")
	}
	// With sprints on, the Sprint board shows the work In progress would.
	res := e.uiDoNoRedirect(t, http.MethodGet, progressPath+"?completed_within=14d", e.authToken, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != sprintPath+"?completed_within=14d" {
		t.Fatalf("GET In progress in sprint mode = %d Location %q, want 303 to the Sprint board", res.StatusCode, res.Header.Get("Location"))
	}
	// The window parameter rides along and the board ignores it.
	e.uiGet(t, sprintPath+"?completed_within=14d", e.authToken)

	e.mustSetFixtureSprintsEnabled(t, false)
	res = e.uiDoNoRedirect(t, http.MethodGet, plannedPath+"?completed_within=30d", e.authToken, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != progressPath+"?completed_within=30d" {
		t.Fatalf("GET Planned without sprints = %d Location %q, want 303 to In progress", res.StatusCode, res.Header.Get("Location"))
	}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodGet, plannedPath+"/panel", e.authToken, nil, map[string]string{"HX-Request": "true"})
	body := readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Push-Url") != progressPath || !strings.Contains(body, "data-project-progress") {
		t.Fatalf("Planned panel without sprints = %d HX-Push-Url %q, want In progress: %s", res.StatusCode, res.Header.Get("HX-Push-Url"), body)
	}
	if strings.Contains(body, planned.Name) || strings.Contains(body, activatePath) {
		t.Fatal("In progress shows planned sprints")
	}
	if status := e.mustSprintStatus(t, planned.ID); status != model.SprintStatusPlanned {
		t.Fatalf("turning sprints off left the planned sprint %s", status)
	}

	// A stale Activate form lands on In progress and says why nothing started.
	_, memberToken := e.mustProjectMemberToken(t, "sprint-mode-planner")
	for _, tc := range []struct {
		token      string
		wantEnable bool
	}{
		{token: e.authToken, wantEnable: true},
		{token: memberToken, wantEnable: false},
	} {
		res := e.uiDoNoRedirect(t, http.MethodPost, activatePath, tc.token, strings.NewReader(""))
		body := readBody(t, res)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "data-progress-notice") || !strings.Contains(body, "Sprints are disabled for this project. Enable sprints to start one.") {
			t.Fatalf("activate while disabled code = %d body = %s", res.StatusCode, body)
		}
		if got := strings.Contains(body, ">Enable sprints</a>"); got != tc.wantEnable {
			t.Fatalf("Enable sprints link shown = %t, want %t", got, tc.wantEnable)
		}
	}
	if status := e.mustSprintStatus(t, planned.ID); status != model.SprintStatusPlanned {
		t.Fatalf("activate while disabled left sprint %s", status)
	}
	if page := e.uiGet(t, progressPath, e.authToken); strings.Contains(page, "data-progress-notice") {
		t.Fatal("In progress shows the activate notice without a failed activation")
	}

	e.mustSetFixtureSprintsEnabled(t, true)
	if page := e.uiGet(t, plannedPath, e.authToken); !strings.Contains(page, planned.Name) || !strings.Contains(page, activatePath) {
		t.Fatal("planned sprint did not come back when sprints were turned on again")
	}
}

func TestUIProjectSprintModeToggle(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, memberToken := e.mustProjectMemberToken(t, "sprint-mode-ui-member")
	running := e.mustPlannedSprint(t, "Running")
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, running.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("start sprint: %v", err)
	}

	page := e.uiGet(t, e.projectPath()+"/members", e.authToken)
	for _, want := range []string{
		`action="` + e.sprintModePath() + `"`,
		`data-project-sprint-mode="enabled"`,
		`name="sprints_enabled" checked disabled`,
		"Complete the active sprint to disable sprints.",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("members page with an active sprint missing %q", want)
		}
	}

	post := func(token string, body string) (int, string) {
		t.Helper()
		res := e.uiDoNoRedirect(t, http.MethodPost, e.sprintModePath(), token, strings.NewReader(body))
		out := readBody(t, res)
		res.Body.Close()
		return res.StatusCode, out
	}

	// A form rendered before the sprint started can still arrive; it is refused.
	code, body := post(e.authToken, "")
	if code != http.StatusOK || !strings.Contains(body, "Sprints were not disabled because a sprint is active.") {
		t.Fatalf("disable with active sprint code = %d body = %s", code, body)
	}
	if !e.mustFixtureSprintsEnabled(t) {
		t.Fatal("UI disable with an active sprint turned sprints off")
	}
	if code, _ := post(memberToken, ""); code != http.StatusForbidden {
		t.Fatalf("write member sprint mode code = %d, want 403", code)
	}
	if code, _ := post(e.authToken, "%zz"); code != http.StatusBadRequest {
		t.Fatalf("malformed sprint mode form code = %d, want 400", code)
	}
	if _, err := e.store.CompleteSprint(e.ctx, running.ID); err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}

	page = e.uiGet(t, e.projectPath()+"/members", e.authToken)
	if strings.Contains(page, "Complete the active sprint to disable sprints.") || strings.Contains(page, `name="sprints_enabled" checked disabled`) {
		t.Fatal("members page still locks sprint mode after the sprint completed")
	}

	code, body = post(e.authToken, "")
	if code != http.StatusOK || !strings.Contains(body, `data-project-sprint-mode="disabled"`) {
		t.Fatalf("disable code = %d body = %s", code, body)
	}
	if e.mustFixtureSprintsEnabled(t) {
		t.Fatal("UI disable left sprints on")
	}
	code, body = post(e.authToken, url.Values{"sprints_enabled": {"on"}}.Encode())
	if code != http.StatusOK || !strings.Contains(body, `data-project-sprint-mode="enabled"`) {
		t.Fatalf("enable code = %d body = %s", code, body)
	}
	if !e.mustFixtureSprintsEnabled(t) {
		t.Fatal("UI enable left sprints off")
	}

	changelog := e.uiGet(t, e.projectPath()+"/changelog", e.authToken)
	for _, want := range []string{"Disabled sprints for project " + e.projKey, "Enabled sprints for project " + e.projKey} {
		if !strings.Contains(changelog, want) {
			t.Fatalf("changelog page missing %q", want)
		}
	}
}
