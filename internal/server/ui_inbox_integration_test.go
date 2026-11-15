package server_test

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

var inboxMoreHref = regexp.MustCompile(`hx-get="(/inbox/[a-z/]*page\?cursor=[^"]+)"`)

func (e *httpEnv) mustCancelFormInStore(t *testing.T, form model.Form) {
	t.Helper()
	if _, err := e.store.CancelForm(e.ctx, form.ID); err != nil {
		t.Fatalf("CancelForm: %v", err)
	}
}

func TestUIInboxPageAndTabs(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-inbox-member")
	_, otherToken := e.mustProjectMemberToken(t, "ui-inbox-other")
	issue := e.mustCreateIssue(t, "Ship 2.3")

	// A second project, so the Inbox spans projects.
	second, err := e.store.CreateProjectForUser(e.ctx, member.ID, "SECND", "Second", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) {
		p.Title = "Pick the channel"
		p.IssueID = &issue.ID
		p.OriginLabel = "Release chat"
	})
	e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) {
		p.Title = "Pick the region"
		p.OriginLabel = ""
		p.OriginURL = ""
	})
	if _, err := e.store.CreateForm(e.ctx, store.CreateFormParams{
		ProjectID: second.ID, Title: "Second project question", Definition: uiFormTestDefinition(), AssigneeID: member.ID, CreatedByID: member.ID,
	}); err != nil {
		t.Fatalf("CreateForm: %v", err)
	}
	cancelled := e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) { p.Title = "Withdrawn question" })
	e.mustCancelFormInStore(t, cancelled)
	e.mustUIForm(t, e.adminID, func(p *store.CreateFormParams) { p.Title = "Admin only question" })

	// To do: a full page, longest-waiting first, with the sidebar count.
	body := e.uiGet(t, "/inbox", token)
	requireContains(t, "inbox page", body,
		"<html", `data-sidebar-view="inbox"`, ">Inbox</h1>", "Forms waiting on your answers.",
		`aria-label="Inbox, 3 waiting"`, "data-sidebar-inbox-dot", "data-sidebar-inbox-count",
		"Pick the channel", "Pick the region", "Second project question", "SECND", e.projKey,
		issue.Identifier, "Release chat", "5 questions · 2 of 3 answered", "Asked ", "data-form-summary-row",
		`href="`+e.projectPath()+`/forms/form-1"`, `hx-get="`+e.projectPath()+`/forms/form-1/panel"`,
		`aria-current="page"`, "To do", "Done",
	)
	requireOmits(t, "inbox page", body, "Withdrawn question", "Admin only question", "data-form-status", "Nothing is waiting on you.")
	if strings.Index(body, "Pick the channel") > strings.Index(body, "Pick the region") || strings.Index(body, "Pick the region") > strings.Index(body, "Second project question") {
		t.Fatal("to do rows are not longest-waiting first")
	}
	// The Inbox link is the active sidebar link, and no other one is.
	sidebarLink := regexp.MustCompile(`<a id="sidebar-inbox"[^>]*>`).FindString(body)
	if !strings.Contains(sidebarLink, `aria-current="page"`) || !strings.Contains(sidebarLink, `href="/inbox"`) || !strings.Contains(sidebarLink, `hx-get="/inbox/panel"`) {
		t.Fatalf("sidebar link = %s", sidebarLink)
	}
	if meLink := regexp.MustCompile(`<a [^>]*data-sidebar-view="me"[^>]*>`).FindString(body); strings.Contains(meLink, "aria-current") {
		t.Fatalf("Me link is active on the inbox: %s", meLink)
	}

	// The panel is the fragment: no document and no sidebar of its own.
	panel := e.uiGet(t, "/inbox/panel", token)
	requireContains(t, "inbox panel", panel, "Pick the channel", ">Inbox</h1>")
	requireOmits(t, "inbox panel", panel, "<html", `id="sidebar-inbox"`)

	// Done: submitted and cancelled forms, with their status, and no open ones.
	submitted := e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) { p.Title = "Answered question" })
	path := e.uiFormPath(submitted)
	e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}}, false)
	if res, b := e.uiFormPost(t, path+"/submit", token, url.Values{}, false); res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d body = %s", res.StatusCode, b)
	}
	done := e.uiGet(t, "/inbox/done", token)
	requireContains(t, "done tab", done, "<html", `aria-label="Inbox, 3 waiting"`, "Answered question", "Withdrawn question", `data-form-status="submitted"`, `data-form-status="cancelled"`)
	requireOmits(t, "done tab", done, "Pick the channel", "Admin only question", "No answered forms yet.")
	if strings.Index(done, "Answered question") > strings.Index(done, "Withdrawn question") {
		t.Fatal("done rows are not most recently changed first")
	}
	donePanel := e.uiGet(t, "/inbox/done/panel", token)
	requireContains(t, "done panel", donePanel, "Answered question")
	requireOmits(t, "done panel", donePanel, "<html")

	// Another person's forms never appear, and theirs have their own count.
	otherBody := e.uiGet(t, "/inbox", otherToken)
	requireContains(t, "other's inbox", otherBody, "Nothing is waiting on you.", `aria-label="Inbox"`)
	requireOmits(t, "other's inbox", otherBody, "Pick the channel", "Answered question", "data-sidebar-inbox-count", "data-sidebar-inbox-dot", " waiting\"")
	otherDone := e.uiGet(t, "/inbox/done", otherToken)
	requireContains(t, "other's done", otherDone, "No answered forms yet.")
	requireOmits(t, "other's done", otherDone, "Answered question", "Withdrawn question")

	// The count shows on every page, and is absent at zero.
	if home := e.uiGet(t, "/me", token); !strings.Contains(home, `aria-label="Inbox, 3 waiting"`) {
		t.Fatalf("/me sidebar lacks the count")
	}
	if home := e.uiGet(t, "/me", otherToken); !strings.Contains(home, `aria-label="Inbox"`) || strings.Contains(home, "data-sidebar-inbox-count") {
		t.Fatalf("/me sidebar shows a count at zero")
	}
}

