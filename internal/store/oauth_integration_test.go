package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/bradleymackey/track-slash/internal/testutil"
)

type oauthEnv struct {
	ctx   context.Context
	store *store.Store
	pool  *pgxpool.Pool
	user  model.User
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db := testutil.NewMigratedDatabase(t)
	st := store.New(db.Pool)
	user, err := st.CreateUser(ctx, "oauth-"+uuid.NewString()+"@example.com", "oauth")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return &oauthEnv{ctx: ctx, store: st, pool: db.Pool, user: user}
}

// expireEverything winds every OAuth deadline into the past. Expiry is set by
// the database clock, so reaching past the public API is the only way to reach
// the expired branches without making a test wait out a real TTL.
func (e *oauthEnv) expireEverything(t *testing.T) {
	t.Helper()
	for _, statement := range []string{
		`UPDATE oauth_authorization_codes SET expires_at = now() - INTERVAL '1 minute'`,
		`UPDATE oauth_refresh_tokens SET expires_at = now() - INTERVAL '1 minute'`,
	} {
		if _, err := e.pool.Exec(e.ctx, statement); err != nil {
			t.Fatalf("expire: %v", err)
		}
	}
}

func (e *oauthEnv) mustClient(t *testing.T, name string, redirects ...string) store.CreatedOAuthClient {
	t.Helper()
	if len(redirects) == 0 {
		redirects = []string{"https://claude.ai/api/mcp/auth_callback"}
	}
	created, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       e.user.ID,
		Name:         name,
		RedirectURIs: redirects,
	})
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}
	return created
}

func (e *oauthEnv) mustCode(t *testing.T, clientID uuid.UUID) string {
	t.Helper()
	code, err := e.store.CreateOAuthAuthorizationCode(e.ctx, store.CreateOAuthAuthorizationCodeParams{
		ClientID:      clientID,
		UserID:        e.user.ID,
		RedirectURI:   "https://claude.ai/api/mcp/auth_callback",
		CodeChallenge: "0123456789012345678901234567890123456789012",
		Scope:         model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("CreateOAuthAuthorizationCode: %v", err)
	}
	return code
}

func TestCreateOAuthClientReturnsASecretOnlyOnce(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)

	created := e.mustClient(t, "Claude")
	if created.RawSecret == "" || created.Client.ClientID == "" {
		t.Fatalf("created client missing credentials: %+v", created)
	}
	if created.Client.Name != "Claude" || len(created.Client.RedirectURIs) != 1 {
		t.Fatalf("created client = %+v", created.Client)
	}

	// Nothing that reads a client back exposes the secret, which is the whole
	// reason it is shown once at creation.
	clients, err := e.store.ListOAuthClientsForUser(e.ctx, e.user.ID)
	if err != nil {
		t.Fatalf("ListOAuthClientsForUser: %v", err)
	}
	if len(clients) != 1 || clients[0].ClientID != created.Client.ClientID {
		t.Fatalf("clients = %+v", clients)
	}
}

func TestCreateOAuthClientRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)

	if _, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       e.user.ID,
		Name:         "",
		RedirectURIs: []string{"https://example.com/cb"},
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("empty name error = %v, want ErrConflict", err)
	}
	if _, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       e.user.ID,
		Name:         "no redirects",
		RedirectURIs: []string{},
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("empty redirect list error = %v, want ErrConflict", err)
	}
	if _, err := e.store.CreateOAuthClient(e.ctx, store.CreateOAuthClientParams{
		UserID:       uuid.New(),
		Name:         "ghost owner",
		RedirectURIs: []string{"https://example.com/cb"},
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown owner error = %v, want ErrNotFound", err)
	}
}

func TestAuthenticateOAuthClient(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")

	client, err := e.store.AuthenticateOAuthClient(e.ctx, created.Client.ClientID, created.RawSecret)
	if err != nil {
		t.Fatalf("AuthenticateOAuthClient: %v", err)
	}
	if client.ID != created.Client.ID {
		t.Fatalf("client.ID = %s, want %s", client.ID, created.Client.ID)
	}
	if _, err := e.store.AuthenticateOAuthClient(e.ctx, created.Client.ClientID, "wrong"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("wrong secret error = %v, want ErrUnauthorized", err)
	}
	// An unknown client ID must be indistinguishable from a bad secret.
	if _, err := e.store.AuthenticateOAuthClient(e.ctx, "nope", created.RawSecret); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("unknown client error = %v, want ErrUnauthorized", err)
	}
}

