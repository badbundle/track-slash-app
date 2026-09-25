package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

type httpEnv struct {
	ctx           context.Context
	ts            *httptest.Server
	pool          *pgxpool.Pool
	store         *store.Store
	projectID     uuid.UUID
	projKey       string
	ownerUsername string
	adminID       uuid.UUID
	authToken     string
}

func datePtr(t time.Time) *time.Time {
	return &t
}

func sprintTestDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func newHTTPEnv(t *testing.T) *httpEnv {
	return newHTTPEnvWithOptions(t, server.Options{})
}

func newHTTPEnvWithOptions(t *testing.T, options server.Options) *httpEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	db := testutil.NewMigratedDatabase(t)
	s := store.New(db.Pool)
	// Hub is nil — none of these handlers need realtime fanout; /api/v1/ws route
	// is just skipped when hub == nil.
	srv := server.NewWithOptions(s, nil, options)
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	key := uniqueProjectKey(t)
	admin, err := s.CreateOrUpdateAdminUser(ctx, "admin-"+key+"@example.com", "Admin")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	proj, err := s.CreateProjectForUser(ctx, admin.ID, key, "http-test", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	enableFixtureSprintMode(t, ctx, db.Pool, proj.ID)
	token, err := s.CreateAuthToken(ctx, store.CreateAuthTokenParams{
		UserID: admin.ID,
		Kind:   model.AuthTokenKindAPI,
		Name:   "test",
	})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}

	return &httpEnv{
		ctx: ctx, ts: ts, pool: db.Pool, store: s, projectID: proj.ID, projKey: key, ownerUsername: admin.Username,
		adminID: admin.ID, authToken: token.RawToken,
	}
}

