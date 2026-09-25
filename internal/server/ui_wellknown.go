package server

import (
	"fmt"
	"net/http"
)

// The password and passkey managers both live on the Login account page.
const uiCredentialSettingsPath = uiLoginSettingsPath

// uiChangePassword implements the W3C well-known URL for changing passwords, so
// a password manager can deep-link a user straight to rotation.
//
// The spec's companion requirement is that
// /.well-known/resource-that-should-not-exist-whose-status-code-should-not-be-200
// must not return 200, otherwise managers assume the site answers 200 for
// everything and ignore this endpoint entirely. That holds because unmatched
// paths 404, which TestSentinelWellKnownPathIsNotFound pins down.
func (s *Server) uiChangePassword(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, uiCredentialSettingsPath, http.StatusSeeOther)
}

// uiPasskeyEndpoints lets a credential manager point a user at passkey
// enrolment and management. Both live on the Login account page.
func (s *Server) uiPasskeyEndpoints(w http.ResponseWriter, r *http.Request) {
	origin := s.uiRequestOrigin(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	fmt.Fprintf(w, "{\n  \"enroll\": %q,\n  \"manage\": %q\n}\n",
		origin+uiCredentialSettingsPath, origin+uiCredentialSettingsPath)
}

// uiRobotsTxt replaces the boilerplate a CDN would otherwise inject with a
// policy the application actually owns.
//
// The disallow list is deliberately short and uses only unambiguous prefixes.
// robots.txt matches by prefix, so a rule like `Disallow: /me` would also block
// an owner named `mercury`. Everything else worth protecting is behind
// authentication and redirects an anonymous crawler to /login anyway.
//
// The Content-Signal line preserves the declaration a Cloudflare-fronted
// deployment publishes today. Serving our own robots.txt overrides that file,
// and dropping the signal silently would loosen what the site says about AI
// training, so the status quo is carried over rather than discarded.
const robotsTxt = `# trackslash

User-agent: *
Content-Signal: search=yes, ai-train=no
Disallow: /login
Disallow: /signup
Disallow: /settings
Disallow: /tokens
Disallow: /projects
Disallow: /issues
Disallow: /api/
Disallow: /mcp
Allow: /
`

func (s *Server) uiRobotsTxt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(robotsTxt))
}
