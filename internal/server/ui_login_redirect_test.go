package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// htmx follows a 303 transparently and swaps the standalone login document into
// #main, which is a sibling of the sidebar, so the login card renders inside the
// app shell. HX-Redirect makes the browser navigate instead.
func TestRedirectUILoginUsesHXRedirectForHTMX(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/me/panel", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "http://localhost:8080/me")
	rec := httptest.NewRecorder()
	redirectUILogin(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/login?next=%2Fme" {
		t.Fatalf("HX-Redirect = %q, want %q", got, "/login?next=%2Fme")
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Fatalf("Location = %q, want no plain redirect", got)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
}

func TestRedirectUILoginKeepsPlainRedirectForNavigation(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	redirectUILogin(rec, httptest.NewRequest(http.MethodGet, "/me", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/login?next=%2Fme" {
		t.Fatalf("Location = %q, want %q", got, "/login?next=%2Fme")
	}
	if got := rec.Header().Get("HX-Redirect"); got != "" {
		t.Fatalf("HX-Redirect = %q, want absent", got)
	}
}

// The htmx request URI is a panel fragment, so returning to it after sign-in
// would render a fragment as a whole page.
func TestUILoginNextPrefersTheBrowserAddressForHTMX(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		requestURI string
		htmx       bool
		currentURL string
		want       string
	}{
		{name: "plain navigation uses the request", requestURI: "/settings/profile", want: "/settings/profile"},
		{name: "htmx uses the browser address", requestURI: "/me/panel", htmx: true, currentURL: "http://localhost:8080/me/all", want: "/me/all"},
		{name: "htmx keeps the query", requestURI: "/badbundle/projects/TRACK/all/panel", htmx: true, currentURL: "http://localhost:8080/badbundle/projects/TRACK/all?sort=priority", want: "/badbundle/projects/TRACK/all?sort=priority"},
		{name: "htmx falls back when the header is missing", requestURI: "/me/panel", htmx: true, want: "/me/panel"},
		{name: "htmx falls back when the header is unparseable", requestURI: "/me/panel", htmx: true, currentURL: "://", want: "/me/panel"},
		{name: "an off-site browser address is refused", requestURI: "/me/panel", htmx: true, currentURL: "https://evil.example.com/steal", want: "/"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tt.requestURI, nil)
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			if tt.currentURL != "" {
				req.Header.Set("HX-Current-URL", tt.currentURL)
			}
			if got := uiLoginNext(req); got != tt.want {
				t.Fatalf("uiLoginNext = %q, want %q", got, tt.want)
			}
		})
	}
}
