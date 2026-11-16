package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
)

// formWaitEnv is a server that reads a waited-on form every few milliseconds,
// so a test that waits for a form doesn't wait for the clock.
func formWaitEnv(t *testing.T, wait server.FormWaitOptions) *httpEnv {
	t.Helper()
	if wait.Poll == 0 {
		wait.Poll = 10 * time.Millisecond
	}
	return newHTTPEnvWithOptions(t, server.Options{FormWait: wait})
}

type formWaitResult struct {
	code    int
	status  model.FormStatus
	body    string
	header  http.Header
	elapsed time.Duration
	err     error
}

// getFormWait calls a wait URL with no credentials at all, as an agent's
// background command does.
func (e *httpEnv) getFormWait(ctx context.Context, waitURL, query string) formWaitResult {
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ts.URL+waitURL+query, nil)
	if err != nil {
		return formWaitResult{err: err}
	}
	res, err := e.ts.Client().Do(req)
	if err != nil {
		return formWaitResult{err: err, elapsed: time.Since(started)}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return formWaitResult{err: err, elapsed: time.Since(started)}
	}
	out := formWaitResult{code: res.StatusCode, body: string(raw), header: res.Header, elapsed: time.Since(started)}
	var parsed struct {
		Status model.FormStatus `json:"status"`
	}
	if json.Unmarshal(raw, &parsed) == nil {
		out.status = parsed.Status
	}
	return out
}

func (e *httpEnv) mustGetFormWait(t *testing.T, waitURL, query string) formWaitResult {
	t.Helper()
	out := e.getFormWait(e.ctx, waitURL, query)
	if out.err != nil {
		t.Fatalf("GET wait URL: %v", out.err)
	}
	return out
}

// goFormWait starts a wait in the background and returns where its result
// arrives.
func (e *httpEnv) goFormWait(waitURL, query string) <-chan formWaitResult {
	done := make(chan formWaitResult, 1)
	go func() { done <- e.getFormWait(e.ctx, waitURL, query) }()
	return done
}

func mustReceiveFormWait(t *testing.T, done <-chan formWaitResult) formWaitResult {
	t.Helper()
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("waiting request: %v", out.err)
		}
		return out
	case <-time.After(20 * time.Second):
		t.Fatal("waiting request never returned")
		return formWaitResult{}
	}
}

func TestFormWaitURLSaysOnlyTheStatus(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 5 * time.Second})
	created := e.mustCreateForm(t, nil)
	if !strings.HasPrefix(created.WaitURL, "/api/v1/form-waits/") || len(created.WaitURL) < len("/api/v1/form-waits/")+26 {
		t.Fatalf("wait_url = %q", created.WaitURL)
	}
	second := e.mustCreateForm(t, nil)
	if second.WaitURL == created.WaitURL {
		t.Fatal("two forms were given the same wait URL")
	}

	// The key is shown once: reading or listing the form never repeats it.
	code, raw := e.do(t, http.MethodGet, e.formPath(created.Ref), nil)
	if code != http.StatusOK || strings.Contains(string(raw), "wait_url") {
		t.Fatalf("get form code = %d body = %s", code, raw)
	}
	code, raw = e.do(t, http.MethodGet, e.formsPath(), nil)
	if code != http.StatusOK || strings.Contains(string(raw), "wait_url") || strings.Contains(string(raw), "wait_key") {
		t.Fatalf("list forms code = %d body = %s", code, raw)
	}

	// With no credentials, the URL answers with the status and nothing else.
	open := e.mustGetFormWait(t, created.WaitURL, "")
	if open.code != http.StatusOK || strings.TrimSpace(open.body) != `{"status":"open"}` {
		t.Fatalf("open: code = %d body = %s", open.code, open.body)
	}
	if got := open.header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if zero := e.mustGetFormWait(t, created.WaitURL, "?wait=0"); zero.code != http.StatusOK || zero.status != model.FormStatusOpen {
		t.Fatalf("wait=0: %+v", zero)
	}

	// Once the form is no longer open, a wait returns at once.
	e.mustSubmitForm(t, e.authToken, created.Ref)
	submitted := e.mustGetFormWait(t, created.WaitURL, "?wait=5")
	if submitted.code != http.StatusOK || strings.TrimSpace(submitted.body) != `{"status":"submitted"}` || submitted.elapsed > 2*time.Second {
		t.Fatalf("submitted: %+v", submitted)
	}
	if other := e.mustGetFormWait(t, second.WaitURL, ""); other.status != model.FormStatusOpen {
		t.Fatalf("the other form's wait URL says %q", other.status)
	}

	for _, query := range []string{"?wait=soon", "?wait=-1", "?wait=1.5"} {
		if bad := e.mustGetFormWait(t, created.WaitURL, query); bad.code != http.StatusBadRequest || !strings.Contains(bad.body, "whole number of seconds") {
			t.Fatalf("%s: code = %d body = %s", query, bad.code, bad.body)
		}
	}
	for _, path := range []string{"/api/v1/form-waits/NOTAKEYANYONEWASGIVEN00000", "/api/v1/form-waits/" + created.Ref} {
		if missing := e.mustGetFormWait(t, path, "?wait=5"); missing.code != http.StatusNotFound || missing.elapsed > 2*time.Second {
			t.Fatalf("%s: %+v", path, missing)
		}
	}
	// Only GET: a wait key is no credential for anything else. Another method
	// falls through to the routes that want an account.
	req, _ := http.NewRequestWithContext(e.ctx, http.MethodPost, e.ts.URL+created.WaitURL, nil)
	res, err := e.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("POST wait URL: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST wait URL code = %d, want 401", res.StatusCode)
	}
}

