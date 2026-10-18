package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

// Deleting an account ends every way into it in the same transaction, rather
// than leaving its credentials live behind the deleted_at filter on each
// sign-in path.
func TestDeleteUserEndsEveryCredential(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	// The connector belongs to someone else, so it outlives the deleted user
	// and only the deleted user's own grant has to end.
	client := e.mustClient(t, "Claude")
	gone, err := e.store.CreateAccount(e.ctx, store.CreateAccountParams{
		Username: "gone" + strings.ToLower(uniqueProjectKey(t)),
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := e.store.AddPasskeyCredential(e.ctx, gone.ID, "localhost", "Laptop", testPasskeyCredential("gone-"+uuid.NewString(), 1)); err != nil {
		t.Fatalf("AddPasskeyCredential: %v", err)
	}
	for _, kind := range []model.AuthTokenKind{model.AuthTokenKindAPI, model.AuthTokenKindSession} {
		if _, err := e.store.CreateAuthToken(e.ctx, store.CreateAuthTokenParams{UserID: gone.ID, Kind: kind, Name: string(kind)}); err != nil {
			t.Fatalf("CreateAuthToken %s: %v", kind, err)
		}
	}
	code := func() string {
		t.Helper()
		code, err := e.store.CreateOAuthAuthorizationCode(e.ctx, store.CreateOAuthAuthorizationCodeParams{
			ClientID: client.Client.ID, UserID: gone.ID, RedirectURI: "https://claude.ai/api/mcp/auth_callback",
			CodeChallenge: "0123456789012345678901234567890123456789012", Scope: model.OAuthScopeMCP,
		})
		if err != nil {
			t.Fatalf("CreateOAuthAuthorizationCode: %v", err)
		}
		return code
	}
	if _, err := e.store.ExchangeOAuthAuthorizationCode(e.ctx, code(), client.Client, acceptCode); err != nil {
		t.Fatalf("ExchangeOAuthAuthorizationCode: %v", err)
	}
	code() // approved again, but never exchanged

	if err := e.store.DeleteUser(e.ctx, gone.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	var tokens, refreshTokens, consents, codes, credentials int
	if err := e.pool.QueryRow(e.ctx, `
		SELECT
			(SELECT count(*) FROM auth_tokens WHERE user_id = $1 AND revoked_at IS NULL),
			(SELECT count(*) FROM oauth_refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL),
			(SELECT count(*) FROM oauth_client_consents WHERE user_id = $1),
			(SELECT count(*) FROM oauth_authorization_codes WHERE user_id = $1),
			(SELECT count(*) FROM auth_credentials WHERE user_id = $1 AND revoked_at IS NULL)
	`, gone.ID).Scan(&tokens, &refreshTokens, &consents, &codes, &credentials); err != nil {
		t.Fatalf("count live credentials: %v", err)
	}
	if tokens != 0 || refreshTokens != 0 || consents != 0 || codes != 0 || credentials != 0 {
		t.Fatalf("live after delete: tokens %d, refresh tokens %d, consents %d, codes %d, credentials %d; want none",
			tokens, refreshTokens, consents, codes, credentials)
	}
	// Someone else's connector is not theirs to take down.
	if _, err := e.store.GetOAuthClientByClientID(e.ctx, client.Client.ClientID); err != nil {
		t.Fatalf("registrant's client after another user is deleted: %v", err)
	}
}

// Bootstrapping an admin by the email of a deleted account must not bring that
// account, and every credential it held, back to life.
func TestCreateOrUpdateAdminUserRefusesADeletedAccount(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	email := "deleted-admin-" + uuid.NewString() + "@example.com"
	admin, err := env.store.CreateOrUpdateAdminUser(env.ctx, email, "Admin")
	if err != nil {
		t.Fatalf("CreateOrUpdateAdminUser: %v", err)
	}
	token, err := env.store.CreateAuthToken(env.ctx, store.CreateAuthTokenParams{UserID: admin.ID, Kind: model.AuthTokenKindAPI, Name: "bootstrap"})
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}
	if err := env.store.DeleteUser(env.ctx, admin.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, err = env.store.CreateOrUpdateAdminUser(env.ctx, email, "Admin")
	if !errors.Is(err, store.ErrEmailBelongsToDeletedUser) || !errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrEmailBelongsToNonAdmin) {
		t.Fatalf("CreateOrUpdateAdminUser over a deleted admin = %v, want ErrEmailBelongsToDeletedUser", err)
	}
	if _, err := env.store.GetUser(env.ctx, admin.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted admin after bootstrap = %v, want ErrNotFound", err)
	}
	if _, err := env.store.AuthenticateToken(env.ctx, token.RawToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("deleted admin's token after bootstrap = %v, want ErrUnauthorized", err)
	}

	// A deleted non-admin's email is refused the same way, rather than as an
	// account -promote-existing could promote.
	other := "deleted-member-" + uuid.NewString() + "@example.com"
	member, err := env.store.CreateUserProfile(env.ctx, "deletedmember"+strings.ToLower(uniqueProjectKey(t)), other, "Member")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	if err := env.store.DeleteUser(env.ctx, member.ID); err != nil {
		t.Fatalf("DeleteUser member: %v", err)
	}
	if _, err := env.store.CreateOrUpdateAdminUser(env.ctx, other, "Admin"); !errors.Is(err, store.ErrEmailBelongsToDeletedUser) {
		t.Fatalf("CreateOrUpdateAdminUser over a deleted member = %v, want ErrEmailBelongsToDeletedUser", err)
	}
}
