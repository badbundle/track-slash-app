package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChangePasswordWellKnownRedirectsToLoginSettings(t *testing.T) {
	t.Parallel()
	router := New(nil, nil, nil).Router()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/change-password", nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if got := rec.Header().Get("Location"); got != "/settings/login" {
		t.Fatalf("Location = %q, want %q", got, "/settings/login")
	}
}

// The change-password spec requires this path not to return 200. A site that
// answers 200 for everything is assumed to have no real well-known support, and
// password managers ignore it. It only holds because unmatched paths 404, so it
// is worth pinning: a future catch-all could break the feature silently.
func TestSentinelWellKnownPathIsNotFound(t *testing.T) {
	t.Parallel()
	router := New(nil, nil, nil).Router()

	const sentinel = "/.well-known/resource-that-should-not-exist-whose-status-code-should-not-be-200"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, sentinel, nil))

	if rec.Code == http.StatusOK {
		t.Fatalf("sentinel path returned 200, which disables the change-password well-known")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestPasskeyEndpointsWellKnown(t *testing.T) {
	t.Parallel()
	router := New(nil, nil, nil).Router()

	req := httptest.NewRequest(http.MethodGet, "http://track.example.com/.well-known/passkey-endpoints", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var endpoints struct {
		Enroll string `json:"enroll"`
		Manage string `json:"manage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &endpoints); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	const want = "http://track.example.com/settings/login"
	if endpoints.Enroll != want || endpoints.Manage != want {
		t.Fatalf("endpoints = %+v, want both %q", endpoints, want)
	}
}

func TestPasskeyEndpointsUseTheConfiguredOrigin(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{PublicOrigin: "https://track.example.com"})

	req := httptest.NewRequest(http.MethodGet, "http://internal-lb.local/.well-known/passkey-endpoints", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if !strings.Contains(rec.Body.String(), "https://track.example.com/settings/login") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRobotsTxtIsServedByTheApp(t *testing.T) {
	t.Parallel()
	router := New(nil, nil, nil).Router()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"User-agent: *",
		"Disallow: /settings",
		"Disallow: /tokens",
		"Disallow: /api/",
		"Disallow: /mcp",
		// Overriding a CDN's robots.txt drops whatever it declared, so the
		// signal is carried over deliberately rather than lost.
		"Content-Signal:",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("robots.txt missing %q:\n%s", want, body)
		}
	}
}

// robots.txt matches by prefix, so a rule must not accidentally cover an owner
// whose username starts with the same characters.
func TestRobotsTxtDisallowsOnlyUnambiguousPrefixes(t *testing.T) {
	t.Parallel()

	for _, line := range strings.Split(robotsTxt, "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "Disallow: ")
		if !ok || path == "" {
			continue
		}
		// Owner-scoped pages live at /{owner}/..., so a rule must never be a
		// prefix another owner could collide with by accident.
		for _, reserved := range []string{"/me", "/o", "/p", "/i", "/a"} {
			if path == reserved {
				t.Fatalf("Disallow %q is short enough to block unrelated owner paths", path)
			}
		}
	}
}