func TestFormWaitHoldsUntilTheFormIsAnswered(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 10 * time.Second})

	answered := e.mustCreateForm(t, nil)
	waiting := e.goFormWait(answered.WaitURL, "?wait=10")
	time.Sleep(150 * time.Millisecond)
	select {
	case early := <-waiting:
		t.Fatalf("wait returned while the form was still open: %+v", early)
	default:
	}
	e.mustSubmitForm(t, e.authToken, answered.Ref)
	got := mustReceiveFormWait(t, waiting)
	if got.code != http.StatusOK || got.status != model.FormStatusSubmitted || got.elapsed < 150*time.Millisecond || got.elapsed > 8*time.Second {
		t.Fatalf("after submit: %+v", got)
	}

	cancelled := e.mustCreateForm(t, nil)
	waiting = e.goFormWait(cancelled.WaitURL, "?wait=10")
	time.Sleep(50 * time.Millisecond)
	if code, raw := e.do(t, http.MethodPost, e.formPath(cancelled.Ref)+"/cancel", nil); code != http.StatusOK {
		t.Fatalf("cancel code = %d body = %s", code, raw)
	}
	if got := mustReceiveFormWait(t, waiting); got.code != http.StatusOK || got.status != model.FormStatusCancelled || got.elapsed > 8*time.Second {
		t.Fatalf("after cancel: %+v", got)
	}
}

func TestFormWaitGivesUpWhenItsTimeRunsOut(t *testing.T) {
	t.Parallel()
	// The request timeout is far shorter than the wait: a wait URL has to be
	// given the longer deadline or it would be cut off first.
	e := newHTTPEnvWithOptions(t, server.Options{
		RequestTimeout: time.Second,
		FormWait:       server.FormWaitOptions{Max: 1500 * time.Millisecond, Poll: 10 * time.Millisecond},
	})
	created := e.mustCreateForm(t, nil)
	// 50 seconds is more than this server allows, so it waits as long as it does allow.
	got := e.mustGetFormWait(t, created.WaitURL, "?wait=50")
	if got.code != http.StatusOK || strings.TrimSpace(got.body) != `{"status":"open"}` {
		t.Fatalf("timed out wait: code = %d body = %s", got.code, got.body)
	}
	if got.elapsed < 1500*time.Millisecond || got.elapsed > 8*time.Second {
		t.Fatalf("timed out wait took %v, want about 1.5s", got.elapsed)
	}
}