func TestGetOAuthClientByClientIDHidesDisabledClients(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")

	if _, err := e.store.GetOAuthClientByClientID(e.ctx, created.Client.ClientID); err != nil {
		t.Fatalf("GetOAuthClientByClientID: %v", err)
	}
	if _, err := e.store.GetOAuthClientByClientID(e.ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown client error = %v, want ErrNotFound", err)
	}
	if err := e.store.DisableOAuthClientForUser(e.ctx, e.user.ID, created.Client.ID); err != nil {
		t.Fatalf("DisableOAuthClientForUser: %v", err)
	}
	if _, err := e.store.GetOAuthClientByClientID(e.ctx, created.Client.ClientID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disabled client error = %v, want ErrNotFound", err)
	}
	// The secret is scrubbed, so a disabled client cannot authenticate even if
	// the caller still holds the original.
	if _, err := e.store.AuthenticateOAuthClient(e.ctx, created.Client.ClientID, created.RawSecret); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("disabled client auth error = %v, want ErrUnauthorized", err)
	}
}

func TestDisableOAuthClientRevokesEverythingItHeld(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	code := e.mustCode(t, created.Client.ID)
	if _, err := e.store.ConsumeOAuthAuthorizationCode(e.ctx, code); err != nil {
		t.Fatalf("ConsumeOAuthAuthorizationCode: %v", err)
	}
	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID: created.Client.ID, ClientName: "Claude", UserID: e.user.ID, Scope: model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); err != nil {
		t.Fatalf("AuthenticateToken before revoke: %v", err)
	}

	if err := e.store.DisableOAuthClientForUser(e.ctx, e.user.ID, created.Client.ID); err != nil {
		t.Fatalf("DisableOAuthClientForUser: %v", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("access token after revoke = %v, want ErrUnauthorized", err)
	}
	if _, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, created.Client.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("refresh token after revoke = %v, want ErrUnauthorized", err)
	}
	// Consent is forgotten too, so reconnecting asks again.
	consented, err := e.store.OAuthClientConsented(e.ctx, created.Client.ID, e.user.ID)
	if err != nil {
		t.Fatalf("OAuthClientConsented: %v", err)
	}
	if consented {
		t.Fatal("revoking a client should forget its remembered consent")
	}
}

func TestDisableOAuthClientScopesToItsOwner(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")

	other, err := e.store.CreateUser(e.ctx, "other-"+uuid.NewString()+"@example.com", "other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := e.store.DisableOAuthClientForUser(e.ctx, other.ID, created.Client.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user revoke error = %v, want ErrNotFound", err)
	}
	if err := e.store.DisableOAuthClientForUser(e.ctx, e.user.ID, created.Client.ID); err != nil {
		t.Fatalf("DisableOAuthClientForUser: %v", err)
	}
	// Revoking twice is not a success: the second call found nothing to do.
	if err := e.store.DisableOAuthClientForUser(e.ctx, e.user.ID, created.Client.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second revoke error = %v, want ErrNotFound", err)
	}
}

func TestOAuthAuthorizationCodeIsSingleUse(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	code := e.mustCode(t, created.Client.ID)

	consumed, err := e.store.ConsumeOAuthAuthorizationCode(e.ctx, code)
	if err != nil {
		t.Fatalf("ConsumeOAuthAuthorizationCode: %v", err)
	}
	if consumed.ClientID != created.Client.ID || consumed.UserID != e.user.ID {
		t.Fatalf("consumed = %+v", consumed)
	}

	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID: created.Client.ID, ClientName: "Claude", UserID: e.user.ID, Scope: model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}

	// Replaying a code means a copy leaked, so everything it produced dies with
	// it rather than the second attempt merely being refused.
	if _, err := e.store.ConsumeOAuthAuthorizationCode(e.ctx, code); !errors.Is(err, store.ErrOAuthReplay) {
		t.Fatalf("replayed code error = %v, want ErrOAuthReplay", err)
	}
	if !errors.Is(store.ErrOAuthReplay, store.ErrUnauthorized) {
		t.Fatal("ErrOAuthReplay should wrap ErrUnauthorized so generic handlers still answer 401")
	}
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("access token after replay = %v, want ErrUnauthorized", err)
	}
}