// enableFixtureSprintMode puts a fixture project in sprint mode, as migration
// 0044 did for every project that already used sprints. It writes the column
// directly so the fixture carries no changelog entry of its own. New projects
// start with sprints disabled; sprint_mode_integration_test.go covers that.
func enableFixtureSprintMode(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE projects SET sprints_enabled = true WHERE id = $1`, projectID); err != nil {
		t.Fatalf("enable fixture sprint mode: %v", err)
	}
}

func (e *httpEnv) projectPath() string {
	return "/" + e.ownerUsername + "/projects/" + e.projKey
}

func (e *httpEnv) projectIssuesPath() string {
	return e.projectPath() + "/issues"
}

func (e *httpEnv) projectSprintsPath() string {
	return e.projectPath() + "/sprints"
}

func (e *httpEnv) issuePath(iss model.Issue) string {
	return "/" + iss.OwnerUsername + "/issues/" + iss.Identifier
}

func (e *httpEnv) issueCommentsPath(iss model.Issue) string {
	return e.issuePath(iss) + "/comments"
}

func (e *httpEnv) issueLinksPath(iss model.Issue) string {
	return e.issuePath(iss) + "/links"
}

func (e *httpEnv) issueSubIssuesPath(iss model.Issue) string {
	return e.issuePath(iss) + "/sub-issues"
}

func (e *httpEnv) sprintPath(sp model.Sprint) string {
	return e.projectSprintsPath() + "/" + sp.Ref
}

func (e *httpEnv) projectLinkPath(link model.IssueLink) string {
	return e.projectPath() + "/links/" + link.Ref
}

func uniqueProjectKey(t *testing.T) string {
	t.Helper()
	n := time.Now().UnixNano()
	out := make([]byte, 9)
	for i := 8; i >= 0; i-- {
		out[i] = byte('0' + (n % 10))
		n /= 10
	}
	return "H" + string(out)
}

// do performs a request against the test server and returns status + body.
func (e *httpEnv) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	return e.doWithToken(t, e.authToken, method, path, body)
}

func (e *httpEnv) doUnauth(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	return e.doWithToken(t, "", method, path, body)
}

func (e *httpEnv) doWithToken(t *testing.T, token, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(e.ctx, method, e.ts.URL+apiPath(path), rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, out
}

func apiPath(path string) string {
	return "/api/v1" + path
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	return v
}

// ---------- createSprint ----------

func TestHTTPCreateSprintHappy(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, body := e.do(t, http.MethodPost,
		e.projectSprintsPath(),
		map[string]any{"name": "S1", "start_date": "2026-06-01", "end_date": "2026-06-14"},
	)
	if code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	sp := decode[model.Sprint](t, body)
	if sp.Name != "S1" || sp.Status != model.SprintStatusPlanned {
		t.Fatalf("bad sprint: %+v", sp)
	}
}

func TestHTTPCreateSprintWithoutDates(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, body := e.do(t, http.MethodPost,
		e.projectSprintsPath(),
		map[string]any{"name": "No dates"},
	)
	if code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", code, body)
	}
	sp := decode[model.Sprint](t, body)
	if sp.StartDate != nil || sp.EndDate != nil {
		t.Fatalf("dates = %v..%v, want nil..nil", sp.StartDate, sp.EndDate)
	}
}

func TestHTTPCreateSprintBadProjectID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost, "/bad!/projects/TRACK/sprints",
		map[string]any{"name": "S", "start_date": "2026-06-01", "end_date": "2026-06-14"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateSprintBadJSON(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	req, _ := http.NewRequestWithContext(e.ctx, http.MethodPost,
		e.ts.URL+apiPath(e.projectSprintsPath()),
		bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("code = %d", res.StatusCode)
	}
}

func TestHTTPCreateSprintBadDateFormat(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	cases := []map[string]any{
		{"start_date": "2026/06/01", "end_date": "2026-06-14"},
		{"start_date": "2026-06-01", "end_date": "tomorrow"},
	}
	for _, body := range cases {
		body["name"] = "x"
		code, _ := e.do(t, http.MethodPost,
			e.projectSprintsPath(), body)
		if code != http.StatusBadRequest {
			t.Fatalf("body=%v code=%d", body, code)
		}
	}
}

func TestHTTPCreateSprintEndBeforeStart(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost,
		e.projectSprintsPath(),
		map[string]any{"name": "x", "start_date": "2026-06-14", "end_date": "2026-06-01"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateSprintRejectsPartialDateRange(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for _, body := range []map[string]any{
		{"name": "start only", "start_date": "2026-06-01"},
		{"name": "end only", "end_date": "2026-06-14"},
	} {
		code, _ := e.do(t, http.MethodPost, e.projectSprintsPath(), body)
		if code != http.StatusBadRequest {
			t.Fatalf("body=%v code=%d", body, code)
		}
	}
}

func TestHTTPCreateSprintNameTooLong(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	code, _ := e.do(t, http.MethodPost,
		e.projectSprintsPath(),
		map[string]any{"name": string(long), "start_date": "2026-06-01", "end_date": "2026-06-14"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateSprintGoalTooLong(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	long := make([]byte, 2001)
	for i := range long {
		long[i] = 'g'
	}
	code, _ := e.do(t, http.MethodPost,
		e.projectSprintsPath(),
		map[string]any{"name": "x", "goal": string(long), "start_date": "2026-06-01", "end_date": "2026-06-14"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateSprintProjectNotFound(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost,
		"/"+e.ownerUsername+"/projects/"+uniqueProjectKey(t)+"/sprints",
		map[string]any{"name": "x", "start_date": "2026-06-01", "end_date": "2026-06-14"})
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

// ---------- listProjectSprints ----------

func TestHTTPListSprintsAndStatusFilter(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for i, name := range []string{"S1", "S2"} {
		e.do(t, http.MethodPost, e.projectSprintsPath(),
			map[string]any{
				"name":       name,
				"start_date": fmt.Sprintf("2026-06-%02d", 1+i*14),
				"end_date":   fmt.Sprintf("2026-06-%02d", 14+i*14),
			})
	}
	code, body := e.do(t, http.MethodGet, e.projectSprintsPath(), nil)
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	sprints := decodePage[model.Sprint](t, body).Items
	if len(sprints) != 2 {
		t.Fatalf("len = %d", len(sprints))
	}

	code, _ = e.do(t, http.MethodGet,
		e.projectSprintsPath()+"?status=completed", nil)
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}

	code, body = e.do(t, http.MethodGet,
		e.projectSprintsPath()+"?status=planned&limit=1", nil)
	if code != http.StatusOK {
		t.Fatalf("planned code = %d", code)
	}
	plannedPage := decodePage[model.Sprint](t, body)
	if len(plannedPage.Items) != 1 || plannedPage.NextCursor == nil {
		t.Fatalf("planned page = %+v", plannedPage)
	}
}

func TestHTTPListSprintsCompletedSortAndCursor(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	olderAt := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	middleAt := olderAt.Add(24 * time.Hour)
	newestAt := middleAt.Add(24 * time.Hour)
	older := createCompletedSprintAtFor(t, e, e.projectID, "older", sprintTestDate(2026, 9, 1), sprintTestDate(2026, 9, 14), &olderAt)
	middle := createCompletedSprintAtFor(t, e, e.projectID, "middle", sprintTestDate(2026, 8, 1), sprintTestDate(2026, 8, 14), &middleAt)
	newest := createCompletedSprintAtFor(t, e, e.projectID, "newest", sprintTestDate(2026, 6, 1), sprintTestDate(2026, 6, 14), &newestAt)

	path := e.projectSprintsPath() + "?status=completed&sort=completed&limit=2"
	code, body := e.do(t, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("page 1 code = %d body = %s", code, body)
	}
	page1 := decodePage[model.Sprint](t, body)
	if len(page1.Items) != 2 || page1.Items[0].ID != newest.ID || page1.Items[1].ID != middle.ID || page1.NextCursor == nil {
		t.Fatalf("page 1 = %+v", page1)
	}

	code, body = e.do(t, http.MethodGet, path+"&cursor="+url.QueryEscape(*page1.NextCursor), nil)
	if code != http.StatusOK {
		t.Fatalf("page 2 code = %d body = %s", code, body)
	}
	page2 := decodePage[model.Sprint](t, body)
	if len(page2.Items) != 1 || page2.Items[0].ID != older.ID || page2.NextCursor != nil {
		t.Fatalf("page 2 = %+v", page2)
	}
}

func TestHTTPListSprintsBadStatus(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet,
		e.projectSprintsPath()+"?status=banana", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListSprintsBadSort(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for _, query := range []string{"sort=banana", "sort=completed", "status=active&sort=completed"} {
		code, _ := e.do(t, http.MethodGet, e.projectSprintsPath()+"?"+query, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("query %q code = %d, want 400", query, code)
		}
	}
}

func TestHTTPListSprintsBadProjectID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, "/bad!/projects/TRACK/sprints", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListSprintHistoryIssues(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "API snapshot sprint"})
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	next, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{ProjectID: e.projectID, Name: "API next sprint"})
	if err != nil {
		t.Fatalf("CreateSprint next: %v", err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}
	todo := e.mustCreateIssue(t, "API unfinished snapshot issue")
	done := e.mustCreateIssue(t, "API done snapshot issue")
	for _, issue := range []model.Issue{todo, done} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sprint.ID}); err != nil {
			t.Fatalf("assign %s: %v", issue.Title, err)
		}
	}
	doneStatus := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, done.ID, store.UpdateIssueParams{Status: &doneStatus}); err != nil {
		t.Fatalf("complete issue: %v", err)
	}
	completed, err := e.store.CompleteSprint(e.ctx, sprint.ID)
	if err != nil {
		t.Fatalf("CompleteSprint: %v", err)
	}
	moved, err := e.store.GetIssue(e.ctx, todo.ID)
	if err != nil {
		t.Fatalf("GetIssue moved: %v", err)
	}
	if moved.SprintID == nil || *moved.SprintID != next.ID {
		t.Fatalf("unfinished issue sprint = %v, want %s", moved.SprintID, next.ID)
	}

	path := e.sprintPath(completed) + "/history/issues?limit=1"
	code, body := e.do(t, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("page 1 code = %d body = %s", code, body)
	}
	page1 := decodePage[model.Issue](t, body)
	if len(page1.Items) != 1 || page1.Items[0].ID != todo.ID || page1.NextCursor == nil {
		t.Fatalf("page 1 = %+v", page1)
	}
	code, body = e.do(t, http.MethodGet, path+"&cursor="+url.QueryEscape(*page1.NextCursor), nil)
	if code != http.StatusOK {
		t.Fatalf("page 2 code = %d body = %s", code, body)
	}
	page2 := decodePage[model.Issue](t, body)
	if len(page2.Items) != 1 || page2.Items[0].ID != done.ID || page2.NextCursor != nil {
		t.Fatalf("page 2 = %+v", page2)
	}

	for _, request := range []struct {
		path string
		want int
	}{
		{path: e.sprintPath(next) + "/history/issues", want: http.StatusConflict},
		{path: e.sprintPath(completed) + "/history/issues?cursor=bad", want: http.StatusBadRequest},
		{path: e.sprintPath(completed) + "/history/issues?limit=0", want: http.StatusBadRequest},
		{path: e.projectSprintsPath() + "/not-a-sprint/history/issues", want: http.StatusBadRequest},
		{path: e.projectSprintsPath() + "/sprint-999999/history/issues", want: http.StatusNotFound},
	} {
		code, _ := e.do(t, http.MethodGet, request.path, nil)
		if code != request.want {
			t.Fatalf("%s code = %d, want %d", request.path, code, request.want)
		}
	}
	_, outsiderToken := e.mustUserToken(t, "sprint-history-api-outsider")
	code, _ = e.doWithToken(t, outsiderToken, http.MethodGet, e.sprintPath(completed)+"/history/issues", nil)
	if code != http.StatusForbidden {
		t.Fatalf("outsider code = %d, want 403", code)
	}
}

// ---------- reorderPlannedSprints ----------

func TestHTTPReorderPlannedSprints(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := createSprintFor(t, e, "A", "2026-06-01", "2026-06-14")
	b := createSprintFor(t, e, "B", "2026-06-15", "2026-06-28")
	c := createSprintFor(t, e, "C", "2026-06-29", "2026-07-12")

	code, body := e.do(t, http.MethodPatch,
		e.projectSprintsPath()+"/planned-order",
		map[string]any{"sprint_refs": []string{c.Ref, a.Ref, b.Ref}},
	)
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	got := decode[[]model.Sprint](t, body)
	want := []uuid.UUID{c.ID, a.ID, b.ID}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d = %s, want %s", i, got[i].ID, id)
		}
		if got[i].PlannedOrder == nil || *got[i].PlannedOrder != int64(i+1) {
			t.Fatalf("position %d planned_order = %v", i, got[i].PlannedOrder)
		}
	}
}

func TestHTTPReorderPlannedSprintsRejectsBadRequests(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := createSprintFor(t, e, "A", "2026-06-01", "2026-06-14")
	b := createSprintFor(t, e, "B", "2026-06-15", "2026-06-28")
	active := createSprintFor(t, e, "active", "2026-06-29", "2026-07-12")
	if code, _ := e.do(t, http.MethodPatch, e.sprintPath(active), map[string]any{"status": "active"}); code != http.StatusOK {
		t.Fatalf("activate code = %d", code)
	}

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{name: "missing", body: map[string]any{"sprint_refs": []string{a.Ref}}, want: http.StatusConflict},
		{name: "duplicate", body: map[string]any{"sprint_refs": []string{a.Ref, a.Ref}}, want: http.StatusConflict},
		{name: "active", body: map[string]any{"sprint_refs": []string{a.Ref, active.Ref}}, want: http.StatusConflict},
		{name: "unknown", body: map[string]any{"sprint_refs": []string{a.Ref, "sprint-999999"}}, want: http.StatusNotFound},
		{name: "extra", body: map[string]any{"sprint_refs": []string{a.Ref, b.Ref, "sprint-999999"}}, want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _ := e.do(t, http.MethodPatch,
				e.projectSprintsPath()+"/planned-order", tc.body)
			if code != tc.want {
				t.Fatalf("code = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestHTTPReorderPlannedSprintsBadIDAndBody(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPatch, "/bad!/projects/TRACK/sprints/planned-order",
		map[string]any{"sprint_refs": []string{}})
	if code != http.StatusBadRequest {
		t.Fatalf("bad id code = %d", code)
	}

	req, _ := http.NewRequestWithContext(e.ctx, http.MethodPatch,
		e.ts.URL+apiPath(e.projectSprintsPath()+"/planned-order"),
		bytes.NewReader([]byte("nope")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad body code = %d", res.StatusCode)
	}

	code, _ = e.do(t, http.MethodPatch, "/"+e.ownerUsername+"/projects/"+uniqueProjectKey(t)+"/sprints/planned-order",
		map[string]any{"sprint_refs": []string{}})
	if code != http.StatusNotFound {
		t.Fatalf("not found code = %d", code)
	}
}

// ---------- getSprint ----------

func TestHTTPGetSprint(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, body := e.do(t, http.MethodPost, e.projectSprintsPath(),
		map[string]any{"name": "S", "start_date": "2026-06-01", "end_date": "2026-06-14"})
	sp := decode[model.Sprint](t, body)

	code, body := e.do(t, http.MethodGet, e.sprintPath(sp), nil)
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	got := decode[model.Sprint](t, body)
	if got.ID != sp.ID {
		t.Fatalf("id mismatch")
	}
}

func TestHTTPGetSprintBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, e.projectSprintsPath()+"/not-a-sprint", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPGetSprintNotFound(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, e.projectSprintsPath()+"/sprint-999999", nil)
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

// ---------- updateSprint ----------

func createSprintFor(t *testing.T, e *httpEnv, name, start, end string) model.Sprint {
	t.Helper()
	_, body := e.do(t, http.MethodPost, e.projectSprintsPath(),
		map[string]any{"name": name, "start_date": start, "end_date": end})
	return decode[model.Sprint](t, body)
}

func createCompletedSprintAtFor(t *testing.T, e *httpEnv, projectID uuid.UUID, name string, start, end time.Time, completedAt *time.Time) model.Sprint {
	t.Helper()
	enableFixtureSprintMode(t, e.ctx, e.pool, projectID)
	sprint, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: projectID,
		Name:      name,
		StartDate: datePtr(start),
		EndDate:   datePtr(end),
	})
	if err != nil {
		t.Fatalf("CreateSprint %s: %v", name, err)
	}
	active := model.SprintStatusActive
	if _, err := e.store.UpdateSprint(e.ctx, sprint.ID, store.UpdateSprintParams{Status: &active}); err != nil {
		t.Fatalf("activate sprint %s: %v", name, err)
	}
	if _, err := e.store.CompleteSprint(e.ctx, sprint.ID); err != nil {
		t.Fatalf("CompleteSprint %s: %v", name, err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE sprints SET completed_at = $1 WHERE id = $2`, completedAt, sprint.ID); err != nil {
		t.Fatalf("set completed_at %s: %v", name, err)
	}
	sprint, err = e.store.GetSprint(e.ctx, sprint.ID)
	if err != nil {
		t.Fatalf("GetSprint %s: %v", name, err)
	}
	return sprint
}

