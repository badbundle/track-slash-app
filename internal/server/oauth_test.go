package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestOAuthMetadataDocumentsDeriveTheirOrigin(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		publicOrigin string
		host         string
		wantIssuer   string
	}{
		// A localhost instance is configured with nothing at all, so the
		// documents have to be correct from the request alone.
		{name: "derived from the request", host: "localhost:8080", wantIssuer: "http://localhost:8080"},
		{name: "configured origin wins", publicOrigin: "https://track.example.com", host: "internal:8080", wantIssuer: "https://track.example.com"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := NewWithOptions(nil, nil, Options{PublicOrigin: tt.publicOrigin}).Router()

			req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}

			var document map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
				t.Fatalf("decode: %v (%s)", err, rec.Body.String())
			}
			for key, want := range map[string]string{
				"issuer":                 tt.wantIssuer,
				"authorization_endpoint": tt.wantIssuer + "/oauth/authorize",
				"token_endpoint":         tt.wantIssuer + "/oauth/token",
				"revocation_endpoint":    tt.wantIssuer + "/oauth/revoke",
			} {
				if document[key] != want {
					t.Fatalf("%s = %v, want %q", key, document[key], want)
				}
			}
			// Its absence is what tells a client to ask the operator for a
			// client ID and secret instead of trying to register itself.
			if _, present := document["registration_endpoint"]; present {
				t.Fatalf("registration_endpoint must be absent: %s", rec.Body.String())
			}
			// trackslash issues opaque tokens, so advertising a key set would
			// point clients at something that does not exist.
			if _, present := document["jwks_uri"]; present {
				t.Fatalf("jwks_uri must be absent: %s", rec.Body.String())
			}
			if methods, _ := document["code_challenge_methods_supported"].([]any); len(methods) != 1 || methods[0] != "S256" {
				t.Fatalf("code_challenge_methods_supported = %v, want [S256]", document["code_challenge_methods_supported"])
			}
		})
	}
}

func TestOAuthProtectedResourceMetadata(t *testing.T) {
	t.Parallel()
	router := NewWithOptions(nil, nil, Options{PublicOrigin: "https://track.example.com"}).Router()

	// RFC 9728 inserts the resource path after the well-known segment; the bare
	// path is served for clients that do not.
	for _, path := range []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		var document map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if document["resource"] != "https://track.example.com/mcp" {
			t.Fatalf("%s resource = %v", path, document["resource"])
		}
		servers, _ := document["authorization_servers"].([]any)
		if len(servers) != 1 || servers[0] != "https://track.example.com" {
			t.Fatalf("%s authorization_servers = %v", path, document["authorization_servers"])
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("%s must be readable cross-origin, got %q", path, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestOAuthPublicEndpointsAnswerPreflight(t *testing.T) {
	t.Parallel()
	// The allow list is deliberately narrow and does not contain the origin
	// below: OAuth discovery must work regardless of it.
	router := NewWithOptions(nil, nil, Options{CORSAllowedOrigins: []string{"https://app.example.com"}}).Router()

	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource/mcp",
		"/oauth/token",
		"/oauth/revoke",
	} {
		req := httptest.NewRequest(http.MethodOptions, path, nil)
		req.Header.Set("Origin", "https://inspector.example.com")
		req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("OPTIONS %s status = %d, want 204", path, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("OPTIONS %s allow-origin = %q, want *", path, got)
		}
	}
}

func TestOAuthPublicPathsBypassTheSharedCORSMiddleware(t *testing.T) {
	t.Parallel()

	if !oauthPublicPath("/oauth/token") || !oauthPublicPath("/.well-known/oauth-protected-resource/mcp") {
		t.Fatal("OAuth endpoints must be recognised as public")
	}
	if oauthPublicPath("/oauth/authorize") {
		t.Fatal("the consent screen is a browser page and must not bypass CORS handling")
	}
	if oauthPublicPath("/api/v1/me") {
		t.Fatal("ordinary API paths must not bypass CORS handling")
	}

	var reached bool
	handler := exceptOAuthPublicPaths(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/oauth/token", nil))
	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("public path did not bypass the middleware: reached = %v code = %d", reached, rec.Code)
	}

	reached = false
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, "/api/v1/me", nil))
	if reached || rec.Code != http.StatusTeapot {
		t.Fatalf("other paths must still pass through: reached = %v code = %d", reached, rec.Code)
	}
}

