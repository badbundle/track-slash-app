package server

import (
	"context"
	"net/http"
	"strings"
	"time"
)

func (s *Server) requestDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLongLivedRequest(r.URL.Path) || isDownloadRequest(r) {
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
	// MCP is stateless JSON with no stream, but a tool call can carry an
	// attachment as base64, so it gets as long as a multipart upload.
	if r.URL.Path == mcpPath {
		return s.uploadTimeout
	}
	if strings.Contains(r.URL.Path, "passkey") {
		return s.authRequestTimeout
	}
	return s.requestTimeout
}

func isLongLivedRequest(path string) bool {
	switch path {
	case "/api/v1/ws", "/realtime", devReloadPath:
		return true
	default:
		return false
	}
}

// isDownloadRequest reports whether r fetches stored object bytes: every such
// route ends in /content. A large file over a slow link can take longer than
// any fixed deadline, so streamStorageObjectContent cuts a download off only
// when it stops making progress.
func isDownloadRequest(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasSuffix(r.URL.Path, "/content")
}