func TestUIInboxDonePages(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-inbox-pager")
	const total = 52
	for i := 1; i <= total; i++ {
		form := e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) { p.Title = fmt.Sprintf("Old question %02d", i) })
		e.mustCancelFormInStore(t, form)
	}
	first := e.uiGet(t, "/inbox/done", token)
	if got := strings.Count(first, "data-form-summary-row"); got != 50 {
		t.Fatalf("first page rows = %d, want 50", got)
	}
	// Most recently changed first: the last one cancelled leads.
	requireContains(t, "first page", first, "Old question 52", "Load more")
	requireOmits(t, "first page", first, "Old question 01<")
	match := inboxMoreHref.FindStringSubmatch(first)
	if match == nil {
		t.Fatalf("no load more link in %s", first)
	}
	next := e.uiGet(t, html.UnescapeString(match[1]), token)
	if got := strings.Count(next, "data-form-summary-row"); got != 2 {
		t.Fatalf("second page rows = %d, want 2", got)
	}
	requireContains(t, "second page", next, "Old question 02", "Old question 01")
	requireOmits(t, "second page", next, "Load more", "<html")

	if res := e.uiDoNoRedirect(t, http.MethodGet, "/inbox/done/page?cursor=nope!", token, nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad cursor code = %d, want 400", res.StatusCode)
	}
}

func TestUIFormChangesRefreshTheSidebarInbox(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-inbox-oob")
	mine := e.mustUIForm(t, member.ID, nil)
	e.mustUIForm(t, member.ID, func(p *store.CreateFormParams) { p.Title = "Another" })
	path := e.uiFormPath(mine)

	oob := func(body string) string {
		t.Helper()
		return regexp.MustCompile(`<a id="sidebar-inbox"[^>]*>`).FindString(body)
	}
	requireOOB := func(what, body, label string) {
		t.Helper()
		link := oob(body)
		if !strings.Contains(link, `hx-swap-oob="true"`) || !strings.Contains(link, `aria-label="`+label+`"`) {
			t.Fatalf("%s: sidebar link = %q, want an out-of-band %q", what, link, label)
		}
	}

	// A full page carries the sidebar once, in place.
	full := e.uiGet(t, path, token)
	if link := oob(full); strings.Contains(link, "hx-swap-oob") || strings.Count(full, `id="sidebar-inbox"`) != 1 {
		t.Fatalf("full page sidebar link = %q", link)
	}

	e.uiFormPost(t, path+"/answers/reason", token, url.Values{"text": {"Because"}}, false)
	res, body := e.uiFormPost(t, path+"/submit", token, url.Values{}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("submit code = %d body = %s", res.StatusCode, body)
	}
	requireOOB("submit", body, "Inbox, 1 waiting")
	if _, body = e.uiFormPost(t, path+"/reopen", token, url.Values{}, false); true {
		requireOOB("reopen", body, "Inbox, 2 waiting")
	}

	// The panel itself refreshes it too, and a cancel changes the canceller's own count.
	htmxGet := func(path, token string) string {
		t.Helper()
		res := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, path, token, nil, map[string]string{"HX-Request": "true"})
		defer res.Body.Close()
		return readBody(t, res)
	}
	requireOOB("panel", htmxGet(path+"/panel", token), "Inbox, 2 waiting")
	adminForm := e.mustUIForm(t, e.adminID, func(p *store.CreateFormParams) { p.Title = "Admin's own" })
	requireOOB("before cancel", htmxGet(e.uiFormPath(adminForm)+"/panel", e.authToken), "Inbox, 1 waiting")
	_, body = e.uiFormPost(t, e.uiFormPath(adminForm)+"/cancel", e.authToken, url.Values{}, false)
	requireOOB("cancel", body, "Inbox")
	if strings.Contains(oob(body), "waiting") {
		t.Fatalf("cancel left a count: %s", oob(body))
	}
}
