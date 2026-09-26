package server_test

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// insightsDoneIssue creates a Done issue that passed through In progress and
// backdates its history an hour, so the Go clock that dates the insight
// window never lands before the Postgres-stamped rows.
func insightsDoneIssue(t *testing.T, e *httpEnv, title string) model.Issue {
	t.Helper()
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: title})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	for _, status := range []model.Status{model.StatusInProgress, model.StatusDone} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{Status: &status}); err != nil {
			t.Fatalf("UpdateIssue %s: %v", status, err)
		}
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE issues SET created_at = now() - interval '3 hours' WHERE id = $1`, issue.ID); err != nil {
		t.Fatalf("backdate issue: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `
		UPDATE project_changelog_entries e
		SET created_at = now() - interval '2 hours' + ranked.rn * interval '10 minutes'
		FROM (SELECT id, row_number() OVER (ORDER BY created_at, id) AS rn FROM project_changelog_entries WHERE issue_id = $1) ranked
		WHERE e.id = ranked.id
	`, issue.ID); err != nil {
		t.Fatalf("backdate changelog: %v", err)
	}
	return issue
}

func insightsActiveSprint(t *testing.T, e *httpEnv, name string) model.Sprint {
	t.Helper()
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: name})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}
	if _, err := e.pool.Exec(e.ctx, `
		UPDATE project_changelog_entries SET created_at = now() - interval '1 hour'
		WHERE entity = 'sprint' AND entity_id = $1 AND op = 'update'
	`, sprint.ID); err != nil {
		t.Fatalf("backdate activation: %v", err)
	}
	return sprint
}

func TestProjectInsightsAPI(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, memberToken := e.mustProjectMemberToken(t, "insights-api-member")
	insightsDoneIssue(t, e, "insight api done")
	path := e.projectPath() + "/insights"

	code, body := e.doWithToken(t, memberToken, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("insights code = %d body = %s", code, body)
	}
	insights := decode[model.ProjectInsights](t, body)
	if insights.ProjectID != e.projectID || insights.Range != model.InsightRangeThirtyDays || insights.Bucket != model.InsightBucketDay || len(insights.Flow) != 30 {
		t.Fatalf("default insights = range %s bucket %s flow %d", insights.Range, insights.Bucket, len(insights.Flow))
	}
	if last := insights.Flow[len(insights.Flow)-1]; last.Completed != 1 || last.Scope != 1 {
		t.Fatalf("latest flow point = %+v", last)
	}
	if len(insights.CycleTime.Issues) != 1 || !insights.Sprints.Enabled || insights.Sprints.Count != 0 {
		t.Fatalf("insights cycle %+v sprints %+v", insights.CycleTime, insights.Sprints)
	}

	sprint := insightsActiveSprint(t, e, "Insight sprint")
	code, body = e.do(t, http.MethodGet, path+"?range=90D&sprint="+sprint.Ref, nil)
	if code != http.StatusOK {
		t.Fatalf("sprint insights code = %d body = %s", code, body)
	}
	insights = decode[model.ProjectInsights](t, body)
	if insights.Range != model.InsightRangeNinetyDays || insights.Bucket != model.InsightBucketWeek || !insights.Sprints.Enabled ||
		insights.Sprints.Burnup == nil || insights.Sprints.Burnup.Ref != sprint.Ref || len(insights.Sprints.Options) != 1 {
		t.Fatalf("sprint insights = range %s sprints %+v", insights.Range, insights.Sprints)
	}

	for _, tt := range []struct {
		name  string
		token string
		path  string
		want  int
	}{
		{name: "invalid range", token: e.authToken, path: path + "?range=7d", want: http.StatusBadRequest},
		{name: "invalid sprint ref", token: e.authToken, path: path + "?sprint=bogus", want: http.StatusBadRequest},
		{name: "missing sprint", token: e.authToken, path: path + "?sprint=sprint-99", want: http.StatusNotFound},
		{name: "bad owner", token: e.authToken, path: "/bad!/projects/" + e.projKey + "/insights", want: http.StatusBadRequest},
		{name: "missing project", token: e.authToken, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/insights", want: http.StatusNotFound},
		{name: "anonymous private", path: path, want: http.StatusUnauthorized},
	} {
		if code, body := e.doWithToken(t, tt.token, http.MethodGet, tt.path, nil); code != tt.want {
			t.Fatalf("%s code = %d body = %s, want %d", tt.name, code, body, tt.want)
		}
	}
	_, deniedToken := e.mustUserToken(t, "insights-api-denied")
	if code, _ := e.doWithToken(t, deniedToken, http.MethodGet, path, nil); code != http.StatusForbidden {
		t.Fatalf("non-member insights code = %d", code)
	}

	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings: %v", err)
	}
	if code, body := e.doUnauth(t, http.MethodGet, path+"?range=2w", nil); code != http.StatusOK {
		t.Fatalf("anonymous public insights code = %d body = %s", code, body)
	}
	if code, _ := e.doWithToken(t, deniedToken, http.MethodGet, path, nil); code != http.StatusOK {
		t.Fatalf("signed-in public insights code = %d", code)
	}

	// Sprint charts follow sprint mode, but completed sprint history is still
	// returned so API clients can read it.
	if _, err := e.pool.Exec(e.ctx, `UPDATE sprints SET status = 'completed', completed_at = now() - interval '30 minutes' WHERE id = $1`, sprint.ID); err != nil {
		t.Fatalf("complete sprint: %v", err)
	}
	if _, err := e.store.SetProjectSprintsEnabled(e.ctx, e.projectID, false); err != nil {
		t.Fatalf("SetProjectSprintsEnabled: %v", err)
	}
	code, body = e.do(t, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("sprints-off insights code = %d body = %s", code, body)
	}
	insights = decode[model.ProjectInsights](t, body)
	if insights.Sprints.Enabled || insights.Sprints.Count != 1 || len(insights.Sprints.Velocity) != 1 {
		t.Fatalf("sprints-off insights sprints = %+v", insights.Sprints)
	}
}

func TestMCPGetProjectInsights(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	insightsDoneIssue(t, e, "insight mcp done")
	session := mcpConnect(t, e, e.authToken)
	args := func(extra map[string]any) map[string]any {
		out := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	out := mcpCall(t, e, session, "track_get_project_insights", args(map[string]any{"range": "90d"}))
	insights := decodeMCPField[model.ProjectInsights](t, out, "insights")
	if insights.Range != model.InsightRangeNinetyDays || insights.Bucket != model.InsightBucketWeek || !insights.Sprints.Enabled {
		t.Fatalf("MCP insights = range %s bucket %s sprints %+v", insights.Range, insights.Bucket, insights.Sprints)
	}
	if last := insights.Flow[len(insights.Flow)-1]; last.Completed != 1 {
		t.Fatalf("MCP latest flow point = %+v", last)
	}

	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_get_project_insights", args(map[string]any{"range": "7d"})), "validation_error")
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_get_project_insights", args(map[string]any{"sprint": "sprint-9"})), "not_found")
	_, outsiderToken := e.mustUserToken(t, "insights-mcp-outsider")
	outsider := mcpConnect(t, e, outsiderToken)
	requireMCPErrorCode(t, mcpCallExpectError(t, e, outsider, "track_get_project_insights", args(nil)), "forbidden")
}

func TestUIProjectInsightsPage(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	readonly, readonlyToken := e.mustUserToken(t, "ui-insights-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	done := insightsDoneIssue(t, e, "insight ui done")
	path := e.projectPath() + "/insights"
	if _, err := e.store.SetProjectSprintsEnabled(e.ctx, e.projectID, false); err != nil {
		t.Fatalf("SetProjectSprintsEnabled off: %v", err)
	}

	body := e.uiGet(t, path, readonlyToken)
	for _, want := range []string{
		`id="insight-burnup"`, `id="insight-flow"`, `id="insight-throughput"`, `id="insight-cycle-time"`,
		"Sprints are off for this project, so sprint burn-up and velocity are hidden.",
		`aria-label="Date range"`, `aria-current="true" class="whitespace-nowrap`, ">30 days</a>",
		`href="` + path + `?range=90d"`, `hx-get="` + path + `/panel?range=90d"`,
		`href="` + path + `" hx-get="` + path + `/panel"`,
		`aria-current="page"`, ">Insights<",
		`href="/` + e.ownerUsername + `/issues/` + done.Identifier + `"`, "data-insight-point",
		`data-insight-toggle="scope"`, `data-insight-toggle="in_progress"`, "Data table",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("insights page missing %q: %s", want, body)
		}
	}
	for _, notWant := range []string{`id="insight-velocity"`, `id="insight-sprint-burnup"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("insights page without sprints rendered %q", notWant)
		}
	}
	chart := insightChartJSON(t, body, "insight-burnup")
	if chart.Kind != "line" || len(chart.Labels) != 30 || len(chart.Series) != 3 || chart.Series[2].Key != "completed" {
		t.Fatalf("burn-up chart data = %+v", chart)
	}
	if last := chart.Series[2].Values[len(chart.Series[2].Values)-1]; last == nil || *last != 1 {
		t.Fatalf("burn-up completed latest = %v", last)
	}

	if _, err := e.store.SetProjectSprintsEnabled(e.ctx, e.projectID, true); err != nil {
		t.Fatalf("SetProjectSprintsEnabled on: %v", err)
	}
	sprint := insightsActiveSprint(t, e, "UI insight sprint")
	body = e.uiGet(t, path+"?range=90d", e.authToken)
	for _, want := range []string{`id="insight-velocity"`, `id="insight-sprint-burnup"`, sprint.Ref + " · UI insight sprint", `href="` + path + `?range=90d&amp;sprint=` + sprint.Ref + `"`, "No sprints were completed in this range."} {
		if !strings.Contains(body, want) {
			t.Fatalf("insights page with sprints missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Sprints are off for this project") {
		t.Fatalf("insights page with sprints still shows the sprint note")
	}

	panel := e.uiGet(t, path+"/panel?range=2w&sprint="+sprint.Ref, e.authToken)
	if !strings.Contains(panel, "data-project-insights") || !strings.Contains(panel, `id="insight-sprint-burnup"`) || strings.Contains(panel, "<html") {
		t.Fatalf("insights panel = %s", panel)
	}

	for _, tt := range []struct {
		name string
		path string
		want int
	}{
		{name: "invalid range", path: path + "?range=forever", want: http.StatusBadRequest},
		{name: "invalid sprint", path: path + "?sprint=later", want: http.StatusBadRequest},
		{name: "missing sprint", path: path + "?sprint=sprint-42", want: http.StatusNotFound},
	} {
		res := e.uiDoNoRedirect(t, http.MethodGet, tt.path, e.authToken, nil)
		res.Body.Close()
		if res.StatusCode != tt.want {
			t.Fatalf("%s code = %d, want %d", tt.name, res.StatusCode, tt.want)
		}
	}
	_, outsiderToken := e.mustUserToken(t, "ui-insights-outsider")
	res := e.uiDoNoRedirect(t, http.MethodGet, path, outsiderToken, nil)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("private insights for outsider code = %d", res.StatusCode)
	}

	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings: %v", err)
	}
	res = e.uiDoNoRedirect(t, http.MethodGet, path, "", nil)
	defer res.Body.Close()
	publicBody := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(publicBody, `id="insight-cycle-time"`) || !strings.Contains(publicBody, `id="insight-velocity"`) {
		t.Fatalf("anonymous public insights code = %d body = %s", res.StatusCode, publicBody)
	}
}

type insightChartPayload struct {
	Kind   string   `json:"kind"`
	Labels []string `json:"labels"`
	Series []struct {
		Key    string     `json:"key"`
		Values []*float64 `json:"values"`
	} `json:"series"`
}

func insightChartJSON(t *testing.T, body, id string) insightChartPayload {
	t.Helper()
	marker := `id="` + id + `" aria-labelledby="` + id + `-title" data-insight-chart="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("chart %s missing", id)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	var payload insightChartPayload
	if err := json.Unmarshal([]byte(html.UnescapeString(rest[:end])), &payload); err != nil {
		t.Fatalf("chart %s data: %v", id, err)
	}
	return payload
}

// A signed-out visitor to a private project is sent to sign in, like every
// other project view.
func TestUIProjectInsightsRedirectsAnonymousPrivate(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	res := e.uiDoNoRedirect(t, http.MethodGet, e.projectPath()+"/insights", "", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Fatalf("anonymous private insights code = %d location = %q", res.StatusCode, res.Header.Get("Location"))
	}
}
