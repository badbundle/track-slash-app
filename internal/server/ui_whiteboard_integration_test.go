package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
)

// uiPostWhiteboard submits a whiteboard form the way htmx does from the page
// at currentPath.
func (e *httpEnv) uiPostWhiteboard(t *testing.T, path, currentPath, token string, form url.Values) (*http.Response, string) {
	t.Helper()
	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, path, token, strings.NewReader(form.Encode()), map[string]string{
		"HX-Request":     "true",
		"HX-Current-URL": e.ts.URL + currentPath,
	})
	defer res.Body.Close()
	return res, readBody(t, res)
}

func TestUIWhiteboardWriterLifecycle(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustProjectMemberToken(t, "ui-whiteboard-writer")
	whiteboard := e.whiteboardPath()

	body := e.uiGet(t, whiteboard, token)
	assertPopulatedCSRFTokens(t, whiteboard, body)
	for _, want := range []string{
		"No whiteboard pages yet", ">New page</a>", `aria-label="New whiteboard page"`,
		// The project header keeps every action a writer has on other tabs.
		`aria-label="New issue"`, `aria-label="Edit project name"`, `aria-label="Favorite project"`,
		`aria-current="page" class="hidden lg:inline-flex`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty whiteboard missing %q: %s", want, body)
		}
	}

	body = e.uiGet(t, whiteboard+"/new", token)
	assertPopulatedCSRFTokens(t, whiteboard+"/new", body)
	for _, want := range []string{">New page</h2>", "Create page", `name="title"`, `name="body"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("new page form missing %q: %s", want, body)
		}
	}

	res, body := e.uiPostWhiteboard(t, whiteboard, whiteboard+"/new", token, url.Values{"title": {"  "}, "body": {"kept body"}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "title required, max 200 chars") || !strings.Contains(body, ">kept body</textarea>") {
		t.Fatalf("invalid create code = %d body = %s", res.StatusCode, body)
	}
	if strings.Contains(body, "<html") {
		t.Fatalf("htmx create answered with a whole document: %s", body)
	}
	res, body = e.uiPostWhiteboard(t, whiteboard, whiteboard+"/new", token, url.Values{"title": {"Launch ideas"}, "body": {strings.Repeat("b", 100001)}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "body max 100000 chars") {
		t.Fatalf("oversized create code = %d body = %s", res.StatusCode, body)
	}

	res, body = e.uiPostWhiteboard(t, whiteboard, whiteboard+"/new", token, url.Values{"title": {"Launch ideas"}, "body": {"Ship **soon**.\n\n![board](https://example.com/board.png)"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("create code = %d body = %s", res.StatusCode, body)
	}
	if got := res.Header.Get("HX-Replace-Url"); got != whiteboard+"/whiteboard-1" {
		t.Fatalf("create HX-Replace-Url = %q", got)
	}
	for _, want := range []string{"Launch ideas", "<strong>soon</strong>", `rel="noreferrer" referrerpolicy="no-referrer">board</a>`, `aria-label="Edit whiteboard page"`, `aria-label="Delete whiteboard page"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("created page missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `<img src="https://example.com`) {
		t.Fatalf("external image rendered inline: %s", body)
	}
	for _, notWant := range []string{"Linked issues", `aria-label="Link issue"`, `aria-label="Manage linked issues"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("whiteboard rendered issue-linking UI %q: %s", notWant, body)
		}
	}

	res, body = e.uiPostWhiteboard(t, whiteboard, whiteboard+"/new", token, url.Values{"title": {"Retro scratchpad"}})
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Replace-Url") != whiteboard+"/whiteboard-2" || !strings.Contains(body, "No content yet.") {
		t.Fatalf("create second code = %d replace = %q body = %s", res.StatusCode, res.Header.Get("HX-Replace-Url"), body)
	}

	// The tab opens on the most recently updated page; each page has its own URL.
	body = e.uiGet(t, whiteboard, token)
	requireMarkupOrder(t, body, ">Retro scratchpad</span>", ">Launch ideas</span>")
	requireMarkupOrder(t, body, `aria-current="page" class="flex min-w-0`, ">Retro scratchpad</span>")
	body = e.uiGet(t, whiteboard+"/whiteboard-1", token)
	if !strings.Contains(body, "<strong>soon</strong>") || !strings.Contains(body, "<html") {
		t.Fatalf("page URL should render the whole document for page 1: %s", body)
	}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodGet, whiteboard+"/whiteboard-1/panel", token, nil, map[string]string{"HX-Request": "true"})
	body = readBody(t, res)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || strings.Contains(body, "<html") || !strings.Contains(body, `id="project-panel-tab-content"`) || !strings.Contains(body, "<strong>soon</strong>") {
		t.Fatalf("page panel code = %d body = %s", res.StatusCode, body)
	}

	body = e.uiGet(t, whiteboard+"/whiteboard-1/edit", token)
	assertPopulatedCSRFTokens(t, "edit", body)
	for _, want := range []string{`value="Launch ideas"`, "Ship **soon**.", ">Save</button>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("edit form missing %q: %s", want, body)
		}
	}
	res, body = e.uiPostWhiteboard(t, whiteboard+"/whiteboard-1", whiteboard+"/whiteboard-1/edit", token, url.Values{"title": {""}, "body": {"still here"}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "title required, max 200 chars") || !strings.Contains(body, ">still here</textarea>") {
		t.Fatalf("invalid update code = %d body = %s", res.StatusCode, body)
	}
	res, body = e.uiPostWhiteboard(t, whiteboard+"/whiteboard-1", whiteboard+"/whiteboard-1/edit", token, url.Values{"title": {"Launch plan"}, "body": {"Ship _now_."}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<em>now</em>") || !strings.Contains(body, ">Launch plan</h2>") {
		t.Fatalf("update code = %d body = %s", res.StatusCode, body)
	}
	if got := res.Header.Get("HX-Replace-Url"); got != whiteboard+"/whiteboard-1" {
		t.Fatalf("update HX-Replace-Url = %q", got)
	}
	requireMarkupOrder(t, body, ">Launch plan</span>", ">Retro scratchpad</span>")

	res, body = e.uiPostWhiteboard(t, whiteboard+"/whiteboard-1/delete", whiteboard+"/whiteboard-1", token, url.Values{})
	if res.StatusCode != http.StatusOK || res.Header.Get("HX-Replace-Url") != whiteboard {
		t.Fatalf("delete code = %d replace = %q body = %s", res.StatusCode, res.Header.Get("HX-Replace-Url"), body)
	}
	if strings.Contains(body, "Launch plan") || !strings.Contains(body, ">Retro scratchpad</h2>") {
		t.Fatalf("after delete the remaining page should be selected: %s", body)
	}
	if _, err := e.store.GetWhiteboardPageByProjectNumber(e.ctx, e.projectID, 1); err == nil {
		t.Fatal("deleted page still readable")
	}

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, whiteboard + "/whiteboard-1", http.StatusNotFound},
		{http.MethodGet, whiteboard + "/nope", http.StatusBadRequest},
		{http.MethodGet, whiteboard + "/whiteboard-1/edit", http.StatusNotFound},
		{http.MethodPost, whiteboard + "/whiteboard-1", http.StatusNotFound},
		{http.MethodPost, whiteboard + "/whiteboard-1/delete", http.StatusNotFound},
		{http.MethodGet, "/" + e.ownerUsername + "/projects/NOPE/whiteboard/whiteboard-2", http.StatusNotFound},
	} {
		res := e.uiDoNoRedirect(t, tc.method, tc.path, token, strings.NewReader(url.Values{"title": {"x"}}.Encode()))
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Fatalf("%s %s code = %d, want %d", tc.method, tc.path, res.StatusCode, tc.want)
		}
	}
}