func TestHTTPUpdateSprintAllFields(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	code, body := e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{
		"name":       "renamed",
		"goal":       "ship",
		"start_date": "2026-06-08",
		"end_date":   "2026-06-22",
	})
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	got := decode[model.Sprint](t, body)
	if got.Name != "renamed" || got.Goal != "ship" {
		t.Fatalf("got = %+v", got)
	}
}

func TestHTTPUpdateSprintActivate(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	code, body := e.do(t, http.MethodPatch, e.sprintPath(sp),
		map[string]any{"status": "active"})
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	if decode[model.Sprint](t, body).Status != model.SprintStatusActive {
		t.Fatal("not active")
	}
}

func TestHTTPUpdateSprintRejectsCompleted(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	code, _ := e.do(t, http.MethodPatch, e.sprintPath(sp),
		map[string]any{"status": "completed"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateSprintInvalidStatus(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	code, _ := e.do(t, http.MethodPatch, e.sprintPath(sp),
		map[string]any{"status": "potato"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateSprintBadDate(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	cases := []map[string]any{
		{"start_date": "yesterday"},
		{"end_date": "tomorrow"},
	}
	for _, body := range cases {
		code, _ := e.do(t, http.MethodPatch, e.sprintPath(sp), body)
		if code != http.StatusBadRequest {
			t.Fatalf("body=%v code=%d", body, code)
		}
	}
}

func TestHTTPUpdateSprintClearDates(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	code, body := e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{"clear_dates": true})
	if code != http.StatusOK {
		t.Fatalf("clear code = %d body = %s", code, body)
	}
	got := decode[model.Sprint](t, body)
	if got.StartDate != nil || got.EndDate != nil {
		t.Fatalf("cleared dates = %v..%v, want nil..nil", got.StartDate, got.EndDate)
	}

	code, _ = e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{"start_date": "2026-07-01"})
	if code != http.StatusConflict {
		t.Fatalf("start-only code = %d, want conflict", code)
	}

	code, body = e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{
		"start_date": "2026-07-01",
		"end_date":   "2026-07-14",
	})
	if code != http.StatusOK {
		t.Fatalf("restore code = %d body = %s", code, body)
	}
	got = decode[model.Sprint](t, body)
	if got.StartDate == nil || got.EndDate == nil {
		t.Fatalf("restored dates = %v..%v, want range", got.StartDate, got.EndDate)
	}
}

func TestHTTPUpdateSprintNameAndGoalTooLong(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")

	bigName := string(bytes.Repeat([]byte("a"), 201))
	bigGoal := string(bytes.Repeat([]byte("g"), 2001))
	for _, body := range []map[string]any{{"name": bigName}, {"goal": bigGoal}} {
		code, _ := e.do(t, http.MethodPatch, e.sprintPath(sp), body)
		if code != http.StatusBadRequest {
			t.Fatalf("body=%v code=%d", body, code)
		}
	}
}

func TestHTTPUpdateSprintBadJSON(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	req, _ := http.NewRequestWithContext(e.ctx, http.MethodPatch,
		e.ts.URL+apiPath(e.sprintPath(sp)), bytes.NewReader([]byte("nope")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.authToken)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("code = %d", res.StatusCode)
	}
}

func TestHTTPUpdateSprintBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPatch, e.projectSprintsPath()+"/not-a-sprint",
		map[string]any{"name": "x"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateSprintNotFound(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPatch, e.projectSprintsPath()+"/sprint-999999",
		map[string]any{"name": "x"})
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateSprintActivationConflict(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	a := createSprintFor(t, e, "A", "2026-06-01", "2026-06-14")
	b := createSprintFor(t, e, "B", "2026-06-15", "2026-06-28")
	if code, _ := e.do(t, http.MethodPatch, e.sprintPath(a),
		map[string]any{"status": "active"}); code != http.StatusOK {
		t.Fatalf("activate A code = %d", code)
	}
	code, _ := e.do(t, http.MethodPatch, e.sprintPath(b),
		map[string]any{"status": "active"})
	if code != http.StatusConflict {
		t.Fatalf("code = %d", code)
	}
}

// ---------- completeSprint ----------

func TestHTTPCompleteSprintHappy(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{"status": "active"})

	code, body := e.do(t, http.MethodPost, e.sprintPath(sp)+"/complete", nil)
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	got := decode[model.Sprint](t, body)
	if got.Status != model.SprintStatusCompleted {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestHTTPCompleteSprintMovesUnfinishedToNextPlannedSprint(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	active := createSprintFor(t, e, "Active", "2026-06-01", "2026-06-14")
	next := createSprintFor(t, e, "Next", "2026-06-15", "2026-06-28")
	createSprintFor(t, e, "Later", "2026-06-29", "2026-07-12")
	e.do(t, http.MethodPatch, e.sprintPath(active), map[string]any{"status": "active"})

	todo, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "todo rollover"})
	if err != nil {
		t.Fatalf("CreateIssue todo: %v", err)
	}
	inProgress, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "progress rollover"})
	if err != nil {
		t.Fatalf("CreateIssue progress: %v", err)
	}
	doneIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "done stays"})
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	closedIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "closed stays"})
	if err != nil {
		t.Fatalf("CreateIssue closed: %v", err)
	}
	progressStatus := model.StatusInProgress
	doneStatus := model.StatusDone
	closedStatus := model.StatusClosed
	for _, issue := range []model.Issue{todo, inProgress, doneIssue, closedIssue} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &active.ID}); err != nil {
			t.Fatalf("assign issue %s: %v", issue.ID, err)
		}
	}
	if _, err := e.store.UpdateIssue(e.ctx, inProgress.ID, store.UpdateIssueParams{Status: &progressStatus}); err != nil {
		t.Fatalf("mark progress: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, doneIssue.ID, store.UpdateIssueParams{Status: &doneStatus}); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	closedReason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, closedIssue.ID, store.UpdateIssueParams{Status: &closedStatus, CloseReason: &closedReason}); err != nil {
		t.Fatalf("mark closed: %v", err)
	}

	code, body := e.do(t, http.MethodPost, e.sprintPath(active)+"/complete", nil)
	if code != http.StatusOK {
		t.Fatalf("complete code = %d body = %s", code, body)
	}
	for _, id := range []uuid.UUID{todo.ID, inProgress.ID} {
		got, err := e.store.GetIssue(e.ctx, id)
		if err != nil {
			t.Fatalf("GetIssue %s: %v", id, err)
		}
		if got.SprintID == nil || *got.SprintID != next.ID {
			t.Fatalf("issue %s sprint = %v, want %s", id, got.SprintID, next.ID)
		}
	}
	gotDone, err := e.store.GetIssue(e.ctx, doneIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue done: %v", err)
	}
	if gotDone.SprintID == nil || *gotDone.SprintID != active.ID {
		t.Fatalf("done sprint = %v, want %s", gotDone.SprintID, active.ID)
	}
	gotClosed, err := e.store.GetIssue(e.ctx, closedIssue.ID)
	if err != nil {
		t.Fatalf("GetIssue closed: %v", err)
	}
	if gotClosed.SprintID == nil || *gotClosed.SprintID != active.ID {
		t.Fatalf("closed sprint = %v, want %s", gotClosed.SprintID, active.ID)
	}
}

