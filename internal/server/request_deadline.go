package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (s *Server) requestDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLongLivedRequest(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		timeout := s.requestTimeoutFor(r)
		deadline := time.Now().Add(timeout)
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()

		controller := http.NewResponseController(w)
		if err := controller.SetReadDeadline(deadline); err == nil {
			defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requestTimeoutFor(r *http.Request) time.Duration {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return s.uploadTimeout
	}
	if strings.Contains(r.URL.Path, "passkey") {
		return s.authRequestTimeout
	}
	if isFormWaitRequest(r) {
		// A wait URL may hold the request for its whole wait, then answers
		// like any other request.
		return s.formWaitMax + s.requestTimeout
	}
	return s.requestTimeout
}

func isLongLivedRequest(path string) bool {
	switch path {
	case "/api/v1/ws", "/realtime", "/mcp", devReloadPath:
		return true
	default:
		return false
	}
}