func TestUIWhiteboardReadersAndOutsiders(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	page := e.mustWhiteboardPage(t, "Shared notes", "Readable by *members*.")
	readonly, readonlyToken := e.mustUserToken(t, "ui-whiteboard-readonly")
	if _, err := e.store.SetProjectMemberRole(e.ctx, e.projectID, readonly.ID, model.ProjectMemberRoleReadonly); err != nil {
		t.Fatalf("SetProjectMemberRole readonly: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "ui-whiteboard-outsider")
	pagePath := e.whiteboardPagePath(page)

	body := e.uiGet(t, e.whiteboardPath(), readonlyToken)
	if !strings.Contains(body, "<em>members</em>") {
		t.Fatalf("readonly member should read the page: %s", body)
	}
	for _, notWant := range []string{`aria-label="New whiteboard page"`, `aria-label="Edit whiteboard page"`, `aria-label="Delete whiteboard page"`, `aria-label="Edit project name"`} {
		if strings.Contains(body, notWant) {
			t.Fatalf("readonly member saw write control %q: %s", notWant, body)
		}
	}
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, e.whiteboardPath() + "/new"},
		{http.MethodGet, pagePath + "/edit"},
		{http.MethodPost, e.whiteboardPath()},
		{http.MethodPost, pagePath},
		{http.MethodPost, pagePath + "/delete"},
	} {
		res := e.uiDoNoRedirect(t, tc.method, tc.path, readonlyToken, strings.NewReader(url.Values{"title": {"Denied"}}.Encode()))
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("readonly %s %s code = %d, want 403", tc.method, tc.path, res.StatusCode)
		}
	}

	// Outsiders of a private project cannot see the whiteboard or probe page refs.
	for _, path := range []string{e.whiteboardPath(), pagePath, e.whiteboardPath() + "/whiteboard-999"} {
		res := e.uiDoNoRedirect(t, http.MethodGet, path, outsiderToken, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("private outsider GET %s code = %d, want 403", path, res.StatusCode)
		}
		res = e.uiDoNoRedirect(t, http.MethodGet, path, "", nil)
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
			t.Fatalf("private anonymous GET %s code = %d location = %q", path, res.StatusCode, res.Header.Get("Location"))
		}
	}

	e.makeProjectPublic(t)
	for _, path := range []string{e.whiteboardPath(), pagePath} {
		res := e.uiDoNoRedirect(t, http.MethodGet, path, "", nil)
		body := readBody(t, res)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "<em>members</em>") {
			t.Fatalf("public anonymous GET %s code = %d body = %s", path, res.StatusCode, body)
		}
		for _, notWant := range []string{`aria-label="New whiteboard page"`, `aria-label="Edit whiteboard page"`, `aria-label="Delete whiteboard page"`, `aria-label="Favorite project"`, `aria-label="Unfavorite project"`, `aria-label="Edit project name"`} {
			if strings.Contains(body, notWant) {
				t.Fatalf("public anonymous reader saw %q on %s: %s", notWant, path, body)
			}
		}
	}
	body = e.uiGet(t, pagePath, outsiderToken)
	if strings.Contains(body, `aria-label="Edit whiteboard page"`) || !strings.Contains(body, "<em>members</em>") {
		t.Fatalf("public signed-in outsider view: %s", body)
	}
	res := e.uiDoNoRedirect(t, http.MethodPost, pagePath+"/delete", outsiderToken, strings.NewReader(""))
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("public outsider delete code = %d, want 403", res.StatusCode)
	}
	if _, err := e.store.GetWhiteboardPageByProjectNumber(e.ctx, e.projectID, page.Number); err != nil {
		t.Fatalf("page after denied UI writes: %v", err)
	}
}
