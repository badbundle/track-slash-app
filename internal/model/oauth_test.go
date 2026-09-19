package model

import "testing"

// Redirect URIs decide where an authorization code is delivered, so matching is
// exact: no normalisation, no case folding, no prefix match.
func TestOAuthClientAllowsRedirectURI(t *testing.T) {
	t.Parallel()

	client := OAuthClient{RedirectURIs: []string{
		"https://claude.ai/api/mcp/auth_callback",
		"http://localhost:8080/callback",
	}}

	for _, uri := range []string{
		"https://claude.ai/api/mcp/auth_callback",
		"http://localhost:8080/callback",
	} {
		if !client.AllowsRedirectURI(uri) {
			t.Fatalf("AllowsRedirectURI(%q) = false, want true", uri)
		}
	}

	for name, uri := range map[string]string{
		"unregistered host": "https://evil.example.com/cb",
		"trailing slash":    "https://claude.ai/api/mcp/auth_callback/",
		"added query":       "https://claude.ai/api/mcp/auth_callback?x=1",
		"different case":    "https://Claude.ai/api/mcp/auth_callback",
		"scheme downgrade":  "http://claude.ai/api/mcp/auth_callback",
		"prefix of a match": "https://claude.ai/api/mcp",
		"empty":             "",
	} {
		if client.AllowsRedirectURI(uri) {
			t.Fatalf("AllowsRedirectURI(%q) = true for %s, want false", uri, name)
		}
	}
}

func TestOAuthClientWithNoRedirectURIsAllowsNothing(t *testing.T) {
	t.Parallel()

	var client OAuthClient
	if client.AllowsRedirectURI("https://claude.ai/cb") {
		t.Fatal("a client with no registered URIs must allow none")
	}
}