func TestHTTPCompletedSprintRenameOnly(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{"status": "active"})
	e.do(t, http.MethodPost, e.sprintPath(sp)+"/complete", nil)

	code, body := e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{"name": "renamed"})
	if code != http.StatusOK {
		t.Fatalf("rename code = %d body = %s", code, body)
	}
	if got := decode[model.Sprint](t, body); got.Name != "renamed" || got.Status != model.SprintStatusCompleted {
		t.Fatalf("got = %+v", got)
	}

	for _, body := range []map[string]any{
		{"goal": "nope"},
		{"start_date": "2026-06-02"},
		{"end_date": "2026-06-15"},
		{"clear_dates": true},
		{"status": "active"},
	} {
		code, _ := e.do(t, http.MethodPatch, e.sprintPath(sp), body)
		if code != http.StatusConflict {
			t.Fatalf("body=%v code=%d", body, code)
		}
	}
}

func TestHTTPCompleteSprintConflictNonActive(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	code, _ := e.do(t, http.MethodPost, e.sprintPath(sp)+"/complete", nil)
	if code != http.StatusConflict {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCompleteSprintBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost, e.projectSprintsPath()+"/zzz/complete", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCompleteSprintNotFound(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost, e.projectSprintsPath()+"/sprint-999999/complete", nil)
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

// ---------- issue PATCH + list with sprint filters ----------

func TestHTTPPatchIssueSetSprint(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")

	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID, Title: "task",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	code, body := e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"sprint": sp.Ref})
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	got := decode[model.Issue](t, body)
	if got.SprintID == nil || *got.SprintID != sp.ID {
		t.Fatalf("sprint_id = %v", got.SprintID)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"clear_sprint": true})
	if code != http.StatusOK {
		t.Fatalf("clear code = %d body = %s", code, body)
	}
	got = decode[model.Issue](t, body)
	if got.SprintID != nil {
		t.Fatalf("sprint_id = %v, want nil", got.SprintID)
	}
}

