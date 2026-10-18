package server

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

const (
	securityContactAddress = "mailto:security@badbundle.com"
	securityPolicyPath     = "/security"

	// RFC 9116 makes Expires mandatory and requires it to be in the future, so
	// a value baked in at build time turns every long-lived deployment
	// non-conforming without anyone noticing. It is computed per request
	// instead, truncated to the day so the document is still cacheable.
	securityTxtValidity = 365 * 24 * time.Hour
)

// webAppManifest completes a PWA that already ships a service worker, web push,
// and a manifest-src 'self' directive in the CSP with nothing to point at.
const webAppManifest = `{
  "name": "trackslash",
  "short_name": "trackslash",
  "description": "The open issue tracker your coding agents can actually use.",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "background_color": "#f8fafc",
  "theme_color": "#4f46e5",
  "icons": [
    { "src": "/static/icon.svg", "sizes": "any", "type": "image/svg+xml" },
    { "src": "/static/icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/static/icon-512.png", "sizes": "512x512", "type": "image/png" },
    { "src": "/static/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
`

// uiSecurityTxt publishes the disclosure policy where researchers and tooling
// look for it. The policy itself already existed at /security, but nothing
// pointed at it from a well-known location.
func (s *Server) uiSecurityTxt(w http.ResponseWriter, r *http.Request) {
	origin := s.uiRequestOrigin(r)
	expires := time.Now().UTC().Add(securityTxtValidity).Truncate(24 * time.Hour)
	body := fmt.Sprintf(""+
		"Contact: %s\n"+
		"Expires: %s\n"+
		"Policy: %s%s\n"+
		"Canonical: %s/.well-known/security.txt\n"+
		"Preferred-Languages: en\n",
		securityContactAddress,
		expires.Format(time.RFC3339),
		origin, securityPolicyPath,
		origin,
	)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(body))
}

func (s *Server) uiWebAppManifest(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(webAppManifest))
}

// uiFavicon answers the implicit request every browser makes, which previously
// 404ed on every page load for every visitor.
func (s *Server) uiFavicon(w http.ResponseWriter, r *http.Request) {
	icon, err := fs.ReadFile(uiStaticFS, "favicon.ico")
	if err != nil {
		// Defensive: the icon is embedded at build time, so a failure here
		// means the binary itself is malformed.
		writeUIInternalError(w, "ui favicon", err)
		return
	}
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "favicon.ico", time.Time{}, bytes.NewReader(icon))
}

// uiRequestOrigin prefers the configured canonical origin and otherwise
// reconstructs one from the request, which is enough for documents that only
// need to name themselves.
func (s *Server) uiRequestOrigin(r *http.Request) string {
	if s.publicOrigin != "" {
		return strings.TrimSuffix(s.publicOrigin, "/")
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
