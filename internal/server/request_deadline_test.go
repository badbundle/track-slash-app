package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestDeadlineConfiguration(t *testing.T) {
	t.Parallel()
	configured := NewWithOptions(nil, nil, Options{
		RequestTimeout:     time.Second,
		AuthRequestTimeout: 2 * time.Second,
		UploadTimeout:      3 * time.Second,
	})
	if configured.requestTimeout != time.Second || configured.authRequestTimeout != 2*time.Second || configured.uploadTimeout != 3*time.Second {
		t.Fatalf("configured timeouts = %v/%v/%v", configured.requestTimeout, configured.authRequestTimeout, configured.uploadTimeout)
	}

	defaults := NewWithOptions(nil, nil, Options{})
	if defaults.requestTimeout != 15*time.Second || defaults.authRequestTimeout != 30*time.Second || defaults.uploadTimeout != 2*time.Minute {
		t.Fatalf("default timeouts = %v/%v/%v", defaults.requestTimeout, defaults.authRequestTimeout, defaults.uploadTimeout)
	}
}

func TestRequestTimeoutFor(t *testing.T) {
	t.Parallel()
	srv := &Server{
		requestTimeout:     15 * time.Second,
		authRequestTimeout: 30 * time.Second,
		uploadTimeout:      2 * time.Minute,
	}
	tests := []struct {
		path        string
		contentType string
		want        time.Duration
	}{
		{path: "/api/v1/session", contentType: "application/json", want: 15 * time.Second},
		{path: "/api/v1/session/passkey", contentType: "application/json", want: 30 * time.Second},
		{path: "/api/v1/projects/objects", contentType: " Multipart/Form-Data; boundary=test ", want: 2 * time.Minute},
		{path: "/mcp", contentType: "application/json", want: 2 * time.Minute},
		{path: "/mcp/other", contentType: "application/json", want: 15 * time.Second},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, tt.path, nil)
		req.Header.Set("Content-Type", tt.contentType)
		if got := srv.requestTimeoutFor(req); got != tt.want {
			t.Fatalf("requestTimeoutFor(%s, %s) = %v, want %v", tt.path, tt.contentType, got, tt.want)
		}
	}
}

func TestLongLivedRequestPaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/v1/ws", "/realtime", devReloadPath} {
		if !isLongLivedRequest(path) {
			t.Fatalf("isLongLivedRequest(%q) = false, want true", path)
		}
	}
	// MCP here is stateless JSON: every request is one response, never a
	// stream, so it keeps a deadline.
	for _, path := range []string{"/api/v1/session", "/mcp"} {
		if isLongLivedRequest(path) {
			t.Fatalf("isLongLivedRequest(%q) = true, want false", path)
		}
	}
}

func TestRequestDeadlineSkipsLongLivedConnections(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{RequestTimeout: time.Second})
	handler := srv.requestDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline := r.Context().Deadline()
		if hasDeadline {
			http.Error(w, "unexpected deadline", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", recorder.Code)
	}
}

func TestRequestDeadlineAppliesUploadTimeoutToMCP(t *testing.T) {
	t.Parallel()
	srv := NewWithOptions(nil, nil, Options{RequestTimeout: time.Second, UploadTimeout: time.Hour})
	handler := srv.requestDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 59*time.Minute {
			http.Error(w, "missing upload deadline", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("code = %d body = %s, want 204", recorder.Code, recorder.Body.String())
	}
}

func TestSlowRequestBodyIsCancelled(t *testing.T) {
	srv := NewWithOptions(nil, nil, Options{RequestTimeout: 50 * time.Millisecond})
	handler := srv.requestDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "request body timed out", http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/session", reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: time.Second}
	started := time.Now()
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("slow request: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("code = %d, want 408", res.StatusCode)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("slow request cancelled after %v, want under 500ms", elapsed)
	}
}