func TestHTTPPatchDoneIssueRejectsSprintEdit(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	current := createSprintFor(t, e, "Current", "2026-06-01", "2026-06-14")
	next := createSprintFor(t, e, "Next", "2026-06-15", "2026-06-28")
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "done issue",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, iss.ID, store.UpdateIssueParams{SprintID: &current.ID}); err != nil {
		t.Fatalf("assign done issue: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, iss.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("mark done issue: %v", err)
	}

	code, body := e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"sprint": next.Ref})
	if code != http.StatusConflict {
		t.Fatalf("set code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"clear_sprint": true})
	if code != http.StatusConflict {
		t.Fatalf("clear code = %d body = %s", code, body)
	}
	got, err := e.store.GetIssue(e.ctx, iss.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.SprintID == nil || *got.SprintID != current.ID {
		t.Fatalf("SprintID = %v, want %s", got.SprintID, current.ID)
	}

	becomingDone, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "done plus sprint",
	})
	if err != nil {
		t.Fatalf("CreateIssue becoming done: %v", err)
	}
	code, body = e.do(t, http.MethodPatch, e.issuePath(becomingDone), map[string]any{
		"status": string(model.StatusDone),
		"sprint": next.Ref,
	})
	if code != http.StatusConflict {
		t.Fatalf("status plus sprint code = %d body = %s", code, body)
	}

	becomingClosed, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "closed plus sprint",
	})
	if err != nil {
		t.Fatalf("CreateIssue becoming closed: %v", err)
	}
	code, body = e.do(t, http.MethodPatch, e.issuePath(becomingClosed), map[string]any{
		"status":       string(model.StatusClosed),
		"close_reason": string(model.CloseReasonWontDo),
		"sprint":       next.Ref,
	})
	if code != http.StatusConflict {
		t.Fatalf("closed plus sprint code = %d body = %s", code, body)
	}
}

func TestHTTPPatchIssueSetAndClearPeople(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, err := e.store.CreateUser(e.ctx, "issue-person-"+uniqueProjectKey(t)+"@example.com", "Issue Person")
	if err != nil {
		t.Fatalf("CreateUser member: %v", err)
	}
	nonMember, err := e.store.CreateUser(e.ctx, "issue-outsider-"+uniqueProjectKey(t)+"@example.com", "Issue Outsider")
	if err != nil {
		t.Fatalf("CreateUser nonmember: %v", err)
	}
	if _, err := e.store.GrantProjectAccess(e.ctx, e.projectID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	code, body := e.do(t, http.MethodPost, e.projectIssuesPath(), map[string]any{
		"title":       "created with people",
		"assignee_id": member.ID,
		"reporter_id": member.ID,
	})
	if code != http.StatusCreated {
		t.Fatalf("create people code = %d body = %s", code, body)
	}
	created := decode[model.Issue](t, body)
	if created.AssigneeID == nil || *created.AssigneeID != member.ID || created.ReporterID == nil || *created.ReporterID != member.ID {
		t.Fatalf("created people = assignee %v reporter %v, want %s", created.AssigneeID, created.ReporterID, member.ID)
	}
	code, body = e.do(t, http.MethodPost, e.projectIssuesPath(), map[string]any{
		"title":       "nonmember assignee",
		"assignee_id": nonMember.ID,
	})
	if code != http.StatusNotFound {
		t.Fatalf("create non-member assignee code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPost, e.projectIssuesPath(), map[string]any{
		"title":       "missing reporter",
		"reporter_id": uuid.NewString(),
	})
	if code != http.StatusNotFound {
		t.Fatalf("create missing reporter code = %d body = %s", code, body)
	}
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "people",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{
		"assignee_id": member.ID,
		"reporter_id": member.ID,
	})
	if code != http.StatusOK {
		t.Fatalf("set people code = %d body = %s", code, body)
	}
	got := decode[model.Issue](t, body)
	if got.AssigneeID == nil || *got.AssigneeID != member.ID || got.ReporterID == nil || *got.ReporterID != member.ID {
		t.Fatalf("people = assignee %v reporter %v, want %s", got.AssigneeID, got.ReporterID, member.ID)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{
		"clear_assignee": true,
		"clear_reporter": true,
	})
	if code != http.StatusOK {
		t.Fatalf("clear people code = %d body = %s", code, body)
	}
	got = decode[model.Issue](t, body)
	if got.AssigneeID != nil || got.ReporterID != nil {
		t.Fatalf("cleared people = assignee %v reporter %v, want nil", got.AssigneeID, got.ReporterID)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"assignee_id": nonMember.ID})
	if code != http.StatusNotFound {
		t.Fatalf("non-member assignee code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"reporter_id": uuid.NewString()})
	if code != http.StatusNotFound {
		t.Fatalf("missing reporter code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"assignee_id": "not-a-uuid"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad assignee id code = %d body = %s", code, body)
	}
}

func TestHTTPPatchIssueCrossProjectSprint(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	other, err := e.store.CreateProject(e.ctx, uniqueProjectKey(t), "other", "")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherSp, err := e.store.CreateSprint(e.ctx, store.CreateSprintParams{
		ProjectID: other.ID, Name: "x",
		StartDate: datePtr(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)),
		EndDate:   datePtr(time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("CreateSprint other: %v", err)
	}
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID, Title: "t",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	code, _ := e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"sprint": otherSp.Ref})
	if code != http.StatusNotFound {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPPatchIssueCompletedSprintRejected(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	e.do(t, http.MethodPatch, e.sprintPath(sp), map[string]any{"status": "active"})
	e.do(t, http.MethodPost, e.sprintPath(sp)+"/complete", nil)
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "task",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	code, _ := e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"sprint": sp.Ref})
	if code != http.StatusConflict {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListIssuesBacklogAndSprintFilters(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	inSprint, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "in"})
	if err != nil {
		t.Fatalf("CreateIssue in: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, inSprint.ID,
		store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign: %v", err)
	}
	backlog, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "backlog"})
	if err != nil {
		t.Fatalf("CreateIssue backlog: %v", err)
	}

	code, body := e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint=backlog", nil)
	if code != http.StatusOK {
		t.Fatalf("backlog code = %d", code)
	}
	got := decodePage[model.Issue](t, body).Items
	if len(got) != 1 || got[0].ID != backlog.ID {
		t.Fatalf("backlog list = %+v, want only %s", got, backlog.ID)
	}

	code, body = e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint="+sp.Ref, nil)
	if code != http.StatusOK {
		t.Fatalf("by sprint code = %d", code)
	}
	got = decodePage[model.Issue](t, body).Items
	if len(got) != 1 || got[0].ID != inSprint.ID {
		t.Fatalf("by sprint = %+v, want only %s", got, inSprint.ID)
	}
}

