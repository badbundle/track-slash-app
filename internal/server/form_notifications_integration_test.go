package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
)

func formChangelogEntries(entries []model.ProjectChangelogEntry) []model.ProjectChangelogEntry {
	var out []model.ProjectChangelogEntry
	for _, entry := range entries {
		if entry.Entity == "form" {
			out = append(out, entry)
		}
	}
	return out
}

// A form's changelog entries belong to the project's members: the REST list,
// the MCP tool and the changelog page show them to a member and never to a
// public viewer.
func TestFormChangelogEntriesAreForMembersOnly(t *testing.T) {
	t.Parallel()
	e := newMCPHTTPEnv(t, nil)
	if _, err := e.store.UpdateProjectAccessMode(e.ctx, e.projectID, model.ProjectAccessPublicIssues); err != nil {
		t.Fatalf("UpdateProjectAccessMode: %v", err)
	}
	_, outsiderToken := e.mustUserToken(t, "form-changelog-outsider")
	form := e.mustUIForm(t, e.adminID, nil)
	changelogPath := e.projectPath() + "/changelog?limit=200"

	code, body := e.do(t, http.MethodGet, changelogPath, nil)
	if code != http.StatusOK {
		t.Fatalf("member changelog code = %d body = %s", code, body)
	}
	entries := formChangelogEntries(decodePage[model.ProjectChangelogEntry](t, body).Items)
	if len(entries) != 1 || entries[0].Summary != "Asked form "+form.Title || entries[0].TargetRef != form.Ref ||
		entries[0].EntityID != form.ID || !entries[0].MembersOnly || entries[0].IssueID != nil {
		t.Fatalf("member REST form entries = %+v", entries)
	}
	for name, do := range map[string]func() (int, []byte){
		"outsider":  func() (int, []byte) { return e.doWithToken(t, outsiderToken, http.MethodGet, changelogPath, nil) },
		"anonymous": func() (int, []byte) { return e.doUnauth(t, http.MethodGet, changelogPath, nil) },
	} {
		code, body := do()
		if code != http.StatusOK {
			t.Fatalf("%s changelog code = %d body = %s", name, code, body)
		}
		if got := formChangelogEntries(decodePage[model.ProjectChangelogEntry](t, body).Items); len(got) != 0 {
			t.Fatalf("%s REST changelog shows form entries: %+v", name, got)
		}
	}

	args := map[string]any{"owner": e.ownerUsername, "key": e.projKey, "limit": 200}
	owner := mcpConnect(t, e, e.authToken)
	outsider := mcpConnect(t, e, outsiderToken)
	if got := formChangelogEntries(decodeMCPField[[]model.ProjectChangelogEntry](t, mcpCall(t, e, owner, "track_list_project_changelog", args), "items")); len(got) != 1 {
		t.Fatalf("member MCP form entries = %+v", got)
	}
	if got := formChangelogEntries(decodeMCPField[[]model.ProjectChangelogEntry](t, mcpCall(t, e, outsider, "track_list_project_changelog", args), "items")); len(got) != 0 {
		t.Fatalf("outsider MCP changelog shows form entries: %+v", got)
	}

	page := e.uiGet(t, e.uiProjectViewPathForTest("changelog"), e.authToken)
	requireContains(t, "member changelog page", page,
		"Asked form "+form.Title, `data-lucide="clipboard-list"`,
		`href="/`+e.ownerUsername+`/projects/`+e.projKey+`/forms/`+form.Ref+`"`)
	if body := e.uiGet(t, e.uiProjectViewPathForTest("changelog"), outsiderToken); strings.Contains(body, "Asked form") {
		t.Fatalf("outsider changelog page shows a form entry: %s", body)
	}
}

// GET /inbox/count is the sidebar's periodic refresh: just the Inbox link,
// in place, for the signed-in user.
func TestUIInboxCountRefreshesTheSidebarLink(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	member, token := e.mustProjectMemberToken(t, "ui-inbox-count")

	body := e.uiGet(t, "/inbox/count", token)
	if !strings.HasPrefix(strings.TrimSpace(body), `<a id="sidebar-inbox"`) || strings.Contains(body, "hx-swap-oob") ||
		!strings.Contains(body, `aria-label="Inbox"`) || strings.Contains(body, "data-sidebar-inbox-count") || strings.Contains(body, "<html") {
		t.Fatalf("empty count = %s", body)
	}

	e.mustUIForm(t, member.ID, nil)
	body = e.uiGet(t, "/inbox/count", token)
	requireContains(t, "count with a form", body, `<a id="sidebar-inbox"`, `aria-label="Inbox, 1 waiting"`, "data-sidebar-inbox-count")
	if strings.Contains(body, "hx-swap-oob") || strings.Contains(body, "<html") {
		t.Fatalf("count is out of band or a full page: %s", body)
	}
	// Someone else's forms are not counted.
	_, otherToken := e.mustProjectMemberToken(t, "ui-inbox-count-other")
	if other := e.uiGet(t, "/inbox/count", otherToken); !strings.Contains(other, `aria-label="Inbox"`) {
		t.Fatalf("another member's count = %s", other)
	}

	res := e.uiDoNoRedirect(t, http.MethodGet, "/inbox/count", "", nil)
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK || strings.Contains(readBody(t, res), "sidebar-inbox") {
		t.Fatalf("signed-out count code = %d", res.StatusCode)
	}
}