func TestFormWaitStopsWhenItsCreatorLosesTheProject(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 10 * time.Second})
	member, memberToken := e.mustProjectMemberToken(t, "wait-member")
	created := e.mustCreateFormWithToken(t, memberToken, nil)
	if created.CreatedByID != member.ID {
		t.Fatalf("created by %s, want the member", created.CreatedByID)
	}
	if got := e.mustGetFormWait(t, created.WaitURL, ""); got.status != model.FormStatusOpen {
		t.Fatalf("before: %+v", got)
	}

	waiting := e.goFormWait(created.WaitURL, "?wait=10")
	time.Sleep(50 * time.Millisecond)
	if err := e.store.RevokeProjectAccess(e.ctx, e.projectID, member.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	// The wait ends exactly as a key that was never issued: not found, and
	// nothing about the form.
	got := mustReceiveFormWait(t, waiting)
	if got.code != http.StatusNotFound || strings.Contains(got.body, "open") || got.elapsed > 8*time.Second {
		t.Fatalf("after losing the project: %+v", got)
	}
	if after := e.mustGetFormWait(t, created.WaitURL, ""); after.code != http.StatusNotFound {
		t.Fatalf("afterwards: %+v", after)
	}
}

func TestFormWaitRefusesMoreThanAFormsShare(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 10 * time.Second, PerForm: 1})
	created := e.mustCreateForm(t, nil)
	other := e.mustCreateForm(t, nil)

	first := e.goFormWait(created.WaitURL, "?wait=10")
	time.Sleep(100 * time.Millisecond)
	// One request may wait on this form. Whichever of the two got there
	// first holds the place, and the other is turned away at once.
	second := e.goFormWait(created.WaitURL, "?wait=1")
	// Another form has its own share.
	elsewhere := e.goFormWait(other.WaitURL, "?wait=10")
	time.Sleep(100 * time.Millisecond)
	// A plain status check never waits, so it is never turned away.
	if check := e.mustGetFormWait(t, created.WaitURL, ""); check.code != http.StatusOK || check.status != model.FormStatusOpen {
		t.Fatalf("status check while others wait: %+v", check)
	}
	for _, ref := range []string{created.Ref, other.Ref} {
		if code, raw := e.do(t, http.MethodPost, e.formPath(ref)+"/cancel", nil); code != http.StatusOK {
			t.Fatalf("cancel %s code = %d body = %s", ref, code, raw)
		}
	}

	results := []formWaitResult{mustReceiveFormWait(t, first), mustReceiveFormWait(t, second)}
	sort.Slice(results, func(i, j int) bool { return results[i].code < results[j].code })
	if results[0].code != http.StatusOK || results[1].code != http.StatusTooManyRequests {
		t.Fatalf("codes = %d and %d, want 200 and 429", results[0].code, results[1].code)
	}
	if results[1].header.Get("Retry-After") == "" || !strings.Contains(results[1].body, "already waiting") || results[1].elapsed > 2*time.Second {
		t.Fatalf("refused wait: %+v", results[1])
	}
	if got := mustReceiveFormWait(t, elsewhere); got.code != http.StatusOK || got.status != model.FormStatusCancelled {
		t.Fatalf("wait on the other form: %+v", got)
	}
}