func TestHTTPListIssuesAssigneeFilters(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	alice, _ := e.mustProjectMemberToken(t, "assignee-alice")
	bob, _ := e.mustProjectMemberToken(t, "assignee-bob")
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")

	aliceIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "alice sprint issue",
		AssigneeID: &alice.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue alice: %v", err)
	}
	bobIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "bob sprint issue",
		AssigneeID: &bob.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue bob: %v", err)
	}
	unassigned, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "unassigned sprint issue"})
	if err != nil {
		t.Fatalf("CreateIssue unassigned: %v", err)
	}
	aliceBacklog, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "alice backlog issue",
		AssigneeID: &alice.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue alice backlog: %v", err)
	}
	for _, issue := range []model.Issue{aliceIssue, bobIssue, unassigned} {
		if _, err := e.store.UpdateIssue(e.ctx, issue.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
			t.Fatalf("assign %s: %v", issue.Identifier, err)
		}
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, bobIssue.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("set bob done: %v", err)
	}

	code, body := e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint="+sp.Ref+"&assignee_id="+alice.ID.String()+"&assignee_id="+bob.ID.String(), nil)
	if code != http.StatusOK {
		t.Fatalf("assignee code = %d body = %s", code, body)
	}
	got := decodePage[model.Issue](t, body).Items
	if len(got) != 2 || got[0].ID != aliceIssue.ID || got[1].ID != bobIssue.ID {
		t.Fatalf("assignee list = %+v, want alice/bob sprint issues", got)
	}
	for _, notWant := range []uuid.UUID{unassigned.ID, aliceBacklog.ID} {
		for _, issue := range got {
			if issue.ID == notWant {
				t.Fatalf("assignee filter included %s in %+v", notWant, got)
			}
		}
	}

	code, body = e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint="+sp.Ref+"&status=done&assignee_id="+bob.ID.String(), nil)
	if code != http.StatusOK {
		t.Fatalf("assignee+status code = %d body = %s", code, body)
	}
	got = decodePage[model.Issue](t, body).Items
	if len(got) != 1 || got[0].ID != bobIssue.ID {
		t.Fatalf("assignee+status list = %+v, want bob done", got)
	}

	closedFiltered, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "bob closed sprint issue",
		AssigneeID: &bob.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue closed filtered: %v", err)
	}
	if _, err := e.store.UpdateIssue(e.ctx, closedFiltered.ID, store.UpdateIssueParams{SprintID: &sp.ID}); err != nil {
		t.Fatalf("assign closed filtered: %v", err)
	}
	closed := model.StatusClosed
	closedReason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, closedFiltered.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &closedReason}); err != nil {
		t.Fatalf("set closed filtered: %v", err)
	}
	code, body = e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint="+sp.Ref+"&status=closed&assignee_id="+bob.ID.String(), nil)
	if code != http.StatusOK {
		t.Fatalf("assignee+closed code = %d body = %s", code, body)
	}
	got = decodePage[model.Issue](t, body).Items
	if len(got) != 1 || got[0].ID != closedFiltered.ID {
		t.Fatalf("assignee+closed list = %+v, want bob closed", got)
	}

	code, _ = e.do(t, http.MethodGet, e.projectIssuesPath()+"?assignee_id=nope", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad assignee_id code = %d", code)
	}
}

func TestHTTPListIssuesMultiStatusPrioritySortAndCursor(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	todoP3, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "todo p3",
		Priority:  model.PriorityP3,
	})
	if err != nil {
		t.Fatalf("CreateIssue todo: %v", err)
	}
	doneP0, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "done p0",
		Priority:  model.PriorityP0,
	})
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	progressP1, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "progress p1",
		Priority:  model.PriorityP1,
	})
	if err != nil {
		t.Fatalf("CreateIssue progress: %v", err)
	}
	closedP4, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "closed p4",
		Priority:  model.PriorityP4,
	})
	if err != nil {
		t.Fatalf("CreateIssue closed: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, doneP0.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("set done: %v", err)
	}
	progress := model.StatusInProgress
	if _, err := e.store.UpdateIssue(e.ctx, progressP1.ID, store.UpdateIssueParams{Status: &progress}); err != nil {
		t.Fatalf("set progress: %v", err)
	}
	closed := model.StatusClosed
	reason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, closedP4.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &reason}); err != nil {
		t.Fatalf("set closed: %v", err)
	}

	path := e.projectIssuesPath() + "?status=todo&status=done&status=in_progress&priority=P0&priority=P1&priority=P3&sort=priority&limit=2"
	code, body := e.do(t, http.MethodGet, path, nil)
	if code != http.StatusOK {
		t.Fatalf("page 1 code = %d body = %s", code, body)
	}
	page := decodePage[model.Issue](t, body)
	if len(page.Items) != 2 || page.Items[0].ID != doneP0.ID || page.Items[1].ID != progressP1.ID {
		t.Fatalf("page 1 issues = %+v, want done/progress by priority", page.Items)
	}
	if page.NextCursor == nil {
		t.Fatal("page 1 next_cursor = nil, want cursor")
	}

	code, body = e.do(t, http.MethodGet, path+"&cursor="+url.QueryEscape(*page.NextCursor), nil)
	if code != http.StatusOK {
		t.Fatalf("page 2 code = %d body = %s", code, body)
	}
	page = decodePage[model.Issue](t, body)
	if len(page.Items) != 1 || page.Items[0].ID != todoP3.ID {
		t.Fatalf("page 2 issues = %+v, want todo p3", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatalf("page 2 next_cursor = %q, want nil", *page.NextCursor)
	}
	for _, issue := range page.Items {
		if issue.ID == closedP4.ID {
			t.Fatalf("filtered page included closed issue: %+v", page.Items)
		}
	}
}

func TestHTTPListProjectAssignees(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, memberToken := e.mustProjectMemberToken(t, "project-assignee-member")
	assigned, _ := e.mustProjectMemberToken(t, "project-assignee-assigned")
	unrelated, _ := e.mustUserToken(t, "project-assignee-unrelated")
	if _, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "assigned issue",
		AssigneeID: &assigned.ID,
	}); err != nil {
		t.Fatalf("CreateIssue assigned: %v", err)
	}

	code, body := e.doWithToken(t, memberToken, http.MethodGet, e.projectPath()+"/assignees", nil)
	if code != http.StatusOK {
		t.Fatalf("assignees code = %d body = %s", code, body)
	}
	got := decode[[]model.ProjectAssignee](t, body)
	if !httpProjectAssigneesContain(got, member.ID) || !httpProjectAssigneesContain(got, assigned.ID) {
		t.Fatalf("assignees missing member/assigned: %+v", got)
	}
	if httpProjectAssigneesContain(got, unrelated.ID) {
		t.Fatalf("assignees included unrelated user: %+v", got)
	}

	_, deniedToken := e.mustUserToken(t, "project-assignee-denied")
	code, _ = e.doWithToken(t, deniedToken, http.MethodGet, e.projectPath()+"/assignees", nil)
	if code != http.StatusForbidden {
		t.Fatalf("denied assignees code = %d", code)
	}
}

