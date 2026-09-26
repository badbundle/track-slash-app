package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/realtime"
	"github.com/bradleymackey/track-slash/internal/server"
	"github.com/bradleymackey/track-slash/internal/store"
)

func (e *httpEnv) whiteboardPath() string {
	return e.projectPath() + "/whiteboard"
}

func (e *httpEnv) whiteboardPagePath(page model.WhiteboardPage) string {
	return e.whiteboardPath() + "/" + page.Ref
}

func (e *httpEnv) mustWhiteboardPage(t *testing.T, title, body string) model.WhiteboardPage {
	t.Helper()
	page, err := e.store.CreateWhiteboardPage(e.ctx, store.CreateWhiteboardPageParams{
		ProjectID:   e.projectID,
		Title:       title,
		Body:        body,
		CreatedByID: e.adminID,
	})
	if err != nil {
		t.Fatalf("CreateWhiteboardPage %q: %v", title, err)
	}
	return page
}

func (e *httpEnv) makeProjectPublic(t *testing.T) {
	t.Helper()
	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings: %v", err)
	}
}

func TestHTTPWhiteboardPageCRUD(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)

	code, body := e.do(t, http.MethodPost, e.whiteboardPath(), map[string]any{"title": "  Ideas  "})
	if code != http.StatusCreated {
		t.Fatalf("create code = %d body = %s", code, body)
	}
	first := decode[model.WhiteboardPage](t, body)
	if first.Ref != "whiteboard-1" || first.Title != "Ideas" || first.Body != "" || first.ProjectID != e.projectID || first.CreatedByID != e.adminID {
		t.Fatalf("created page = %+v", first)
	}
	code, body = e.do(t, http.MethodPost, e.whiteboardPath(), map[string]any{"title": "Scratch", "body": "# Notes\n\nSome *thoughts*."})
	if code != http.StatusCreated {
		t.Fatalf("create second code = %d body = %s", code, body)
	}
	second := decode[model.WhiteboardPage](t, body)
	if second.Ref != "whiteboard-2" || second.Body != "# Notes\n\nSome *thoughts*." {
		t.Fatalf("second page = %+v", second)
	}

	code, body = e.do(t, http.MethodGet, e.whiteboardPath()+"?limit=1", nil)
	if code != http.StatusOK {
		t.Fatalf("list code = %d body = %s", code, body)
	}
	page := decodePage[model.WhiteboardPageSummary](t, body)
	if len(page.Items) != 1 || page.Items[0].Ref != second.Ref || page.NextCursor == nil {
		t.Fatalf("first list page = %+v, want the newest page and a cursor", page)
	}
	if strings.Contains(string(body), `"body"`) {
		t.Fatalf("list should omit page bodies: %s", body)
	}
	code, body = e.do(t, http.MethodGet, e.whiteboardPath()+"?limit=1&cursor="+*page.NextCursor, nil)
	if code != http.StatusOK {
		t.Fatalf("list page 2 code = %d body = %s", code, body)
	}
	page = decodePage[model.WhiteboardPageSummary](t, body)
	if len(page.Items) != 1 || page.Items[0].Ref != first.Ref || page.NextCursor != nil {
		t.Fatalf("second list page = %+v, want the older page and no cursor", page)
	}

	code, body = e.do(t, http.MethodGet, e.whiteboardPagePath(second), nil)
	if code != http.StatusOK || decode[model.WhiteboardPage](t, body).Body != second.Body {
		t.Fatalf("get code = %d body = %s", code, body)
	}

	code, body = e.do(t, http.MethodPatch, e.whiteboardPagePath(first), map[string]any{"title": "Big ideas", "body": "Now with a body."})
	if code != http.StatusOK {
		t.Fatalf("patch code = %d body = %s", code, body)
	}
	updated := decode[model.WhiteboardPage](t, body)
	if updated.Title != "Big ideas" || updated.Body != "Now with a body." || !updated.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("patched page = %+v", updated)
	}
	code, body = e.do(t, http.MethodPatch, e.whiteboardPagePath(first), map[string]any{"body": ""})
	if code != http.StatusOK || decode[model.WhiteboardPage](t, body).Body != "" {
		t.Fatalf("clear body code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPatch, e.whiteboardPagePath(first), map[string]any{})
	if code != http.StatusOK || decode[model.WhiteboardPage](t, body).Title != "Big ideas" {
		t.Fatalf("empty patch code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodGet, e.whiteboardPath(), nil)
	if items := decodePage[model.WhiteboardPageSummary](t, body).Items; code != http.StatusOK || len(items) != 2 || items[0].Ref != first.Ref {
		t.Fatalf("list after edit code = %d body = %s, want the edited page first", code, body)
	}

	code, body = e.do(t, http.MethodDelete, e.whiteboardPagePath(first), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete code = %d body = %s", code, body)
	}
	for _, tc := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPatch, map[string]any{"title": "Back again"}},
		{http.MethodDelete, nil},
	} {
		if code, body = e.do(t, tc.method, e.whiteboardPagePath(first), tc.body); code != http.StatusNotFound {
			t.Fatalf("%s deleted page code = %d body = %s", tc.method, code, body)
		}
	}
	code, body = e.do(t, http.MethodGet, e.whiteboardPath(), nil)
	if items := decodePage[model.WhiteboardPageSummary](t, body).Items; code != http.StatusOK || len(items) != 1 || items[0].Ref != second.Ref {
		t.Fatalf("list after delete code = %d body = %s", code, body)
	}
	code, body = e.do(t, http.MethodPost, e.whiteboardPath(), map[string]any{"title": "Third"})
	if code != http.StatusCreated || decode[model.WhiteboardPage](t, body).Ref != "whiteboard-3" {
		t.Fatalf("create after delete code = %d body = %s, want whiteboard-3", code, body)
	}

	entries, _, err := e.store.ListProjectChangelog(e.ctx, store.ListProjectChangelogParams{ProjectID: e.projectID, Limit: 20})
	if err != nil {
		t.Fatalf("ListProjectChangelog: %v", err)
	}
	var whiteboardEntries int
	for _, entry := range entries {
		if entry.Entity == "whiteboard_page" {
			whiteboardEntries++
			if entry.Actor == nil || entry.Actor.ID != e.adminID {
				t.Fatalf("changelog entry actor = %+v, want the API caller", entry.Actor)
			}
		}
	}
	// Three creates, two edits (the empty patch changes nothing), one delete.
	if whiteboardEntries != 6 {
		t.Fatalf("whiteboard changelog entries = %d, want 6", whiteboardEntries)
	}
}

func TestHTTPWhiteboardPageValidation(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	page := e.mustWhiteboardPage(t, "Valid", "")
	longTitle := strings.Repeat("t", 201)
	longBody := strings.Repeat("b", 100001)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		{name: "create empty body", method: http.MethodPost, path: e.whiteboardPath(), body: nil, want: http.StatusBadRequest},
		{name: "create unknown field", method: http.MethodPost, path: e.whiteboardPath(), body: map[string]any{"title": "x", "issue": "KEY-1"}, want: http.StatusBadRequest},
		{name: "create blank title", method: http.MethodPost, path: e.whiteboardPath(), body: map[string]any{"title": "   "}, want: http.StatusBadRequest},
		{name: "create long title", method: http.MethodPost, path: e.whiteboardPath(), body: map[string]any{"title": longTitle}, want: http.StatusBadRequest},
		{name: "create long body", method: http.MethodPost, path: e.whiteboardPath(), body: map[string]any{"title": "x", "body": longBody}, want: http.StatusBadRequest},
		{name: "create unknown project", method: http.MethodPost, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/whiteboard", body: map[string]any{"title": "x"}, want: http.StatusNotFound},
		{name: "list bad limit", method: http.MethodGet, path: e.whiteboardPath() + "?limit=0", want: http.StatusBadRequest},
		{name: "list bad cursor", method: http.MethodGet, path: e.whiteboardPath() + "?cursor=not-a-cursor!", want: http.StatusBadRequest},
		{name: "list unknown project", method: http.MethodGet, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/whiteboard", want: http.StatusNotFound},
		{name: "get malformed ref", method: http.MethodGet, path: e.whiteboardPath() + "/context-1", want: http.StatusBadRequest},
		{name: "get unknown ref", method: http.MethodGet, path: e.whiteboardPath() + "/whiteboard-999", want: http.StatusNotFound},
		{name: "patch unknown project", method: http.MethodPatch, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/whiteboard/whiteboard-1", body: map[string]any{"title": "x"}, want: http.StatusNotFound},
		{name: "patch malformed ref", method: http.MethodPatch, path: e.whiteboardPath() + "/nope", body: map[string]any{"title": "x"}, want: http.StatusBadRequest},
		{name: "patch bad json", method: http.MethodPatch, path: e.whiteboardPagePath(page), body: nil, want: http.StatusBadRequest},
		{name: "patch blank title", method: http.MethodPatch, path: e.whiteboardPagePath(page), body: map[string]any{"title": ""}, want: http.StatusBadRequest},
		{name: "patch long body", method: http.MethodPatch, path: e.whiteboardPagePath(page), body: map[string]any{"body": longBody}, want: http.StatusBadRequest},
		{name: "delete unknown project", method: http.MethodDelete, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/whiteboard/whiteboard-1", want: http.StatusNotFound},
		{name: "delete unknown ref", method: http.MethodDelete, path: e.whiteboardPath() + "/whiteboard-999", want: http.StatusNotFound},
		{name: "get unknown project", method: http.MethodGet, path: "/" + e.ownerUsername + "/projects/" + uniqueProjectKey(t) + "/whiteboard/whiteboard-1", want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := e.do(t, tc.method, tc.path, tc.body)
			if code != tc.want {
				t.Fatalf("code = %d body = %s, want %d", code, body, tc.want)
			}
		})
	}
	got, err := e.store.GetWhiteboardPageByProjectNumber(e.ctx, e.projectID, page.Number)
	if err != nil || got.Title != "Valid" {
		t.Fatalf("page after rejected writes = %+v err=%v", got, err)
	}
}

func TestHTTPWhiteboardPagePermissions(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	page := e.mustWhiteboardPage(t, "Shared notes", "Readable by members.")
	_, writerToken := e.mustProjectMemberToken(t, "whiteboard-writer")
	readonly, readonlyToken := e.mustUserToken(t, "whiteboard-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "whiteboard-outsider")

	// Writers create, edit, and delete.
	code, body := e.doWithToken(t, writerToken, http.MethodPost, e.whiteboardPath(), map[string]any{"title": "Writer page"})
	if code != http.StatusCreated {
		t.Fatalf("writer create code = %d body = %s", code, body)
	}
	writerPage := decode[model.WhiteboardPage](t, body)
	if code, body = e.doWithToken(t, writerToken, http.MethodPatch, e.whiteboardPagePath(writerPage), map[string]any{"body": "Edited"}); code != http.StatusOK {
		t.Fatalf("writer patch code = %d body = %s", code, body)
	}
	if code, body = e.doWithToken(t, writerToken, http.MethodDelete, e.whiteboardPagePath(writerPage), nil); code != http.StatusNoContent {
		t.Fatalf("writer delete code = %d body = %s", code, body)
	}

	// Read-only members read but never write.
	if code, body = e.doWithToken(t, readonlyToken, http.MethodGet, e.whiteboardPath(), nil); code != http.StatusOK {
		t.Fatalf("readonly list code = %d body = %s", code, body)
	}
	if code, body = e.doWithToken(t, readonlyToken, http.MethodGet, e.whiteboardPagePath(page), nil); code != http.StatusOK {
		t.Fatalf("readonly get code = %d body = %s", code, body)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, e.whiteboardPath(), map[string]any{"title": "Denied"}},
		{http.MethodPatch, e.whiteboardPagePath(page), map[string]any{"title": "Denied"}},
		{http.MethodDelete, e.whiteboardPagePath(page), nil},
	} {
		if code, body = e.doWithToken(t, readonlyToken, tc.method, tc.path, tc.body); code != http.StatusForbidden {
			t.Fatalf("readonly %s code = %d body = %s", tc.method, code, body)
		}
	}

	// Outsiders of a private project learn nothing, not even whether a page exists.
	for _, path := range []string{e.whiteboardPath(), e.whiteboardPagePath(page), e.whiteboardPath() + "/whiteboard-999"} {
		if code, body = e.doWithToken(t, outsiderToken, http.MethodGet, path, nil); code != http.StatusForbidden {
			t.Fatalf("private outsider GET %s code = %d body = %s", path, code, body)
		}
		if code, body = e.doUnauth(t, http.MethodGet, path, nil); code != http.StatusUnauthorized {
			t.Fatalf("private anonymous GET %s code = %d body = %s", path, code, body)
		}
	}
	if code, body = e.doWithToken(t, outsiderToken, http.MethodPost, e.whiteboardPath(), map[string]any{"title": "Denied"}); code != http.StatusForbidden {
		t.Fatalf("private outsider create code = %d body = %s", code, body)
	}

	// Public projects are readable by anyone and writable by nobody new.
	e.makeProjectPublic(t)
	if code, body = e.doUnauth(t, http.MethodGet, e.whiteboardPath(), nil); code != http.StatusOK || len(decodePage[model.WhiteboardPageSummary](t, body).Items) != 1 {
		t.Fatalf("public anonymous list code = %d body = %s", code, body)
	}
	if code, body = e.doUnauth(t, http.MethodGet, e.whiteboardPagePath(page), nil); code != http.StatusOK {
		t.Fatalf("public anonymous get code = %d body = %s", code, body)
	}
	if code, body = e.doUnauth(t, http.MethodPost, e.whiteboardPath(), map[string]any{"title": "Denied"}); code != http.StatusUnauthorized {
		t.Fatalf("public anonymous create code = %d body = %s", code, body)
	}
	if code, body = e.doWithToken(t, outsiderToken, http.MethodGet, e.whiteboardPagePath(page), nil); code != http.StatusOK {
		t.Fatalf("public outsider get code = %d body = %s", code, body)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, e.whiteboardPath(), map[string]any{"title": "Denied"}},
		{http.MethodPatch, e.whiteboardPagePath(page), map[string]any{"title": "Denied"}},
		{http.MethodDelete, e.whiteboardPagePath(page), nil},
	} {
		if code, body = e.doWithToken(t, outsiderToken, tc.method, tc.path, tc.body); code != http.StatusForbidden {
			t.Fatalf("public outsider %s code = %d body = %s", tc.method, code, body)
		}
	}
}

func TestWebSocketWhiteboardPageTopicPermission(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	hub := realtime.NewHub()
	ts := httptest.NewServer(server.New(e.store, hub, nil).Router())
	t.Cleanup(ts.Close)
	page := e.mustWhiteboardPage(t, "Live notes", "")
	_, memberToken := e.mustProjectMemberToken(t, "whiteboard-ws-member")
	_, outsiderToken := e.mustUserToken(t, "whiteboard-ws-outsider")

	dial := func(t *testing.T, token string) *websocket.Conn {
		t.Helper()
		ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+apiPath("/ws"), &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
		})
		if err != nil {
			t.Fatalf("websocket dial: %v", err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		return conn
	}
	subscribe := func(t *testing.T, conn *websocket.Conn, topic string) {
		t.Helper()
		msg, err := json.Marshal(map[string]string{"action": "subscribe", "topic": topic})
		if err != nil {
			t.Fatalf("marshal subscribe: %v", err)
		}
		if err := conn.Write(e.ctx, websocket.MessageText, msg); err != nil {
			t.Fatalf("write subscribe: %v", err)
		}
	}
	read := func(t *testing.T, conn *websocket.Conn) map[string]any {
		t.Helper()
		ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
		defer cancel()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read websocket: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		return got
	}

	outsider := dial(t, outsiderToken)
	subscribe(t, outsider, realtime.WhiteboardPageTopic(page.ID))
	if got := read(t, outsider); got["error"] != "forbidden" {
		t.Fatalf("outsider subscribe response = %v, want forbidden", got)
	}

	member := dial(t, memberToken)
	subscribe(t, member, realtime.WhiteboardPageTopic(page.ID))
	// The hub has no acknowledgement, so publish until the subscription lands.
	deadline := time.Now().Add(5 * time.Second)
	received := make(chan map[string]any, 1)
	go func() {
		ctx, cancel := context.WithTimeout(e.ctx, 5*time.Second)
		defer cancel()
		_, data, err := member.Read(ctx)
		if err != nil {
			return
		}
		var got map[string]any
		if json.Unmarshal(data, &got) == nil {
			received <- got
		}
	}()
	for {
		hub.Publish(realtime.Event{Op: realtime.OpUpdate, Entity: realtime.EntityWhiteboardPage, ID: page.ID, ProjectID: &e.projectID, Version: 2})
		select {
		case got := <-received:
			if got["entity"] != "whiteboard_page" || got["id"] != page.ID.String() {
				t.Fatalf("member event = %v, want the whiteboard page event", got)
			}
			return
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("member never received the whiteboard page event")
		}
	}
}