func TestOAuthVerifyPKCE(t *testing.T) {
	t.Parallel()

	verifier := "a-verifier-long-enough-to-be-realistic-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	if !oauthVerifyPKCE(challenge, verifier) {
		t.Fatal("a matching verifier must be accepted")
	}
	if oauthVerifyPKCE(challenge, verifier+"x") {
		t.Fatal("a different verifier must be rejected")
	}
	// Padded base64 is a different string and must not be treated as equal.
	if oauthVerifyPKCE(base64.StdEncoding.EncodeToString(sum[:]), verifier) {
		t.Fatal("only unpadded base64url challenges match")
	}
}

// The consent page is the one place a trackslash form is meant to submit to
// another origin, and the global policy would otherwise stop it.
func TestAllowOAuthFormActionWidensOnlyFormAction(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	allowOAuthFormAction(rec, "https://claude.ai/api/mcp/auth_callback")

	got := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(got, "form-action 'self' https://claude.ai;") {
		t.Fatalf("form-action was not widened: %s", got)
	}
	// Only the path was dropped: a redirect URI's origin is what a CSP source
	// expression can express, and nothing else in the policy may move.
	if strings.Contains(got, "auth_callback") {
		t.Fatalf("the path must not reach the policy: %s", got)
	}
	rest := strings.Replace(got, "form-action 'self' https://claude.ai", contentSecurityPolicyFormAction, 1)
	if rest != contentSecurityPolicy {
		t.Fatalf("other directives changed:\n got  %s\n want %s", rest, contentSecurityPolicy)
	}
}

func TestAllowOAuthFormActionIgnoresUnusableInput(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		policy      string
		redirectURI string
	}{
		{name: "relative uri", policy: contentSecurityPolicy, redirectURI: "/callback"},
		{name: "no scheme", policy: contentSecurityPolicy, redirectURI: "claude.ai/cb"},
		{name: "no policy set", policy: "", redirectURI: "https://claude.ai/cb"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			if tt.policy != "" {
				rec.Header().Set("Content-Security-Policy", tt.policy)
			}
			allowOAuthFormAction(rec, tt.redirectURI)
			if got := rec.Header().Get("Content-Security-Policy"); got != tt.policy {
				t.Fatalf("policy = %q, want %q", got, tt.policy)
			}
		})
	}
}

// A connector sends a signed-out user straight to the consent screen, so the
// login round trip has to bring them back to the request they arrived with.
func TestSafeUINextKeepsTheAuthorizationRequest(t *testing.T) {
	t.Parallel()

	raw := "/oauth/authorize?client_id=abc&state=xyz&code_challenge=zzz"
	if got := safeUINext(raw); got != raw {
		t.Fatalf("safeUINext(%q) = %q, want it preserved", raw, got)
	}
	if got := safeUINext("/oauth/token"); got != "/" {
		t.Fatalf("safeUINext(/oauth/token) = %q, want /", got)
	}
}

