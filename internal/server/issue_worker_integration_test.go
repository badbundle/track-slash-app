package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func issueWorkerIs(issue model.Issue, want model.IssueWorker) bool {
	return issue.Worker != nil && *issue.Worker == want
}

func TestIssueWorkerREST(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	code, body := e.do(t, http.MethodPost, e.projectIssuesPath(), map[string]any{"title": "for an agent", "worker": "agent"})
	forAgent := decode[model.Issue](t, body)
	if code != http.StatusCreated || !issueWorkerIs(forAgent, model.WorkerAgent) {
		t.Fatalf("create agent code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPost, e.projectIssuesPath(), map[string]any{"title": "unmarked"})
	unmarked := decode[model.Issue](t, body)
	if code != http.StatusCreated || unmarked.Worker != nil || !strings.Contains(string(body), `"worker":null`) {
		t.Fatalf("create unmarked code = %d body = %s", code, body)
	}
	if code, body := e.do(t, http.MethodPost, e.projectIssuesPath(), map[string]any{"title": "robot", "worker": "robot"}); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid worker") {
		t.Fatalf("create invalid worker code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPost, e.issueSubIssuesPath(forAgent), map[string]any{"title": "sub for a human", "worker": "human"})
	if sub := decode[model.Issue](t, body); code != http.StatusCreated || !issueWorkerIs(sub, model.WorkerHuman) {
		t.Fatalf("create sub-issue code = %d body = %s", code, body)
	}
	if code, body := e.do(t, http.MethodPost, e.issueSubIssuesPath(forAgent), map[string]any{"title": "robot", "worker": "robot"}); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid worker") {
		t.Fatalf("create sub-issue invalid worker code = %d body = %s", code, body)
	}

	// The agent hands its issue to a person.
	code, body = e.do(t, http.MethodPatch, e.issuePath(forAgent), map[string]any{"worker": "human"})
	if updated := decode[model.Issue](t, body); code != http.StatusOK || !issueWorkerIs(updated, model.WorkerHuman) {
		t.Fatalf("update human code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.issuePath(forAgent), nil)
	if got := decode[model.Issue](t, body); code != http.StatusOK || !issueWorkerIs(got, model.WorkerHuman) {
		t.Fatalf("get code = %d body = %s", code, body)
	}
	if code, body := e.do(t, http.MethodPatch, e.issuePath(forAgent), map[string]any{"worker": "robot"}); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid worker") {
		t.Fatalf("update invalid worker code = %d body = %s", code, body)
	}

	// Lists filter by worker.
	listIDs := func(query string) (int, []uuid.UUID, []byte) {
		t.Helper()
		code, body := e.do(t, http.MethodGet, e.projectIssuesPath()+"?"+query, nil)
		if code != http.StatusOK {
			return code, nil, body
		}
		page := decode[struct {
			Items []model.Issue `json:"items"`
		}](t, body)
		ids := make([]uuid.UUID, 0, len(page.Items))
		for _, issue := range page.Items {
			ids = append(ids, issue.ID)
		}
		return code, ids, body
	}
	for query, want := range map[string][]uuid.UUID{
		"worker=human":             {forAgent.ID},
		"worker=agent":             {},
		"worker=none":              {unmarked.ID},
		"worker=agent&worker=none": {unmarked.ID},
		"worker=human&worker=none": {forAgent.ID, unmarked.ID},
		"worker=":                  {forAgent.ID, unmarked.ID},
	} {
		code, ids, body := listIDs(query)
		if code != http.StatusOK || len(ids) != len(want) {
			t.Fatalf("list %s code = %d body = %s", query, code, body)
		}
		for i := range want {
			if ids[i] != want[i] {
				t.Fatalf("list %s ids = %v, want %v", query, ids, want)
			}
		}
	}
	if code, _, body := listIDs("worker=robot"); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid worker") {
		t.Fatalf("list invalid worker code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(forAgent), map[string]any{"clear_worker": true})
	if cleared := decode[model.Issue](t, body); code != http.StatusOK || cleared.Worker != nil {
		t.Fatalf("clear worker code = %d body = %s", code, body)
	}

	// Choosing who works on an issue is for members: someone filing into a
	// public project may not.
	if _, err := e.store.UpdateProjectAccessMode(e.ctx, e.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "worker-outsider")
	if code, body := e.doWithToken(t, outsiderToken, http.MethodPost, e.projectIssuesPath(), map[string]any{"title": "outsider", "worker": "agent"}); code != http.StatusForbidden {
		t.Fatalf("outsider create with worker code = %d body = %s", code, body)
	}
	if code, body := e.doWithToken(t, outsiderToken, http.MethodPost, e.projectIssuesPath(), map[string]any{"title": "outsider"}); code != http.StatusCreated {
		t.Fatalf("outsider create code = %d body = %s", code, body)
	}
}

func TestIssueWorkerMCP(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	session := mcpConnect(t, e, e.authToken)
	project := map[string]any{"owner": e.ownerUsername, "key": e.projKey}
	with := func(base map[string]any, extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	forAgent := decodeMCPField[model.Issue](t, mcpCall(t, e, session, "track_create_issue", with(project, map[string]any{"title": "for an agent", "worker": "agent"})), "issue")
	if !issueWorkerIs(forAgent, model.WorkerAgent) {
		t.Fatalf("create worker = %v", forAgent.Worker)
	}
	unmarked := decodeMCPField[model.Issue](t, mcpCall(t, e, session, "track_create_issue", with(project, map[string]any{"title": "unmarked"})), "issue")
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_create_issue", with(project, map[string]any{"title": "robot", "worker": "robot"})), "validation_error")

	parent := map[string]any{"owner": e.ownerUsername, "issue": forAgent.Identifier}
	sub := decodeMCPField[model.Issue](t, mcpCall(t, e, session, "track_create_sub_issue", with(parent, map[string]any{"title": "sub for a human", "worker": "human"})), "issue")
	if !issueWorkerIs(sub, model.WorkerHuman) {
		t.Fatalf("sub-issue worker = %v", sub.Worker)
	}
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_create_sub_issue", with(parent, map[string]any{"title": "robot", "worker": "robot"})), "validation_error")

	updated := decodeMCPField[model.Issue](t, mcpCall(t, e, session, "track_update_issue", with(parent, map[string]any{"worker": "human"})), "issue")
	if !issueWorkerIs(updated, model.WorkerHuman) {
		t.Fatalf("update worker = %v", updated.Worker)
	}
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_update_issue", with(parent, map[string]any{"worker": "robot"})), "validation_error")

	for name, tc := range map[string]struct {
		workers []string
		want    []uuid.UUID
	}{
		"human":         {workers: []string{"human"}, want: []uuid.UUID{forAgent.ID}},
		"none":          {workers: []string{"none"}, want: []uuid.UUID{unmarked.ID}},
		"agent or none": {workers: []string{"agent", "none"}, want: []uuid.UUID{unmarked.ID}},
	} {
		items := decodeMCPField[[]model.Issue](t, mcpCall(t, e, session, "track_list_issues", with(project, map[string]any{"workers": tc.workers})), "items")
		if len(items) != len(tc.want) {
			t.Fatalf("%s: items = %+v, want %v", name, items, tc.want)
		}
		for i := range tc.want {
			if items[i].ID != tc.want[i] {
				t.Fatalf("%s: items = %+v, want %v", name, items, tc.want)
			}
		}
	}
	requireMCPErrorCode(t, mcpCallExpectError(t, e, session, "track_list_issues", with(project, map[string]any{"workers": []string{"robot"}})), "validation_error")

	cleared := decodeMCPField[model.Issue](t, mcpCall(t, e, session, "track_update_issue", with(parent, map[string]any{"clear_worker": true})), "issue")
	if cleared.Worker != nil {
		t.Fatalf("clear worker = %v", *cleared.Worker)
	}

	if _, err := e.store.UpdateProjectAccessMode(e.ctx, e.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "mcp-worker-outsider")
	outsider := mcpConnect(t, e, outsiderToken)
	requireMCPErrorCode(t, mcpCallExpectError(t, e, outsider, "track_create_issue", with(project, map[string]any{"title": "outsider", "worker": "agent"})), "forbidden")
	mcpCall(t, e, outsider, "track_create_issue", with(project, map[string]any{"title": "outsider"}))
}

func TestUIIssueWorker(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-worker")
	issue := e.mustCreateIssue(t, "worker target issue")

	panel := e.uiGet(t, e.issuePath(issue)+"/panel", token)
	for _, want := range []string{
		`>Worker</dt>`,
		`aria-label="Edit worker" hx-get="` + e.issuePath(issue) + `/worker/edit"`,
	} {
		if !strings.Contains(panel, want) {
			t.Fatalf("unmarked panel missing %q: %s", want, panel)
		}
	}
	if strings.Contains(panel, "data-issue-worker-") {
		t.Fatalf("unmarked panel rendered a worker badge: %s", panel)
	}

	edit := e.uiGet(t, e.issuePath(issue)+"/worker/edit", token)
	for _, want := range []string{
		`role="listbox" aria-label="Issue worker"`,
		`method="post" action="` + e.issuePath(issue) + `/worker"`,
		`aria-label="Change worker: None"`,
		`name="worker" value="" role="option" aria-selected="true"`,
		`name="worker" value="agent" role="option" aria-selected="false"`,
		`name="worker" value="human" role="option" aria-selected="false"`,
		`data-lucide="circle-dashed"`,
		`data-lucide="bot"`,
		`data-lucide="user-round"`,
		`hx-get="` + e.issuePath(issue) + `/panel"`,
	} {
		if !strings.Contains(edit, want) {
			t.Fatalf("worker edit missing %q: %s", want, edit)
		}
	}

	post := func(form url.Values) (int, string) {
		t.Helper()
		res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/worker", token, strings.NewReader(form.Encode()))
		defer res.Body.Close()
		return res.StatusCode, readBody(t, res)
	}
	code, body := post(url.Values{"worker": {"human"}})
	for _, want := range []string{
		`data-issue-worker-badge="human" data-tooltip="Needs a human"`,
		`data-issue-worker-label="human"`,
		"Needs a human</span>",
	} {
		if code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("set human code = %d missing %q: %s", code, want, body)
		}
	}
	if got, err := e.store.GetIssue(e.ctx, issue.ID); err != nil || !issueWorkerIs(got, model.WorkerHuman) {
		t.Fatalf("GetIssue after human = %+v, %v", got.Worker, err)
	}
	edit = e.uiGet(t, e.issuePath(issue)+"/worker/edit", token)
	if !strings.Contains(edit, `name="worker" value="human" role="option" aria-selected="true"`) || !strings.Contains(edit, "border-amber-300 bg-amber-50 text-amber-800") {
		t.Fatalf("worker edit does not show human as current: %s", edit)
	}

	// Rows and the board card show the mark too.
	for _, path := range []string{e.projectPath() + "/all"} {
		if list := e.uiGet(t, path, token); !strings.Contains(list, `data-issue-worker-badge="human"`) {
			t.Fatalf("%s missing worker badge: %s", path, list)
		}
	}
	start := time.Now().UTC()
	end := start.AddDate(0, 0, 7)
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "Worker sprint", StartDate: &start, EndDate: &end})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sprint.ID}); err != nil {
		t.Fatalf("UpdateIssue sprint: %v", err)
	}
	if board := e.uiGet(t, e.projectPath()+"/sprint", token); !strings.Contains(board, `data-issue-worker-badge="human"`) {
		t.Fatalf("sprint board missing worker badge: %s", board)
	}

	code, body = post(url.Values{"worker": {"agent"}})
	if code != http.StatusOK || !strings.Contains(body, `data-issue-worker-badge="agent" data-tooltip="For an agent"`) {
		t.Fatalf("set agent code = %d body = %s", code, body)
	}
	code, body = post(url.Values{"worker": {"robot"}})
	if code != http.StatusBadRequest || !strings.Contains(body, "invalid worker") {
		t.Fatalf("invalid worker code = %d body = %s", code, body)
	}
	code, body = post(url.Values{"worker": {""}})
	if code != http.StatusOK || strings.Contains(body, "data-issue-worker-") {
		t.Fatalf("clear worker code = %d body = %s", code, body)
	}
	if got, err := e.store.GetIssue(e.ctx, issue.ID); err != nil || got.Worker != nil {
		t.Fatalf("GetIssue after clear = %+v, %v", got.Worker, err)
	}
	res := e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/worker", token, strings.NewReader("%zz"))
	defer res.Body.Close()
	if body := readBody(t, res); res.StatusCode != http.StatusBadRequest || !strings.Contains(body, "unable to read form") {
		t.Fatalf("unreadable form code = %d body = %s", res.StatusCode, body)
	}

	// A read-only member sees the mark but cannot change it.
	readonly, readonlyToken := e.mustUserToken(t, "ui-worker-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole: %v", err)
	}
	if readonlyPanel := e.uiGet(t, e.issuePath(issue)+"/panel", readonlyToken); strings.Contains(readonlyPanel, `aria-label="Edit worker"`) {
		t.Fatalf("readonly panel offered worker edit: %s", readonlyPanel)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, e.issuePath(issue)+"/worker", readonlyToken, strings.NewReader(url.Values{"worker": {"human"}}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("readonly update code = %d, want 403", res.StatusCode)
	}
}

