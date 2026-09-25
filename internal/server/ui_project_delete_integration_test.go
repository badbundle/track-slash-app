package server_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// mustOwnedProject creates a project owned by a fresh non-admin user, so the
// owner-only delete paths are exercised without site-admin standing in for them.
func (e *httpEnv) mustOwnedProject(t *testing.T, label, name string) (model.User, string, model.Project) {
	t.Helper()
	owner, token := e.mustUserToken(t, label)
	project, err := e.store.CreateProjectForUser(e.ctx, owner.ID, uniqueProjectKey(t), name, "")
	if err != nil {
		t.Fatalf("CreateProjectForUser: %v", err)
	}
	return owner, token, project
}

func uiProjectPathFor(project model.Project) string {
	return "/" + project.OwnerUsername + "/projects/" + project.Key
}

func TestUIProjectDeleteFromActionsMenu(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	owner, ownerToken, project := e.mustOwnedProject(t, "ui-delete-owner", "Deletable Project")
	issue, err := e.store.CreateIssue(e.ctx, store.CreateIssueParams{ProjectID: project.ID, Title: "goes with the project"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	projectPath := uiProjectPathFor(project)
	deletePath := projectPath + "/delete"
	ownerProjects := "/" + owner.Username + "/projects"

	menu := e.uiGet(t, projectPath+"/all", ownerToken)
	if !strings.Contains(menu, `href="`+deletePath+`?view=all"`) || !strings.Contains(menu, "<span>Delete project</span>") {
		t.Fatalf("project actions menu missing delete entry: %s", menu)
	}
	if strings.Contains(menu, `id="project-delete"`) {
		t.Fatalf("delete dialog rendered before it was asked for: %s", menu)
	}

	dialog := e.uiGet(t, deletePath+"?view=all", ownerToken)
	for _, want := range []string{
		`id="project-delete"`,
		`action="` + deletePath + `"`,
		`hx-post="` + deletePath + `"`,
		`name="key"`,
		"Type " + project.Key + " to confirm",
		"This cannot be undone from the app.",
		`hx-get="` + projectPath + `/all/panel"`,
	} {
		if !strings.Contains(dialog, want) {
			t.Fatalf("delete dialog missing %q: %s", want, dialog)
		}
	}
	// Cancelling returns to whichever view the actions menu was opened from,
	// and an unknown view falls back to the default rather than offering a
	// cancel path the project does not have.
	if kept := e.uiGet(t, deletePath+"?view=context", ownerToken); !strings.Contains(kept, `hx-get="`+projectPath+`/context/panel"`) {
		t.Fatalf("delete dialog lost the context view: %s", kept)
	}
	if fallback := e.uiGet(t, deletePath+"?view=nonsense", ownerToken); !strings.Contains(fallback, `hx-get="`+projectPath+`/all/panel"`) {
		t.Fatalf("unknown view did not fall back to the project's landing panel: %s", fallback)
	}

	// htmx gets the panel fragment, not a second whole document.
	fragment := e.uiDoNoRedirectWithHeaders(t, http.MethodGet, deletePath, ownerToken, nil, map[string]string{"HX-Request": "true"})
	defer fragment.Body.Close()
	fragmentBody := readBody(t, fragment)
	if fragment.StatusCode != http.StatusOK || strings.Contains(fragmentBody, "<html") {
		t.Fatalf("htmx dialog code = %d body = %s", fragment.StatusCode, fragmentBody)
	}
	if !strings.Contains(fragmentBody, `id="project-delete"`) {
		t.Fatalf("htmx dialog missing the modal: %s", fragmentBody)
	}

	// A well-formed key that names no project is a 404 on both methods.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		res := e.uiDoNoRedirect(t, method, "/"+owner.Username+"/projects/ZZTOP/delete", ownerToken, strings.NewReader(url.Values{"key": {"ZZTOP"}}.Encode()))
		defer res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("%s unknown project code = %d body = %s", method, res.StatusCode, readBody(t, res))
		}
	}
	malformed := e.uiDoNoRedirect(t, http.MethodPost, deletePath, ownerToken, strings.NewReader("key=%zz"))
	defer malformed.Body.Close()
	if malformed.StatusCode != http.StatusBadRequest {
		t.Fatalf("unreadable form code = %d body = %s", malformed.StatusCode, readBody(t, malformed))
	}

	res := e.uiDoNoRedirect(t, http.MethodPost, deletePath, ownerToken, strings.NewReader(url.Values{
		"view": {"all"},
		"key":  {"WRONGKEY"},
	}.Encode()))
	defer res.Body.Close()
	mismatch := readBody(t, res)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mismatched key code = %d body = %s", res.StatusCode, mismatch)
	}
	if !strings.Contains(mismatch, "Type "+project.Key+" to confirm deletion.") || !strings.Contains(mismatch, `value="WRONGKEY"`) {
		t.Fatalf("mismatched key did not re-open the dialog with the typed value: %s", mismatch)
	}
	if _, err := e.store.GetProject(e.ctx, project.ID); err != nil {
		t.Fatalf("project deleted despite mismatched key: %v", err)
	}

	// The confirmation is case-insensitive; the input is uppercased on screen.
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, deletePath, ownerToken, strings.NewReader(url.Values{
		"view": {"all"},
		"key":  {strings.ToLower(project.Key)},
	}.Encode()), map[string]string{"HX-Request": "true"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("htmx delete code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if got := res.Header.Get("HX-Redirect"); got != ownerProjects {
		t.Fatalf("HX-Redirect = %q, want %q", got, ownerProjects)
	}
	if _, err := e.store.GetProject(e.ctx, project.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after delete = %v, want ErrNotFound", err)
	}
	if _, err := e.store.GetIssue(e.ctx, issue.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetIssue after project delete = %v, want ErrNotFound", err)
	}

	// Without htmx the same form is an ordinary POST that redirects.
	plain, err := e.store.CreateProjectForUser(e.ctx, owner.ID, uniqueProjectKey(t), "Doomed Without HTMX", "")
	if err != nil {
		t.Fatalf("CreateProjectForUser plain: %v", err)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, uiProjectPathFor(plain)+"/delete", ownerToken, strings.NewReader(url.Values{
		"view": {"about"},
		"key":  {plain.Key},
	}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("plain delete code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if got := res.Header.Get("Location"); got != ownerProjects {
		t.Fatalf("Location = %q, want %q", got, ownerProjects)
	}
	if _, err := e.store.GetProject(e.ctx, plain.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after plain delete = %v, want ErrNotFound", err)
	}
}

func TestUIProjectDeleteIsOwnerAndAdminOnly(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, _, project := e.mustOwnedProject(t, "ui-delete-member-owner", "Member Visible Project")
	member, memberToken := e.mustUserToken(t, "ui-delete-member")
	if _, err := e.store.GrantProjectAccess(e.ctx, project.ID, member.ID); err != nil {
		t.Fatalf("GrantProjectAccess: %v", err)
	}
	projectPath := uiProjectPathFor(project)

	menu := e.uiGet(t, projectPath+"/all", memberToken)
	if strings.Contains(menu, "Delete project") || strings.Contains(menu, projectPath+`/delete?`) {
		t.Fatalf("write member sees the delete action: %s", menu)
	}
	res := e.uiDoNoRedirect(t, http.MethodGet, projectPath+"/delete", memberToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member dialog code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, projectPath+"/delete", memberToken, strings.NewReader(url.Values{"key": {project.Key}}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("member delete code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if _, err := e.store.GetProject(e.ctx, project.ID); err != nil {
		t.Fatalf("project deleted by write member: %v", err)
	}

	// A site admin may remove a project owned by somebody else.
	adminMenu := e.uiGet(t, projectPath+"/all", e.authToken)
	if !strings.Contains(adminMenu, "<span>Delete project</span>") {
		t.Fatalf("admin menu missing delete entry: %s", adminMenu)
	}
	res = e.uiDoNoRedirect(t, http.MethodPost, projectPath+"/delete", e.authToken, strings.NewReader(url.Values{"key": {project.Key}}.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin delete code = %d body = %s", res.StatusCode, readBody(t, res))
	}
	if _, err := e.store.GetProject(e.ctx, project.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetProject after admin delete = %v, want ErrNotFound", err)
	}
}