func TestUIParseOAuthRedirectURIs(t *testing.T) {
	t.Parallel()

	got, err := uiParseOAuthRedirectURIs("https://claude.ai/cb\n\nhttp://localhost:8080/cb\nhttps://claude.ai/cb\n")
	if err != nil {
		t.Fatalf("uiParseOAuthRedirectURIs: %v", err)
	}
	// Blank lines are skipped and a repeat is folded away rather than stored
	// twice, since matching is exact and a duplicate would never be reached.
	if len(got) != 2 || got[0] != "https://claude.ai/cb" || got[1] != "http://localhost:8080/cb" {
		t.Fatalf("parsed = %v", got)
	}

	for _, tt := range []struct {
		name string
		in   string
	}{
		{name: "empty", in: "   \n  "},
		{name: "relative", in: "/callback"},
		{name: "fragment", in: "https://claude.ai/cb#part"},
		{name: "plaintext off localhost", in: "http://claude.ai/cb"},
		{name: "unsupported scheme", in: "ftp://claude.ai/cb"},
		// RFC 6749 section 3.1.2 excludes userinfo, and trackslash would be
		// putting a fresh authorization code next to a password in a header.
		{name: "userinfo", in: "https://user:pass@claude.ai/cb"},
		// These hosts would inject into the consent page's CSP.
		{name: "host with a semicolon", in: "https://evil.example.com;script-src/cb"},
		{name: "host with a comma", in: "https://evil.example.com,claude.ai/cb"},
		{name: "wildcard host", in: "https://*/cb"},
		{name: "too many", in: tooManyRedirectURIs(11)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := uiParseOAuthRedirectURIs(tt.in); err == nil {
				t.Fatalf("uiParseOAuthRedirectURIs(%q) should have failed", tt.in)
			}
		})
	}
}

// Distinct URIs, so the count is tested rather than the deduplication.
func tooManyRedirectURIs(count int) string {
	var builder strings.Builder
	for i := range count {
		builder.WriteString("https://claude.ai/cb")
		builder.WriteString(strconv.Itoa(i))
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestOAuthRedirectTargetPreservesExistingQuery(t *testing.T) {
	t.Parallel()

	got := oauthRedirectTarget("https://claude.ai/cb?keep=1", map[string]string{"code": "abc", "state": ""})
	if !strings.Contains(got, "keep=1") || !strings.Contains(got, "code=abc") {
		t.Fatalf("target = %q", got)
	}
	// A request that carried no state must not come back with an empty one.
	if strings.Contains(got, "state=") {
		t.Fatalf("empty state should be omitted: %q", got)
	}
}

// Go's URL parser accepts ';' ',' '*' and '\” in a host. The consent page
// splices the origin into its Content-Security-Policy, where ';' would add a
// whole directive and ',' would split the header into two policies.
func TestAllowOAuthFormActionRejectsHostsThatWouldInjectCSP(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		redirectURI string
	}{
		{name: "directive injection", redirectURI: "https://evil.example.com;script-src/cb"},
		{name: "policy splitting", redirectURI: "https://evil.example.com,claude.ai/cb"},
		{name: "wildcard host", redirectURI: "https://*/cb"},
		{name: "quote", redirectURI: "https://evil.example.com'/cb"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Security-Policy", contentSecurityPolicy)
			allowOAuthFormAction(rec, tt.redirectURI)
			if got := rec.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
				t.Fatalf("policy was modified by %q:\n%s", tt.redirectURI, got)
			}
		})
	}
}

func TestOAuthSafeHost(t *testing.T) {
	t.Parallel()

	for _, host := range []string{"claude.ai", "localhost:8080", "sub.example.co.uk", "[::1]", "127.0.0.1:443"} {
		if !oauthSafeHost(host) {
			t.Fatalf("oauthSafeHost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"", "evil.com;script-src", "a,b", "*", "e'v", "a b", "x\"y", "a<b"} {
		if oauthSafeHost(host) {
			t.Fatalf("oauthSafeHost(%q) = true, want false", host)
		}
	}
}

func TestOAuthValidPKCELength(t *testing.T) {
	t.Parallel()

	// RFC 7636 section 4.1 fixes the range at 43 to 128 characters.
	if oauthValidPKCELength(strings.Repeat("a", 42)) || oauthValidPKCELength(strings.Repeat("a", 129)) {
		t.Fatal("lengths outside 43-128 must be rejected")
	}
	if !oauthValidPKCELength(strings.Repeat("a", 43)) || !oauthValidPKCELength(strings.Repeat("a", 128)) {
		t.Fatal("the boundary lengths must be accepted")
	}
}