func TestHTTPGetProjectStats(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, memberToken := e.mustProjectMemberToken(t, "project-stats-member")
	todoIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "stats todo",
		AssigneeID: &member.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue todo: %v", err)
	}
	_ = todoIssue
	doneIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID:  e.projectID,
		Title:      "stats done",
		AssigneeID: &member.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue done: %v", err)
	}
	done := model.StatusDone
	if _, err := e.store.UpdateIssue(e.ctx, doneIssue.ID, store.UpdateIssueParams{Status: &done}); err != nil {
		t.Fatalf("UpdateIssue done: %v", err)
	}
	closedIssue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{
		ProjectID: e.projectID,
		Title:     "stats closed",
	})
	if err != nil {
		t.Fatalf("CreateIssue closed: %v", err)
	}
	closed := model.StatusClosed
	reason := model.CloseReasonWontDo
	if _, err := e.store.UpdateIssue(e.ctx, closedIssue.ID, store.UpdateIssueParams{Status: &closed, CloseReason: &reason}); err != nil {
		t.Fatalf("UpdateIssue closed: %v", err)
	}

	code, body := e.doWithToken(t, memberToken, http.MethodGet, e.projectPath()+"/stats", nil)
	if code != http.StatusOK {
		t.Fatalf("stats code = %d body = %s", code, body)
	}
	stats := decode[model.ProjectStats](t, body)
	if stats.ProjectID != e.projectID {
		t.Fatalf("stats project id = %s, want %s", stats.ProjectID, e.projectID)
	}
	wantCounts := model.ProjectIssueStatusCounts{Total: 3, Todo: 1, Done: 1, Closed: 1}
	if stats.AllTime != wantCounts || stats.Last7Days != wantCounts {
		t.Fatalf("stats counts = all %+v week %+v, want %+v", stats.AllTime, stats.Last7Days, wantCounts)
	}
	if len(stats.TopAssignees) != 1 || stats.TopAssignees[0].UserID != member.ID || stats.TopAssignees[0].Counts.Total != 2 {
		t.Fatalf("top assignees = %+v, want member with 2 assigned issues", stats.TopAssignees)
	}

	_, deniedToken := e.mustUserToken(t, "project-stats-denied")
	code, _ = e.doWithToken(t, deniedToken, http.MethodGet, e.projectPath()+"/stats", nil)
	if code != http.StatusForbidden {
		t.Fatalf("denied stats code = %d", code)
	}
	code, _ = e.do(t, http.MethodGet, "/bad!/projects/"+e.projKey+"/stats", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad owner stats code = %d", code)
	}
	code, _ = e.do(t, http.MethodGet, "/"+e.ownerUsername+"/projects/bad!/stats", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad key stats code = %d", code)
	}
	code, _ = e.do(t, http.MethodGet, "/"+e.ownerUsername+"/projects/"+uniqueProjectKey(t)+"/stats", nil)
	if code != http.StatusNotFound {
		t.Fatalf("missing project stats code = %d", code)
	}
}

func httpProjectAssigneesContain(in []model.ProjectAssignee, id uuid.UUID) bool {
	for _, assignee := range in {
		if assignee.ID == id {
			return true
		}
	}
	return false
}