func TestFormWaitGivesItsPlaceBackWhenTheCallerGoesAway(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 30 * time.Second, PerForm: 1})
	created := e.mustCreateForm(t, nil)

	ctx, hangUp := context.WithTimeout(e.ctx, 150*time.Millisecond)
	defer hangUp()
	if gone := e.getFormWait(ctx, created.WaitURL, "?wait=30"); !errors.Is(gone.err, context.DeadlineExceeded) {
		t.Fatalf("abandoned wait = %+v, want the caller's own timeout", gone)
	}

	// The server notices the caller left and frees the form's only place, so
	// a new wait is let in long before the abandoned one would have ended.
	deadline := time.Now().Add(10 * time.Second)
	for {
		probe, stop := context.WithTimeout(e.ctx, 300*time.Millisecond)
		next := e.getFormWait(probe, created.WaitURL, "?wait=30")
		stop()
		if errors.Is(next.err, context.DeadlineExceeded) {
			return // it was let in and is waiting; we hung up on it
		}
		if next.err != nil || next.code != http.StatusTooManyRequests {
			t.Fatalf("next wait = %+v", next)
		}
		if time.Now().After(deadline) {
			t.Fatal("the abandoned wait never gave its place back")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mcpGetFormWaiting calls track_get_form without failing the test on a tool
// error, so a test can see how a wait was refused.
func mcpGetFormWaiting(e *httpEnv, session *mcp.ClientSession, ref string, waitSeconds int) (formJSON, string, error) {
	res, err := session.CallTool(e.ctx, &mcp.CallToolParams{Name: "track_get_form", Arguments: mcpFormArgs(e, map[string]any{
		"form": ref, "wait_seconds": waitSeconds,
	})})
	if err != nil {
		return formJSON{}, "", err
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return formJSON{}, "", err
	}
	if res.IsError {
		var failed struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &failed); err != nil {
			return formJSON{}, "", err
		}
		return formJSON{}, failed.Error.Code, nil
	}
	var out struct {
		Form formJSON `json:"form"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return formJSON{}, "", err
	}
	return out.Form, "", nil
}

type mcpFormWaitResult struct {
	form    formJSON
	code    string
	elapsed time.Duration
	err     error
}

func goMCPGetFormWaiting(e *httpEnv, session *mcp.ClientSession, ref string, waitSeconds int) <-chan mcpFormWaitResult {
	done := make(chan mcpFormWaitResult, 1)
	go func() {
		started := time.Now()
		form, code, err := mcpGetFormWaiting(e, session, ref, waitSeconds)
		done <- mcpFormWaitResult{form: form, code: code, elapsed: time.Since(started), err: err}
	}()
	return done
}

func mustReceiveMCPFormWait(t *testing.T, done <-chan mcpFormWaitResult) mcpFormWaitResult {
	t.Helper()
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("waiting track_get_form: %v", out.err)
		}
		return out
	case <-time.After(20 * time.Second):
		t.Fatal("waiting track_get_form never returned")
		return mcpFormWaitResult{}
	}
}

func TestMCPCreateFormReturnsAWaitURL(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 5 * time.Second})
	session := mcpConnect(t, e, e.authToken)

	created := decodeMCPField[formJSON](t, mcpCall(t, e, session, "track_create_form", mcpFormArgs(e, map[string]any{
		"title": "Release decisions", "sections": mcpFormSections(),
	})), "form")
	if !strings.HasPrefix(created.WaitURL, "/api/v1/form-waits/") {
		t.Fatalf("wait_url = %q", created.WaitURL)
	}
	if got := e.mustGetFormWait(t, created.WaitURL, ""); got.code != http.StatusOK || got.status != model.FormStatusOpen {
		t.Fatalf("wait URL from MCP: %+v", got)
	}

	// No other form tool shows the key again.
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"track_get_form", map[string]any{"form": created.Ref}},
		{"track_get_form", map[string]any{"form": created.Ref, "include_definition": true}},
		{"track_list_forms", nil},
		{"track_cancel_form", map[string]any{"form": created.Ref}},
	} {
		raw, err := json.Marshal(mcpCall(t, e, session, call.tool, mcpFormArgs(e, call.args)))
		if err != nil {
			t.Fatalf("marshal %s: %v", call.tool, err)
		}
		if strings.Contains(string(raw), "wait_url") || strings.Contains(string(raw), "wait_key") {
			t.Fatalf("%s %v shows the wait key: %s", call.tool, call.args, raw)
		}
	}
	if got := e.mustGetFormWait(t, created.WaitURL, "?wait=5"); got.status != model.FormStatusCancelled {
		t.Fatalf("after cancel over MCP: %+v", got)
	}

	// The tools say how to wait, so an agent does it unprompted.
	tools, err := session.ListTools(e.ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools.Tools {
		switch tool.Name {
		case "track_create_form":
			if !strings.Contains(tool.Description, "wait_url") || !strings.Contains(tool.Description, "wait_seconds") {
				t.Fatalf("track_create_form doesn't say how to wait: %s", tool.Description)
			}
		case "track_get_form":
			schema, _ := json.Marshal(tool.InputSchema)
			if !strings.Contains(tool.Description, "wait_seconds") || !strings.Contains(string(schema), "wait_seconds") {
				t.Fatalf("track_get_form doesn't offer wait_seconds: %s / %s", tool.Description, schema)
			}
		}
	}
}

func TestMCPGetFormWaitsForTheAnswers(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 10 * time.Second})
	session := mcpConnect(t, e, e.authToken)
	created := e.mustCreateForm(t, nil)

	waiting := goMCPGetFormWaiting(e, session, created.Ref, 10)
	time.Sleep(150 * time.Millisecond)
	select {
	case early := <-waiting:
		t.Fatalf("track_get_form returned while the form was still open: %+v", early)
	default:
	}
	e.mustSubmitForm(t, e.authToken, created.Ref)
	got := mustReceiveMCPFormWait(t, waiting)
	if got.code != "" || got.form.Status != model.FormStatusSubmitted || got.elapsed < 150*time.Millisecond || got.elapsed > 8*time.Second {
		t.Fatalf("after submit: %+v", got)
	}
	// It returns the form as it now is: the answers come with it.
	rotated := false
	for _, response := range got.form.Responses {
		if response.ID == "rotated" && response.Checked != nil && *response.Checked {
			rotated = true
		}
	}
	if !rotated {
		t.Fatalf("responses after the wait = %+v", got.form.Responses)
	}

	// Nothing to wait for: a form that is no longer open returns at once.
	again, code, err := mcpGetFormWaiting(e, session, created.Ref, 10)
	if err != nil || code != "" || again.Status != model.FormStatusSubmitted {
		t.Fatalf("second call = %+v, %q, %v", again, code, err)
	}
	if _, code, err := mcpGetFormWaiting(e, session, created.Ref, -1); err != nil || code != "validation_error" {
		t.Fatalf("wait_seconds -1 = %q, %v; want validation_error", code, err)
	}
	if _, code, err := mcpGetFormWaiting(e, session, "form-99", 10); err != nil || code != "not_found" {
		t.Fatalf("missing form = %q, %v; want not_found", code, err)
	}
}

func TestMCPGetFormGivesUpWhenItsTimeRunsOut(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 300 * time.Millisecond})
	session := mcpConnect(t, e, e.authToken)
	created := e.mustCreateForm(t, nil)

	started := time.Now()
	got, code, err := mcpGetFormWaiting(e, session, created.Ref, 50)
	if err != nil || code != "" || got.Status != model.FormStatusOpen {
		t.Fatalf("timed out wait = %+v, %q, %v", got, code, err)
	}
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond || elapsed > 8*time.Second {
		t.Fatalf("timed out wait took %v, want about 300ms", elapsed)
	}
}

func TestMCPGetFormWaitChecksMembershipEveryTime(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 10 * time.Second})
	member, memberToken := e.mustProjectMemberToken(t, "mcp-wait-member")
	session := mcpConnect(t, e, memberToken)
	created := e.mustCreateForm(t, nil)

	waiting := goMCPGetFormWaiting(e, session, created.Ref, 10)
	time.Sleep(50 * time.Millisecond)
	if err := e.store.RevokeProjectAccess(e.ctx, e.projectID, member.ID); err != nil {
		t.Fatalf("RevokeProjectAccess: %v", err)
	}
	got := mustReceiveMCPFormWait(t, waiting)
	if got.code == "" || got.form.Ref != "" || got.elapsed > 8*time.Second {
		t.Fatalf("wait by someone who stopped being a member: %+v", got)
	}
}

func TestMCPGetFormWaitSharesTheFormsPlaces(t *testing.T) {
	t.Parallel()
	e := formWaitEnv(t, server.FormWaitOptions{Max: 10 * time.Second, PerForm: 1})
	session := mcpConnect(t, e, e.authToken)
	created := e.mustCreateForm(t, nil)

	// A wait URL and track_get_form wait on the same form, so they share its
	// one place: whichever came second is turned away.
	byURL := e.goFormWait(created.WaitURL, "?wait=10")
	time.Sleep(100 * time.Millisecond)
	byTool := goMCPGetFormWaiting(e, session, created.Ref, 1)
	time.Sleep(100 * time.Millisecond)
	if code, raw := e.do(t, http.MethodPost, e.formPath(created.Ref)+"/cancel", nil); code != http.StatusOK {
		t.Fatalf("cancel code = %d body = %s", code, raw)
	}
	url, tool := mustReceiveFormWait(t, byURL), mustReceiveMCPFormWait(t, byTool)
	urlRefused := url.code == http.StatusTooManyRequests
	toolRefused := tool.code == "rate_limited"
	if urlRefused == toolRefused {
		t.Fatalf("want exactly one wait refused: url = %+v, tool = %+v", url, tool)
	}
	if !urlRefused && url.code != http.StatusOK {
		t.Fatalf("wait URL = %+v", url)
	}
	if !toolRefused && tool.code != "" {
		t.Fatalf("track_get_form = %+v", tool)
	}
}