func TestConsumeOAuthAuthorizationCodeRejectsUnknownAndExpired(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")

	if _, err := e.store.ConsumeOAuthAuthorizationCode(e.ctx, "never-issued"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("unknown code error = %v, want ErrUnauthorized", err)
	}

	code := e.mustCode(t, created.Client.ID)
	e.expireEverything(t)
	if _, err := e.store.ConsumeOAuthAuthorizationCode(e.ctx, code); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("expired code error = %v, want ErrUnauthorized", err)
	}
}

func TestRotateOAuthRefreshToken(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID: created.Client.ID, ClientName: "Claude", UserID: e.user.ID, Scope: model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}

	rotated, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, created.Client.ID)
	if err != nil {
		t.Fatalf("RotateOAuthRefreshToken: %v", err)
	}
	if rotated.RefreshToken == issued.RefreshToken || rotated.AccessToken == issued.AccessToken {
		t.Fatal("rotation must issue a fresh pair")
	}
	if _, err := e.store.AuthenticateToken(e.ctx, rotated.AccessToken); err != nil {
		t.Fatalf("AuthenticateToken on rotated access token: %v", err)
	}

	// Presenting a retired refresh token is the signal that a copy leaked.
	if _, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, created.Client.ID); !errors.Is(err, store.ErrOAuthReplay) {
		t.Fatalf("reused refresh token error = %v, want ErrOAuthReplay", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, rotated.AccessToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("access token after refresh replay = %v, want ErrUnauthorized", err)
	}
}

func TestRotateOAuthRefreshTokenRejectsTheWrongCaller(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	other := e.mustClient(t, "Other")
	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID: created.Client.ID, ClientName: "Claude", UserID: e.user.ID, Scope: model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}

	if _, err := e.store.RotateOAuthRefreshToken(e.ctx, "unknown", created.Client.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("unknown refresh token error = %v, want ErrUnauthorized", err)
	}
	// Another client holding the string is not entitled to rotate it, and is
	// told nothing that would confirm it exists.
	if _, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, other.Client.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("wrong client error = %v, want ErrUnauthorized", err)
	}

	e.expireEverything(t)
	if _, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, created.Client.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("expired refresh token error = %v, want ErrUnauthorized", err)
	}
}

func TestRevokeOAuthToken(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	other := e.mustClient(t, "Other")
	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID: created.Client.ID, ClientName: "Claude", UserID: e.user.ID, Scope: model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}

	// RFC 7009: an unknown token is still a success, so a client cannot probe
	// which tokens exist.
	if err := e.store.RevokeOAuthToken(e.ctx, "never-issued", created.Client.ID); err != nil {
		t.Fatalf("RevokeOAuthToken unknown: %v", err)
	}
	// Another client's token is not revoked on its say-so.
	if err := e.store.RevokeOAuthToken(e.ctx, issued.AccessToken, other.Client.ID); err != nil {
		t.Fatalf("RevokeOAuthToken wrong client: %v", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); err != nil {
		t.Fatalf("access token should survive another client's revoke: %v", err)
	}

	if err := e.store.RevokeOAuthToken(e.ctx, issued.AccessToken, created.Client.ID); err != nil {
		t.Fatalf("RevokeOAuthToken access: %v", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("revoked access token = %v, want ErrUnauthorized", err)
	}
	// The refresh token still works, because only the access token was revoked.
	rotated, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, created.Client.ID)
	if err != nil {
		t.Fatalf("RotateOAuthRefreshToken: %v", err)
	}

	// Revoking a refresh token is a disconnect, so it takes the grant with it.
	if err := e.store.RevokeOAuthToken(e.ctx, rotated.RefreshToken, created.Client.ID); err != nil {
		t.Fatalf("RevokeOAuthToken refresh: %v", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, rotated.AccessToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("access token after refresh revoke = %v, want ErrUnauthorized", err)
	}
}

func TestOAuthAccessTokensAuthenticateAsTheApprovingUser(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID: created.Client.ID, ClientName: "Claude", UserID: e.user.ID, Scope: model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}

	authenticated, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken)
	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if authenticated.User.ID != e.user.ID {
		t.Fatalf("user = %s, want %s", authenticated.User.ID, e.user.ID)
	}
	if authenticated.Token.Kind != model.AuthTokenKindOAuth {
		t.Fatalf("kind = %q, want %q", authenticated.Token.Kind, model.AuthTokenKindOAuth)
	}
	// An OAuth token always expires, unlike an API token, so a forgotten
	// connector cannot keep access indefinitely.
	if authenticated.Token.ExpiresAt == nil {
		t.Fatal("OAuth access tokens must carry an expiry")
	}
}

