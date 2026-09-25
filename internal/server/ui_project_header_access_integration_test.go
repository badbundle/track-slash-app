package server_test

import (
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
)

// The Context tab once built its project header by hand and dropped every
// permission-dependent action, so it now shares the header rules with the
// other project views. Compare it with All for each kind of viewer.
func TestUIProjectContextHeaderMatchesOtherProjectViews(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	views := []string{"/all", "/context"}

	ownerActions := []string{
		`aria-label="New issue"`,
		`aria-label="Edit project name"`,
		`href="` + e.projectPath() + `/members"`,
		"Delete project",
		`aria-label="Favorite project"`,
	}
	for _, view := range views {
		body := e.uiGet(t, e.projectPath()+view, e.authToken)
		for _, want := range ownerActions {
			if !strings.Contains(body, want) {
				t.Fatalf("owner %s header missing %q: %s", view, want, body)
			}
		}
	}

	if _, err := e.store.UpdateProjectAccessSettings(e.ctx, e.projectID, model.ProjectAccessSettings{IsPublic: true}); err != nil {
		t.Fatalf("UpdateProjectAccessSettings: %v", err)
	}
	for _, view := range views {
		body := e.uiGet(t, e.projectPath()+view, "")
		for _, notWant := range ownerActions {
			if strings.Contains(body, notWant) {
				t.Fatalf("anonymous %s header rendered %q: %s", view, notWant, body)
			}
		}
		if !strings.Contains(body, `href="/`+e.ownerUsername+`/projects"`) {
			t.Fatalf("anonymous %s header missing owner crumb: %s", view, body)
		}
	}
}
