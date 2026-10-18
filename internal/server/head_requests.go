package server

import "net/http"

// headExemptPaths keep their own HEAD behaviour and must not be rewritten.
//
//   - "/" answers an unconditional 200 for uptime monitors, which probe the root
//     with HEAD and would otherwise follow the signed-out redirect to /login.
//   - "/mcp" answers POST only, as a stateless JSON endpoint, so a HEAD probe
//     has no GET to mirror.
//   - "/api/v1/ws" is a long-lived stream. Rewriting a HEAD probe into a GET
//     would open a connection nobody is going to read.
//   - devReloadPath is the development server-sent-event stream, same reason.
var headExemptPaths = map[string]bool{
	"/":           true,
	"/mcp":        true,
	"/api/v1/ws":  true,
	devReloadPath: true,
}

// serveHEADAsGET routes HEAD requests through the matching GET handler. chi's
// Get() registers GET alone, so without this every route but the few with an
// explicit HEAD registration answers a HEAD probe with 405 and an empty body.
//
// The response body is left to net/http, which discards writes for a request
// that arrived as HEAD while still reporting the Content-Length a GET would
// have produced. The rewritten method lives on a copy so the connection's own
// record of the request stays HEAD.
func serveHEADAsGET(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || headExemptPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		routed := r.Clone(r.Context())
		routed.Method = http.MethodGet
		next.ServeHTTP(w, routed)
	})
}
