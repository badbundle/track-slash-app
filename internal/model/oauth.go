package model

import (
	"time"

	"github.com/google/uuid"
)

// OAuthClient is a registered application that may ask a trackslash user for
// access, such as a Claude.ai custom connector. The secret is intentionally
// excluded: it is stored only as a hash and can never be read back, so it is
// shown once at creation and never again.
type OAuthClient struct {
	ID           uuid.UUID  `json:"id"`
	ClientID     string     `json:"client_id"`
	Name         string     `json:"name"`
	RedirectURIs []string   `json:"redirect_uris"`
	CreatedByID  uuid.UUID  `json:"created_by_id"`
	DisabledAt   *time.Time `json:"disabled_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// AllowsRedirectURI reports whether uri is one this client registered.
//
// The comparison is exact. OAuth redirect URIs are a security boundary — they
// decide where an authorization code is delivered — so no normalisation,
// case folding, or prefix matching is applied. A client that wants a different
// callback registers it.
func (c OAuthClient) AllowsRedirectURI(uri string) bool {
	for _, candidate := range c.RedirectURIs {
		if candidate == uri {
			return true
		}
	}
	return false
}

// OAuthScopeMCP is the only scope trackslash issues. An access token carries
// the full authority of the user who approved it, exactly as an API token that
// user created would, so subdividing it here would imply an enforcement
// boundary that does not exist anywhere else in the product.
const OAuthScopeMCP = "mcp"
