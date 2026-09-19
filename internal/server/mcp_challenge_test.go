package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A 401 with no WWW-Authenticate leaves a client guessing, and one that omits
// resource_metadata leaves a discovery-first client unable to find the OAuth
// flow it would otherwise complete on its own.
func TestMCPUnauthenticatedRequestAdvertisesBearer(t *testing.T) {
	t.Parallel()
	srv := New(nil, nil, nil)
	router := srv.Router()
	wantChallenge := `Bearer realm="trackslash", resource_metadata="http://example.com/.well-known/oauth-protected-resource/mcp"`

	for _, tt := range []struct {
		name       string
		authHeader string
	}{
		{name: "no authorization header"},
		{name: "not a bearer scheme", authHeader: "Basic dXNlcjpwYXNz"},
		{name: "malformed bearer", authHeader: "Bearer"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != wantChallenge {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, wantChallenge)
			}
		})
	}
}

func TestMCPBearerChallengeOnlyAppliesToUnauthorized(t *testing.T) {
	t.Parallel()

	srv := New(nil, nil, nil)
	computed := `Bearer realm="trackslash", resource_metadata="http://example.com/.well-known/oauth-protected-resource/mcp"`

	for _, tt := range []struct {
		name          string
		status        int
		existing      string
		wantChallenge string
	}{
		{name: "success", status: http.StatusOK},
		{name: "forbidden", status: http.StatusForbidden},
		{name: "server error", status: http.StatusInternalServerError},
		{name: "unauthorized", status: http.StatusUnauthorized, wantChallenge: computed},
		{
			name:          "an existing challenge is left alone",
			status:        http.StatusUnauthorized,
			existing:      `Bearer resource_metadata="https://example.com/meta"`,
			wantChallenge: `Bearer resource_metadata="https://example.com/meta"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			handler := srv.mcpBearerChallengeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.existing != "" {
					w.Header().Set("WWW-Authenticate", tt.existing)
				}
				w.WriteHeader(tt.status)
			}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != tt.wantChallenge {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, tt.wantChallenge)
			}
		})
	}
}

// MCP responses can be streamed, so the wrapper must not hide what the writer
// underneath it can do.
func TestMCPChallengeWriterKeepsStreamingCapabilities(t *testing.T) {
	t.Parallel()

	var flushed bool
	srv := New(nil, nil, nil)
	handler := srv.mcpBearerChallengeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Errorf("ResponseController.Flush: %v", err)
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
			flushed = true
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))

	if !flushed {
		t.Fatal("the wrapped writer no longer satisfies http.Flusher")
	}
}

// A repeated WriteHeader must not rewrite the challenge for a later status.
func TestMCPChallengeWriterIgnoresRepeatedWriteHeader(t *testing.T) {
	t.Parallel()

	srv := New(nil, nil, nil)
	handler := srv.mcpBearerChallengeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))

	if got := rec.Header().Get("WWW-Authenticate"); got != "" {
		t.Fatalf("WWW-Authenticate = %q, want absent", got)
	}
}

func TestMCPChallengeWriterUnwrapsToTheUnderlyingWriter(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	wrapped := &mcpChallengeWriter{ResponseWriter: rec}
	if got := wrapped.Unwrap(); got != http.ResponseWriter(rec) {
		t.Fatalf("Unwrap returned %T, want the recorder", got)
	}
}
