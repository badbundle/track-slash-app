package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/bradleymackey/track-slash/internal/model"
)

const (
	oauthAuthorizePath                 = "/oauth/authorize"
	oauthTokenPath                     = "/oauth/token"
	oauthRevokePath                    = "/oauth/revoke"
	oauthProtectedResourceMetadataPath = "/.well-known/oauth-protected-resource"
	oauthAuthorizationServerPath       = "/.well-known/oauth-authorization-server"
	mcpPath                            = "/mcp"
)

// oauthAuthorizationServerMetadata is RFC 8414. The SDK ships an equivalent
// struct, but its jwks_uri field has no omitempty and trackslash issues opaque
// tokens rather than JWTs, so reusing it would advertise an empty key set URL to
// every client. The protected resource document has no such problem and does
// reuse oauthex.ProtectedResourceMetadata.
//
// registration_endpoint is deliberately absent. Its absence is the signal that
// tells a client not to attempt dynamic client registration and to ask the
// operator for a client ID and secret instead, which is exactly the flow
// trackslash supports.
type oauthAuthorizationServerMetadata struct {
	Issuer                                 string   `json:"issuer"`
	AuthorizationEndpoint                  string   `json:"authorization_endpoint"`
	TokenEndpoint                          string   `json:"token_endpoint"`
	RevocationEndpoint                     string   `json:"revocation_endpoint"`
	ResponseTypesSupported                 []string `json:"response_types_supported"`
	ResponseModesSupported                 []string `json:"response_modes_supported"`
	GrantTypesSupported                    []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported          []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported      []string `json:"token_endpoint_auth_methods_supported"`
	RevocationEndpointAuthMethodsSupported []string `json:"revocation_endpoint_auth_methods_supported"`
	ScopesSupported                        []string `json:"scopes_supported"`
	ServiceDocumentation                   string   `json:"service_documentation,omitempty"`
}

var oauthClientAuthMethods = []string{"client_secret_basic", "client_secret_post"}

func (s *Server) oauthAuthorizationServerMetadata(r *http.Request) oauthAuthorizationServerMetadata {
	origin := s.uiRequestOrigin(r)
	return oauthAuthorizationServerMetadata{
		Issuer:                                 origin,
		AuthorizationEndpoint:                  origin + oauthAuthorizePath,
		TokenEndpoint:                          origin + oauthTokenPath,
		RevocationEndpoint:                     origin + oauthRevokePath,
		ResponseTypesSupported:                 []string{"code"},
		ResponseModesSupported:                 []string{"query"},
		GrantTypesSupported:                    []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:          []string{oauthCodeChallengeMethodS256},
		TokenEndpointAuthMethodsSupported:      oauthClientAuthMethods,
		RevocationEndpointAuthMethodsSupported: oauthClientAuthMethods,
		ScopesSupported:                        []string{model.OAuthScopeMCP},
		ServiceDocumentation:                   origin + "/security",
	}
}

func (s *Server) oauthProtectedResourceMetadata(r *http.Request) oauthex.ProtectedResourceMetadata {
	origin := s.uiRequestOrigin(r)
	return oauthex.ProtectedResourceMetadata{
		Resource:               origin + mcpPath,
		AuthorizationServers:   []string{origin},
		ScopesSupported:        []string{model.OAuthScopeMCP},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "trackslash",
		ResourceDocumentation:  origin + "/security",
	}
}

func (s *Server) oauthAuthorizationServerMetadataHandler(w http.ResponseWriter, r *http.Request) {
	writeOAuthCORS(w)
	writeOAuthMetadata(w, s.oauthAuthorizationServerMetadata(r))
}

func (s *Server) oauthProtectedResourceMetadataHandler(w http.ResponseWriter, r *http.Request) {
	writeOAuthCORS(w)
	writeOAuthMetadata(w, s.oauthProtectedResourceMetadata(r))
}

func writeOAuthMetadata(w http.ResponseWriter, document any) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, document)
}

// oauthResourceMetadataURL is what an unauthenticated /mcp request points a
// client at, so it can discover where to start an OAuth flow.
func (s *Server) oauthResourceMetadataURL(r *http.Request) string {
	// RFC 9728 section 3.1 inserts the resource path after the well-known
	// segment, so the document for the resource at /mcp lives here. The bare
	// path is served too, for clients that skip the insertion; trackslash has
	// exactly one protected resource, so the two cannot disagree.
	return s.uiRequestOrigin(r) + oauthProtectedResourceMetadataPath + mcpPath
}

// writeOAuthCORS opens the discovery and token endpoints to any origin.
//
// A wildcard is correct here and not a weakening: these endpoints are either
// public documents or authenticated by a client secret carried in the request
// itself. None of them reads a cookie, so credentials stay off and a hostile
// page learns nothing it could not fetch from its own server.
func writeOAuthCORS(w http.ResponseWriter) {
	headers := w.Header()
	headers.Set("Access-Control-Allow-Origin", "*")
	headers.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	headers.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	headers.Set("Access-Control-Max-Age", "300")
}

func oauthCORSPreflight(w http.ResponseWriter, _ *http.Request) {
	writeOAuthCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

// oauthPublicPaths are answered for any origin, so the shared CORS middleware
// must not reach them.
//
// go-chi/cors intercepts a preflight from an origin outside its allow list and
// answers a bare 200 with no CORS headers, without ever calling the handler
// underneath. For a browser that is a failed preflight, which would make the
// discovery documents unreadable from exactly the clients that need them.
func oauthPublicPath(path string) bool {
	switch path {
	case oauthTokenPath, oauthRevokePath,
		oauthProtectedResourceMetadataPath, oauthProtectedResourceMetadataPath + mcpPath,
		oauthAuthorizationServerPath, oauthAuthorizationServerPath + mcpPath:
		return true
	}
	return false
}

// mountOAuthPublicRoutes registers the endpoints an OAuth client reaches
// without a browser session: discovery, token exchange and revocation.
func (s *Server) mountOAuthPublicRoutes(r chi.Router) {
	for _, path := range []string{
		oauthProtectedResourceMetadataPath,
		oauthProtectedResourceMetadataPath + mcpPath,
	} {
		r.Get(path, s.oauthProtectedResourceMetadataHandler)
		r.Method(http.MethodOptions, path, http.HandlerFunc(oauthCORSPreflight))
	}
	for _, path := range []string{
		oauthAuthorizationServerPath,
		oauthAuthorizationServerPath + mcpPath,
	} {
		r.Get(path, s.oauthAuthorizationServerMetadataHandler)
		r.Method(http.MethodOptions, path, http.HandlerFunc(oauthCORSPreflight))
	}
	r.Post(oauthTokenPath, s.oauthToken)
	r.Method(http.MethodOptions, oauthTokenPath, http.HandlerFunc(oauthCORSPreflight))
	r.Post(oauthRevokePath, s.oauthRevoke)
	r.Method(http.MethodOptions, oauthRevokePath, http.HandlerFunc(oauthCORSPreflight))
}

func exceptOAuthPublicPaths(middleware func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := middleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if oauthPublicPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}
