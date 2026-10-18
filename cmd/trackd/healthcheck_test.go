package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckHealth(t *testing.T) {
	status := http.StatusOK
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()

	if err := checkHealth(srv.URL); err != nil {
		t.Fatalf("checkHealth healthy server: %v", err)
	}
	if path != "/api/v1/healthz" {
		t.Fatalf("checkHealth requested %q, want /api/v1/healthz", path)
	}

	status = http.StatusServiceUnavailable
	if err := checkHealth(srv.URL); err == nil {
		t.Fatal("checkHealth accepted 503")
	}
}

func TestCheckHealthRejectsUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if err := checkHealth(srv.URL); err == nil {
		t.Fatal("checkHealth accepted a closed server")
	}
}