func TestOAuthClientConsentIsRememberedPerUser(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")

	consented, err := e.store.OAuthClientConsented(e.ctx, created.Client.ID, e.user.ID)
	if err != nil {
		t.Fatalf("OAuthClientConsented: %v", err)
	}
	if consented {
		t.Fatal("a brand new client should not be pre-approved")
	}

	e.mustCode(t, created.Client.ID)
	consented, err = e.store.OAuthClientConsented(e.ctx, created.Client.ID, e.user.ID)
	if err != nil {
		t.Fatalf("OAuthClientConsented: %v", err)
	}
	if !consented {
		t.Fatal("approving a client should be remembered")
	}

	// Approving twice must not collide on the uniqueness constraint.
	e.mustCode(t, created.Client.ID)

	other, err := e.store.CreateUser(e.ctx, "other-"+uuid.NewString()+"@example.com", "other")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	consented, err = e.store.OAuthClientConsented(e.ctx, created.Client.ID, other.ID)
	if err != nil {
		t.Fatalf("OAuthClientConsented: %v", err)
	}
	if consented {
		t.Fatal("one user's approval must not speak for another")
	}
}

func TestCreateOAuthAuthorizationCodeRejectsUnknownClient(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)

	if _, err := e.store.CreateOAuthAuthorizationCode(e.ctx, store.CreateOAuthAuthorizationCodeParams{
		ClientID:      uuid.New(),
		UserID:        e.user.ID,
		RedirectURI:   "https://claude.ai/api/mcp/auth_callback",
		CodeChallenge: "0123456789012345678901234567890123456789012",
		Scope:         model.OAuthScopeMCP,
	}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown client error = %v, want ErrNotFound", err)
	}

	created := e.mustClient(t, "Claude")
	// A challenge shorter than the PKCE minimum is refused by the schema.
	if _, err := e.store.CreateOAuthAuthorizationCode(e.ctx, store.CreateOAuthAuthorizationCodeParams{
		ClientID:      created.Client.ID,
		UserID:        e.user.ID,
		RedirectURI:   "https://claude.ai/api/mcp/auth_callback",
		CodeChallenge: "too-short",
		Scope:         model.OAuthScopeMCP,
	}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("short challenge error = %v, want ErrConflict", err)
	}
}

func TestListOAuthClientsForUserRejectsUnknownUser(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)

	if _, err := e.store.ListOAuthClientsForUser(e.ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user error = %v, want ErrNotFound", err)
	}
}

// The session sweep added in 0040 revokes stale browser sessions. Connector
// access tokens carry a real expiry and must not be caught by it, nor left
// behind by it.
func TestSessionSweepLeavesOAuthTokensAlone(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	issued, err := e.store.IssueOAuthTokens(e.ctx, store.IssueOAuthTokensParams{
		ClientID: created.Client.ID, ClientName: "Claude", UserID: e.user.ID, Scope: model.OAuthScopeMCP,
	})
	if err != nil {
		t.Fatalf("IssueOAuthTokens: %v", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if _, err := e.store.RevokeSessionAuthTokensForUser(e.ctx, e.user.ID); err != nil {
		t.Fatalf("RevokeSessionAuthTokensForUser: %v", err)
	}
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); err != nil {
		t.Fatalf("session revocation should not touch connector tokens: %v", err)
	}
}
