package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A query string can hold a searched email address or a push endpoint, so the
// access log keeps the path and drops the rest, while the handler still sees
// the whole request.
func TestAccessLogRecordsThePathWithoutTheQuery(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	var query string
	handler := newAccessLogger(&logs)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("q")
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/ada/projects/TRACK/member-candidates?q=grace%40example.com", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if query != "grace@example.com" {
		t.Fatalf("handler query = %q", query)
	}
	line := logs.String()
	if !strings.Contains(line, "GET") || !strings.Contains(line, "http://example.com/ada/projects/TRACK/member-candidates HTTP/1.1") ||
		!strings.Contains(line, "204") {
		t.Fatalf("access log = %q", line)
	}
	if strings.Contains(line, "grace") || strings.Contains(line, "?") {
		t.Fatalf("access log kept the query: %q", line)
	}
}

// A push endpoint is a capability URL, so the settings page sends it in a
// header rather than in the URL.
func TestPushSettingsScriptKeepsTheEndpointOutOfTheURL(t *testing.T) {
	t.Parallel()
	body, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	source := string(body)
	if !strings.Contains(source, `headers: { "Accept": "application/json", "`+uiPushEndpointHeader+`": endpoint },`) {
		t.Fatalf("app.js does not send the push endpoint in %s", uiPushEndpointHeader)
	}
	if strings.Contains(source, "?endpoint=") {
		t.Fatal("app.js still puts the push endpoint in a query string")
	}
}