func TestHTTPListIssuesMutuallyExclusive(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	sp := createSprintFor(t, e, "S", "2026-06-01", "2026-06-14")
	code, _ := e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint=backlog&sprint_id="+sp.ID.String(), nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListIssuesBadSprintParam(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint=potato", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListIssuesBadSprintID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet,
		e.projectIssuesPath()+"?sprint_id=zzz", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

// ---------- pre-existing issue handlers (light coverage so sprint_id wiring
// in those handlers is exercised too) ----------

func TestHTTPCreateIssueAndGet(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, body := e.do(t, http.MethodPost,
		e.projectIssuesPath(),
		map[string]any{"title": "first"})
	if code != http.StatusCreated {
		t.Fatalf("create code = %d body = %s", code, body)
	}
	iss := decode[model.Issue](t, body)
	if iss.SprintID != nil {
		t.Fatalf("new issue should default to backlog, got sprint_id %v", iss.SprintID)
	}

	code, body = e.do(t, http.MethodGet, e.issuePath(iss), nil)
	if code != http.StatusOK {
		t.Fatalf("get code = %d", code)
	}
	got := decode[model.Issue](t, body)
	if got.ID != iss.ID {
		t.Fatal("id mismatch")
	}
}

func TestHTTPCreateAndUpdateIssuePriority(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, body := e.do(t, http.MethodPost,
		e.projectIssuesPath(),
		map[string]any{"title": "default priority"})
	if code != http.StatusCreated {
		t.Fatalf("create default code = %d body = %s", code, body)
	}
	defaultIssue := decode[model.Issue](t, body)
	if defaultIssue.Priority != model.PriorityP2 {
		t.Fatalf("default Priority = %q, want %q", defaultIssue.Priority, model.PriorityP2)
	}

	code, body = e.do(t, http.MethodPost,
		e.projectIssuesPath(),
		map[string]any{"title": "urgent", "priority": string(model.PriorityP0)})
	if code != http.StatusCreated {
		t.Fatalf("create explicit code = %d body = %s", code, body)
	}
	urgent := decode[model.Issue](t, body)
	if urgent.Priority != model.PriorityP0 {
		t.Fatalf("explicit Priority = %q, want %q", urgent.Priority, model.PriorityP0)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(urgent),
		map[string]any{"priority": string(model.PriorityP4)})
	if code != http.StatusOK {
		t.Fatalf("patch priority code = %d body = %s", code, body)
	}
	updated := decode[model.Issue](t, body)
	if updated.Priority != model.PriorityP4 {
		t.Fatalf("updated Priority = %q, want %q", updated.Priority, model.PriorityP4)
	}

	code, _ = e.do(t, http.MethodPost,
		e.projectIssuesPath(),
		map[string]any{"title": "bad priority", "priority": "p0"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad create priority code = %d, want %d", code, http.StatusBadRequest)
	}

	code, _ = e.do(t, http.MethodPatch, e.issuePath(urgent),
		map[string]any{"priority": "P5"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad update priority code = %d, want %d", code, http.StatusBadRequest)
	}
	got, err := e.store.GetIssue(e.ctx, urgent.ID)
	if err != nil {
		t.Fatalf("GetIssue after bad priority: %v", err)
	}
	if got.Priority != model.PriorityP4 {
		t.Fatalf("bad priority changed Priority = %q, want %q", got.Priority, model.PriorityP4)
	}
}

func TestHTTPCreateAndUpdateIssueDueDate(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, body := e.do(t, http.MethodPost,
		e.projectIssuesPath(),
		map[string]any{"title": "due api", "due_date": "2026-06-24"})
	if code != http.StatusCreated {
		t.Fatalf("create due date code = %d body = %s", code, body)
	}
	iss := decode[model.Issue](t, body)
	if iss.DueDate == nil || iss.DueDate.String() != "2026-06-24" {
		t.Fatalf("create DueDate = %v", iss.DueDate)
	}

	code, body = e.do(t, http.MethodGet, e.issuePath(iss), nil)
	if code != http.StatusOK {
		t.Fatalf("get due date code = %d body = %s", code, body)
	}
	got := decode[model.Issue](t, body)
	if got.DueDate == nil || got.DueDate.String() != "2026-06-24" {
		t.Fatalf("get DueDate = %v", got.DueDate)
	}

	code, body = e.do(t, http.MethodGet, e.projectIssuesPath(), nil)
	if code != http.StatusOK {
		t.Fatalf("list due date code = %d body = %s", code, body)
	}
	listed := decodePage[model.Issue](t, body).Items
	if len(listed) != 1 || listed[0].DueDate == nil || listed[0].DueDate.String() != "2026-06-24" {
		t.Fatalf("listed = %+v", listed)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"due_date": "2026-06-26"})
	if code != http.StatusOK {
		t.Fatalf("patch due date code = %d body = %s", code, body)
	}
	updated := decode[model.Issue](t, body)
	if updated.DueDate == nil || updated.DueDate.String() != "2026-06-26" {
		t.Fatalf("updated DueDate = %v", updated.DueDate)
	}

	code, body = e.do(t, http.MethodPost, e.issueSubIssuesPath(iss),
		map[string]any{"title": "due sub", "due_date": "2026-06-27"})
	if code != http.StatusCreated {
		t.Fatalf("create sub due date code = %d body = %s", code, body)
	}
	child := decode[model.Issue](t, body)
	if child.DueDate == nil || child.DueDate.String() != "2026-06-27" {
		t.Fatalf("child DueDate = %v", child.DueDate)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"clear_due_date": true})
	if code != http.StatusOK {
		t.Fatalf("clear due date code = %d body = %s", code, body)
	}
	cleared := decode[model.Issue](t, body)
	if cleared.DueDate != nil {
		t.Fatalf("cleared DueDate = %v, want nil", cleared.DueDate)
	}

	code, _ = e.do(t, http.MethodPost,
		e.projectIssuesPath(),
		map[string]any{"title": "bad due", "due_date": "2026/06/24"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad create due date code = %d, want %d", code, http.StatusBadRequest)
	}

	code, _ = e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"due_date": "tomorrow"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad update due date code = %d, want %d", code, http.StatusBadRequest)
	}
}

func TestHTTPCreateIssueBadProject(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost, "/bad!/projects/TRACK/issues",
		map[string]any{"title": "x"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPCreateIssueMissingTitle(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPost,
		e.projectIssuesPath(),
		map[string]any{"title": ""})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateIssueBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodPatch, "/"+e.ownerUsername+"/issues/not-uuid",
		map[string]any{"title": "x"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateIssueBadStatus(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "t"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	code, _ := e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"status": "blocked"})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPUpdateIssueClosedStatus(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "closed via api"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	code, body := e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"status": string(model.StatusClosed), "close_reason": string(model.CloseReasonWontDo)})
	if code != http.StatusOK {
		t.Fatalf("code = %d body = %s", code, body)
	}
	got := decode[model.Issue](t, body)
	if got.Status != model.StatusClosed {
		t.Fatalf("status = %s, want closed", got.Status)
	}
	if got.CloseReason == nil || *got.CloseReason != model.CloseReasonWontDo {
		t.Fatalf("close reason = %v, want wont_do", got.CloseReason)
	}
}

func TestHTTPUpdateIssueCloseReasonValidationAndReopen(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "close reason api"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	code, _ := e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"status": string(model.StatusClosed)})
	if code != http.StatusBadRequest {
		t.Fatalf("missing close_reason code = %d, want 400", code)
	}
	code, _ = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{
		"status":       string(model.StatusClosed),
		"close_reason": "bogus",
	})
	if code != http.StatusBadRequest {
		t.Fatalf("invalid close_reason code = %d, want 400", code)
	}
	code, _ = e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{"close_reason": string(model.CloseReasonInvalid)})
	if code != http.StatusBadRequest {
		t.Fatalf("reason on open issue code = %d, want 400", code)
	}

	code, body := e.do(t, http.MethodPatch, e.issuePath(iss), map[string]any{
		"status":       string(model.StatusClosed),
		"close_reason": string(model.CloseReasonDuplicate),
	})
	if code != http.StatusOK {
		t.Fatalf("close code = %d body = %s", code, body)
	}
	closed := decode[model.Issue](t, body)
	if closed.Status != model.StatusClosed || closed.CloseReason == nil || *closed.CloseReason != model.CloseReasonDuplicate {
		t.Fatalf("closed issue = %+v", closed)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(closed), map[string]any{"close_reason": string(model.CloseReasonInvalid)})
	if code != http.StatusOK {
		t.Fatalf("reason update code = %d body = %s", code, body)
	}
	updated := decode[model.Issue](t, body)
	if updated.CloseReason == nil || *updated.CloseReason != model.CloseReasonInvalid {
		t.Fatalf("updated close reason = %v, want invalid", updated.CloseReason)
	}

	code, body = e.do(t, http.MethodPatch, e.issuePath(updated), map[string]any{"status": string(model.StatusInProgress)})
	if code != http.StatusOK {
		t.Fatalf("reopen code = %d body = %s", code, body)
	}
	reopened := decode[model.Issue](t, body)
	if reopened.Status != model.StatusInProgress || reopened.CloseReason != nil {
		t.Fatalf("reopened issue = status %s reason %v, want in_progress/nil", reopened.Status, reopened.CloseReason)
	}
}

func TestHTTPUpdateIssueTitleTooLong(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	iss, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: e.projectID, Title: "t"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	long := string(bytes.Repeat([]byte("a"), 201))
	code, _ := e.do(t, http.MethodPatch, e.issuePath(iss),
		map[string]any{"title": long})
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPGetIssueBadID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, "/"+e.ownerUsername+"/issues/zzz", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListIssuesBadProjectID(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	code, _ := e.do(t, http.MethodGet, "/bad!/projects/TRACK/issues", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d", code)
	}
}

func TestHTTPListIssuesBadListParams(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	for _, query := range []string{
		"status=banana",
		"priority=P9",
		"sort=banana",
		"direction=sideways",
	} {
		code, _ := e.do(t, http.MethodGet, e.projectIssuesPath()+"?"+query, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("%s code = %d, want 400", query, code)
		}
	}
}
