package server

import (
	"net/http/httptest"
	"testing"
)

func TestUISetHXHistoryURL(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("POST", "/bradley/projects/TRACK/context/context-1", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "http://track.test/bradley/projects/TRACK/context/context-1/edit")
	res := httptest.NewRecorder()
	uiSetHXReplaceURL(res, req, "/bradley/projects/TRACK/context")
	if got := res.Header().Get("HX-Replace-Url"); got != "/bradley/projects/TRACK/context" {
		t.Fatalf("HX-Replace-Url = %q, want project context path", got)
	}

	req = httptest.NewRequest("POST", "/bradley/issues/TRACK-7/description", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "http://track.test/bradley/issues/TRACK-7")
	res = httptest.NewRecorder()
	uiSetHXPushURL(res, req, "/bradley/issues/TRACK-7")
	if got := res.Header().Get("HX-Push-Url"); got != "" {
		t.Fatalf("same-url HX-Push-Url = %q, want empty", got)
	}

	req = httptest.NewRequest("POST", "/bradley/issues/TRACK-7/delete", nil)
	res = httptest.NewRecorder()
	uiSetHXPushURL(res, req, "/bradley/projects/TRACK/deleted")
	if got := res.Header().Get("HX-Push-Url"); got != "" {
		t.Fatalf("non-htmx HX-Push-Url = %q, want empty", got)
	}
}

func TestSafeUINextRootPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: "/"},
		{name: "root", raw: "/", want: "/"},
		{name: "removed work", raw: "/sprint", want: "/"},
		{name: "removed work panel with query", raw: "/sprint/panel?x=1", want: "/"},
		{name: "projects", raw: "/projects", want: "/projects"},
		{name: "projects panel", raw: "/projects/panel", want: "/projects/panel"},
		{name: "new project", raw: "/projects/new", want: "/projects/new"},
		{name: "new project panel with query", raw: "/projects/new/panel?x=1", want: "/projects/new/panel?x=1"},
		{name: "old settings", raw: "/settings?x=1", want: "/settings?x=1"},
		{name: "profile", raw: "/settings/profile", want: "/settings/profile"},
		{name: "login settings", raw: "/settings/login", want: "/settings/login"},
		{name: "notifications with query", raw: "/settings/notifications?x=1", want: "/settings/notifications?x=1"},
		{name: "tokens", raw: "/tokens", want: "/tokens"},
		{name: "settings form action", raw: "/settings/password", want: "/"},
		{name: "new issue", raw: "/issues/new", want: "/issues/new"},
		{name: "new issue panel with query", raw: "/issues/new/panel?project_id=8cc21ed4-2d69-4d43-9f0c-402736e4aa16", want: "/issues/new/panel?project_id=8cc21ed4-2d69-4d43-9f0c-402736e4aa16"},
		{name: "new issue project options with query", raw: "/issues/new/projects?project=track", want: "/issues/new/projects?project=track"},
		{name: "bad root issues", raw: "/issues", want: "/"},
		{name: "issue", raw: "/bradley/issues/TRACK-7", want: "/bradley/issues/TRACK-7"},
		{name: "issue panel with query", raw: "/bradley/issues/TRACK-7/panel?x=1", want: "/bradley/issues/TRACK-7/panel?x=1"},
		{name: "issue title edit with query", raw: "/bradley/issues/TRACK-7/title/edit?x=1", want: "/bradley/issues/TRACK-7/title/edit?x=1"},
		{name: "issue description edit with query", raw: "/bradley/issues/TRACK-7/description/edit?x=1", want: "/bradley/issues/TRACK-7/description/edit?x=1"},
		{name: "issue status edit", raw: "/bradley/issues/TRACK-7/status/edit", want: "/bradley/issues/TRACK-7/status/edit"},
		{name: "issue close reason edit", raw: "/bradley/issues/TRACK-7/close-reason/edit", want: "/bradley/issues/TRACK-7/close-reason/edit"},
		{name: "issue priority edit", raw: "/bradley/issues/TRACK-7/priority/edit", want: "/bradley/issues/TRACK-7/priority/edit"},
		{name: "issue sprint edit", raw: "/bradley/issues/TRACK-7/sprint/edit", want: "/bradley/issues/TRACK-7/sprint/edit"},
		{name: "issue restore", raw: "/bradley/issues/TRACK-7/restore", want: "/bradley/issues/TRACK-7/restore"},
		{name: "issue removed archive action", raw: "/bradley/issues/TRACK-7/archive", want: "/"},
		{name: "issue link add", raw: "/bradley/issues/TRACK-7/links/new?x=1", want: "/bradley/issues/TRACK-7/links/new?x=1"},
		{name: "issue sub-issue add", raw: "/bradley/issues/TRACK-7/sub-issues/new?x=1", want: "/bradley/issues/TRACK-7/sub-issues/new?x=1"},
		{name: "issue context view", raw: "/bradley/issues/TRACK-7/context?x=1", want: "/bradley/issues/TRACK-7/context?x=1"},
		{name: "issue context item", raw: "/bradley/issues/TRACK-7/context/context-1?x=1", want: "/bradley/issues/TRACK-7/context/context-1?x=1"},
		{name: "issue context edit", raw: "/bradley/issues/TRACK-7/context/context-1/edit?x=1", want: "/bradley/issues/TRACK-7/context/context-1/edit?x=1"},
		{name: "issue context add", raw: "/bradley/issues/TRACK-7/context/new?x=1", want: "/bradley/issues/TRACK-7/context/new?x=1"},
		{name: "issue context attach", raw: "/bradley/issues/TRACK-7/context/link?x=1", want: "/bradley/issues/TRACK-7/context/link?x=1"},
		{name: "issue link edit", raw: "/bradley/issues/TRACK-7/links/link-2/edit", want: "/bradley/issues/TRACK-7/links/link-2/edit"},
		{name: "bad issue id", raw: "/bradley/issues/nope", want: "/"},
		{name: "bad issue child", raw: "/bradley/issues/TRACK-7/activity", want: "/"},
		{name: "bad issue nested panel", raw: "/bradley/issues/TRACK-7/panel/extra", want: "/"},
		{name: "bad issue status child", raw: "/bradley/issues/TRACK-7/status/panel", want: "/"},
		{name: "bad issue link ref", raw: "/bradley/issues/TRACK-7/links/nope/edit", want: "/"},
		{name: "bad issue link action", raw: "/bradley/issues/TRACK-7/links/link-2/delete", want: "/"},
		{name: "project", raw: "/bradley/projects/TRACK", want: "/bradley/projects/TRACK"},
		{name: "project about", raw: "/bradley/projects/TRACK/about", want: "/bradley/projects/TRACK/about"},
		{name: "project sprint", raw: "/bradley/projects/TRACK/sprint", want: "/bradley/projects/TRACK/sprint"},
		{name: "project planned", raw: "/bradley/projects/TRACK/planned", want: "/bradley/projects/TRACK/planned"},
		{name: "project all", raw: "/bradley/projects/TRACK/all", want: "/bradley/projects/TRACK/all"},
		{name: "project deleted", raw: "/bradley/projects/TRACK/deleted", want: "/bradley/projects/TRACK/deleted"},
		{name: "project about panel with query", raw: "/bradley/projects/TRACK/about/panel?x=1", want: "/bradley/projects/TRACK/about/panel?x=1"},
		{name: "project planned panel with query", raw: "/bradley/projects/TRACK/planned/panel?x=1", want: "/bradley/projects/TRACK/planned/panel?x=1"},
		{name: "project all panel with query", raw: "/bradley/projects/TRACK/all/panel?x=1", want: "/bradley/projects/TRACK/all/panel?x=1"},
		{name: "project all page with query", raw: "/bradley/projects/TRACK/all/page?cursor=abc", want: "/bradley/projects/TRACK/all/page?cursor=abc"},
		{name: "project backlog panel with query", raw: "/bradley/projects/TRACK/backlog/panel?x=1", want: "/bradley/projects/TRACK/backlog/panel?x=1"},
		{name: "project deleted panel with query", raw: "/bradley/projects/TRACK/deleted/panel?x=1", want: "/bradley/projects/TRACK/deleted/panel?x=1"},
		{name: "project name edit", raw: "/bradley/projects/TRACK/name/edit?view=about", want: "/bradley/projects/TRACK/name/edit?view=about"},
		{name: "project description edit", raw: "/bradley/projects/TRACK/description/edit", want: "/bradley/projects/TRACK/description/edit"},
		{name: "project sprint new", raw: "/bradley/projects/TRACK/sprints/new", want: "/bradley/projects/TRACK/sprints/new"},
		{name: "project sprint edit", raw: "/bradley/projects/TRACK/sprints/sprint-1/edit", want: "/bradley/projects/TRACK/sprints/sprint-1/edit"},
		{name: "project sprint add issue", raw: "/bradley/projects/TRACK/sprints/sprint-1/issues/new", want: "/bradley/projects/TRACK/sprints/sprint-1/issues/new"},
		{name: "stale project sprint remove issue", raw: "/bradley/projects/TRACK/sprints/sprint-1/issues/TRACK-7/delete", want: "/"},
		{name: "project context add", raw: "/bradley/projects/TRACK/context/new?x=1", want: "/bradley/projects/TRACK/context/new?x=1"},
		{name: "project context issue link", raw: "/bradley/projects/TRACK/context/context-1/issues/new?x=1", want: "/bradley/projects/TRACK/context/context-1/issues/new?x=1"},
		{name: "bad project key", raw: "/bradley/projects/bad!/sprint", want: "/"},
		{name: "bad project child", raw: "/bradley/projects/TRACK/issues", want: "/"},
		{name: "bad project panel", raw: "/bradley/projects/TRACK/sprint/card", want: "/"},
		{name: "bad project sprint ref", raw: "/bradley/projects/TRACK/sprints/nope/edit", want: "/"},
		{name: "bad project sprint issue", raw: "/bradley/projects/TRACK/sprints/sprint-1/issues/not-a-ref/delete", want: "/"},
		{name: "api", raw: "/api/v1/projects", want: "/"},
		{name: "legacy app", raw: "/app/sprint", want: "/"},
		{name: "scheme relative", raw: "//evil.example/sprint", want: "/"},
		{name: "relative", raw: "sprint", want: "/"},
	}

	for _, tt := range tests {
		if got := safeUINext(tt.raw); got != tt.want {
			t.Fatalf("%s: safeUINext(%q) = %q, want %q", tt.name, tt.raw, got, tt.want)
		}
	}
}
