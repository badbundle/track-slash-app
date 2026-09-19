package server

import "net/http"

// mcpBearerChallenge is what an unauthenticated /mcp request is told to do about
// it: either present a token, or go and discover how to obtain one.
//
// The resource_metadata parameter is RFC 9728, and pointing at that document is
// what lets a client start an OAuth flow on its own. trackslash also still
// accepts a hand-made API token, so the challenge advertises discovery without
// implying it is the only way in.
//
// The URL has to be built per request. It is derived from TRACK_SLASH_PUBLIC_ORIGIN
// when that is set and from the request host otherwise, which is what makes a
// localhost instance work with no configuration at all. The MCP SDK can emit
// this header itself, but only from a string fixed when the route is mounted,
// which cannot express the second case.
func (s *Server) mcpBearerChallenge(r *http.Request) string {
	return `Bearer realm="trackslash", resource_metadata="` + s.oauthResourceMetadataURL(r) + `"`
}

func (s *Server) mcpBearerChallengeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&mcpChallengeWriter{ResponseWriter: w, challenge: s.mcpBearerChallenge(r)}, r)
	})
}

type mcpChallengeWriter struct {
	http.ResponseWriter
	challenge   string
	wroteHeader bool
}

func (w *mcpChallengeWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		if status == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
			w.Header().Set("WWW-Authenticate", w.challenge)
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

// MCP responses can be streamed, so the wrapper must not hide the capabilities
// of the writer underneath it. Unwrap covers http.ResponseController; Flush
// covers code that type-asserts directly.
func (w *mcpChallengeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *mcpChallengeWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