func TestUINewIssueWorker(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-new-worker")
	submit := func(token string, worker string) (int, string) {
		t.Helper()
		form := url.Values{"project_id": {e.projectID.String()}, "title": {"new issue for " + worker}, "worker": {worker}}
		res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/issues", token, strings.NewReader(form.Encode()), map[string]string{"HX-Request": "true"})
		defer res.Body.Close()
		return res.StatusCode, readBody(t, res)
	}

	if form := e.uiGet(t, e.projectPath()+"/issues/new", token); !strings.Contains(form, `type="radio" name="worker" value="human" >`) {
		t.Fatalf("new issue form missing worker picker: %s", form)
	}
	code, body := submit(token, "human")
	if code != http.StatusOK || !strings.Contains(body, `data-issue-worker-label="human"`) {
		t.Fatalf("create with worker code = %d body = %s", code, body)
	}
	issues, _, err := e.store.ListIssues(e.ctx, store.ListIssuesParams{ProjectID: e.projectID, Workers: []model.IssueWorker{model.WorkerHuman}, Limit: 10})
	if err != nil || len(issues) != 1 || issues[0].Title != "new issue for human" {
		t.Fatalf("ListIssues human = %+v, %v", issues, err)
	}
	code, body = submit(token, "robot")
	if code != http.StatusOK || !strings.Contains(body, "Invalid worker.") || !strings.Contains(body, `value="new issue for robot"`) {
		t.Fatalf("create with invalid worker code = %d body = %s", code, body)
	}

	if _, err := e.store.UpdateProjectAccessMode(e.ctx, e.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "ui-new-worker-outsider")
	if form := e.uiGet(t, e.projectPath()+"/issues/new", outsiderToken); strings.Contains(form, `name="worker"`) {
		t.Fatalf("public new issue form offered the worker picker: %s", form)
	}
	code, body = submit(outsiderToken, "agent")
	if code != http.StatusOK || !strings.Contains(body, "Public issue submissions cannot choose who works on the issue.") {
		t.Fatalf("outsider create with worker code = %d body = %s", code, body)
	}
}
