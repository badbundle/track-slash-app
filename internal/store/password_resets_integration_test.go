package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

const resetOldPassword = "correct-horse-battery"

// resetOldPasswordHash is resetOldPassword at bcrypt's lowest cost. These
// tests make many accounts, and at the default cost, under the race
// detector, hashing alone would take most of a test's deadline.
var resetOldPasswordHash = func() string {
	hash, err := bcrypt.GenerateFromPassword([]byte(resetOldPassword), bcrypt.MinCost)
	if err != nil {
		panic(err)
	}
	return string(hash)
}()

// resetAccount is a password account whose email is verified.
type resetAccount struct {
	user  model.User
	email string
}

// mustResetAccount makes an account like CreateAccount does, with
// resetOldPassword as its password, and verifies an email for it.
func mustResetAccount(t *testing.T, env *sprintsTestEnv, label string) resetAccount {
	t.Helper()
	user, err := env.store.CreateUserProfile(env.ctx, label+strings.ReplaceAll(uuid.NewString()[:13], "-", ""), "", "Reset User")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx, `
		INSERT INTO auth_credentials (user_id, kind, identifier, secret_hash)
		VALUES ($1, $2, $3, $4)
	`, user.ID, string(model.AuthCredentialKindPassword), user.Username, resetOldPasswordHash); err != nil {
		t.Fatalf("add password: %v", err)
	}
	email := "Reset-" + uuid.NewString()[:8] + "@Example.com"
	mustVerifyEmail(t, env, user.ID, email)
	return resetAccount{user: user, email: email}
}

func mustCreatePasswordReset(t *testing.T, env *sprintsTestEnv, a resetAccount) string {
	t.Helper()
	token, err := env.store.CreatePasswordReset(env.ctx, a.user.ID, a.email)
	if err != nil {
		t.Fatalf("CreatePasswordReset: %v", err)
	}
	return token
}

// mustPasskeyOnly gives the account a passkey and turns password sign-in off,
// so it signs in with the passkey alone.
func mustPasskeyOnly(t *testing.T, env *sprintsTestEnv, userID uuid.UUID) {
	t.Helper()
	if _, err := env.store.AddPasskeyCredential(env.ctx, userID, "localhost", "Laptop", testPasskeyCredential("reset-"+uuid.NewString(), 1)); err != nil {
		t.Fatalf("AddPasskeyCredential: %v", err)
	}
	if _, err := env.store.SetPasswordLoginEnabled(env.ctx, userID, false); err != nil {
		t.Fatalf("SetPasswordLoginEnabled: %v", err)
	}
}

func requireResetUnusable(t *testing.T, env *sprintsTestEnv, token, what string) {
	t.Helper()
	if _, err := env.store.CheckPasswordReset(env.ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%s: CheckPasswordReset = %v, want ErrNotFound", what, err)
	}
	if _, err := env.store.ResetPassword(env.ctx, token, "another-good-password"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%s: ResetPassword = %v, want ErrNotFound", what, err)
	}
}

// A reset request names an account by username or by verified email, either
// in any case, and only a live account with a verified email answers.
func TestFindPasswordResetAccount(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	a := mustResetAccount(t, env, "resetfind")

	for _, identifier := range []string{a.user.Username, "  " + strings.ToUpper(a.user.Username) + " ", a.email, strings.ToLower(a.email), " " + strings.ToUpper(a.email)} {
		got, err := env.store.FindPasswordResetAccount(env.ctx, identifier)
		if err != nil {
			t.Fatalf("FindPasswordResetAccount(%q): %v", identifier, err)
		}
		want := store.PasswordResetAccount{UserID: a.user.ID, Username: a.user.Username, Email: a.email, PasswordLogin: true}
		if got != want {
			t.Fatalf("FindPasswordResetAccount(%q) = %+v, want %+v", identifier, got, want)
		}
	}

	// An unverified address, by itself or behind its username, finds nothing.
	unverified := mustEmailUser(t, env, "unverified-"+uuid.NewString()[:8]+"@example.com")
	// A deleted account finds nothing either, even with a verified address.
	deleted := mustResetAccount(t, env, "resetgone")
	if err := env.store.DeleteUser(env.ctx, deleted.user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	for _, identifier := range []string{"", "   ", "nobody-" + uuid.NewString()[:8], "nobody@example.com", unverified.Username, unverified.Email, deleted.user.Username, deleted.email} {
		if got, err := env.store.FindPasswordResetAccount(env.ctx, identifier); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("FindPasswordResetAccount(%q) = %+v, %v, want ErrNotFound", identifier, got, err)
		}
	}

	// An account that signs in with a passkey alone is found, marked so.
	passkey := mustResetAccount(t, env, "resetpasskey")
	mustPasskeyOnly(t, env, passkey.user.ID)
	got, err := env.store.FindPasswordResetAccount(env.ctx, passkey.email)
	if err != nil || got.UserID != passkey.user.ID || got.PasswordLogin {
		t.Fatalf("passkey-only account = %+v, %v", got, err)
	}

	canceled, cancel := context.WithCancel(env.ctx)
	cancel()
	if _, err := env.store.FindPasswordResetAccount(canceled, a.user.Username); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("lookup without a database = %v", err)
	}
}

// A link sets a new password once. Checking it doesn't use it up, it is
// stored only as a hash, and using it ends every web session.
func TestResetPasswordSetsANewPasswordOnce(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	a := mustResetAccount(t, env, "resetonce")
	session, err := env.store.CreateAuthToken(env.ctx, store.CreateAuthTokenParams{UserID: a.user.ID, Kind: model.AuthTokenKindSession, Name: "web"})
	if err != nil {
		t.Fatalf("CreateAuthToken session: %v", err)
	}
	api, err := env.store.CreateAuthToken(env.ctx, store.CreateAuthTokenParams{UserID: a.user.ID, Kind: model.AuthTokenKindAPI, Name: "api"})
	if err != nil {
		t.Fatalf("CreateAuthToken api: %v", err)
	}
	reauth, err := env.store.CreatePasskeyReauthToken(env.ctx, a.user.ID)
	if err != nil {
		t.Fatalf("CreatePasskeyReauthToken: %v", err)
	}
	mustUpsertPushSubscription(t, env, a.user.ID, "https://push.example.test/"+uuid.NewString())

	token := mustCreatePasswordReset(t, env, a)
	if len(token) < 43 {
		t.Fatalf("token %q is shorter than 32 random bytes", token)
	}
	var storedHash []byte
	var storedEmail string
	if err := env.pool.QueryRow(env.ctx, `SELECT token_hash, email FROM password_resets WHERE user_id = $1`, a.user.ID).Scan(&storedHash, &storedEmail); err != nil {
		t.Fatalf("read stored link: %v", err)
	}
	if sum := sha256.Sum256([]byte(token)); !bytes.Equal(storedHash, sum[:]) || storedEmail != a.email {
		t.Fatalf("stored link = %x %q, want the token's SHA-256 and %q", storedHash, storedEmail, a.email)
	}
	var holdsToken bool
	if err := env.pool.QueryRow(env.ctx, `
		SELECT EXISTS (SELECT 1 FROM password_resets WHERE token_hash = $1 OR email = $2 OR position($1 in token_hash) > 0)
	`, []byte(token), token).Scan(&holdsToken); err != nil || holdsToken {
		t.Fatalf("the raw token is stored: %v %v", holdsToken, err)
	}

	for i := 0; i < 2; i++ {
		got, err := env.store.CheckPasswordReset(env.ctx, token)
		if err != nil || got.UserID != a.user.ID || got.Username != a.user.Username || got.Email != a.email || !got.PasswordLogin {
			t.Fatalf("CheckPasswordReset %d = %+v, %v", i, got, err)
		}
	}
	if _, err := env.store.ResetPassword(env.ctx, token, "short"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("short password = %v, want a validation error", err)
	}
	if _, err := env.store.CheckPasswordReset(env.ctx, token); err != nil {
		t.Fatalf("a refused password used up the link: %v", err)
	}

	const newPassword = "brand-new-horse-battery"
	got, err := env.store.ResetPassword(env.ctx, token, newPassword)
	if err != nil || got.UserID != a.user.ID || got.Username != a.user.Username || got.Email != a.email {
		t.Fatalf("ResetPassword = %+v, %v", got, err)
	}
	if _, err := env.store.AuthenticatePassword(env.ctx, a.user.Username, newPassword); err != nil {
		t.Fatalf("new password: %v", err)
	}
	if _, err := env.store.AuthenticatePassword(env.ctx, a.user.Username, resetOldPassword); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("old password = %v, want ErrUnauthorized", err)
	}
	if _, err := env.store.AuthenticateToken(env.ctx, session.RawToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("web session after reset = %v, want ErrUnauthorized", err)
	}
	if _, err := env.store.AuthenticateToken(env.ctx, api.RawToken); err != nil {
		t.Fatalf("API token after reset: %v", err)
	}
	if err := env.store.ConsumePasskeyReauthToken(env.ctx, a.user.ID, reauth); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("reauthentication after reset = %v, want ErrUnauthorized", err)
	}
	if count, err := env.store.CountActivePushSubscriptions(env.ctx, a.user.ID); err != nil || count != 0 {
		t.Fatalf("push subscriptions after reset = %d, %v", count, err)
	}
	requireResetUnusable(t, env, token, "used link")
}

// Requests racing to use one link set at most one password.
func TestResetPasswordLinkWorksOnceUnderARace(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	a := mustResetAccount(t, env, "resetrace")
	token := mustCreatePasswordReset(t, env, a)
	const racers = 4
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() {
			_, errs[i] = env.store.ResetPassword(env.ctx, token, fmt.Sprintf("racing-password-%d", i))
		})
	}
	wg.Wait()
	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner < 0:
			winner = i
		case err == nil:
			t.Fatalf("racers %d and %d both used the link", winner, i)
		case !errors.Is(err, store.ErrNotFound):
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no racer used the link")
	}
	if _, err := env.store.AuthenticatePassword(env.ctx, a.user.Username, fmt.Sprintf("racing-password-%d", winner)); err != nil {
		t.Fatalf("winning password: %v", err)
	}
}

// Asking again cancels the older link, and a link stops working after 30
// minutes.
func TestPasswordResetLinksExpireAndOnlyTheNewestWorks(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	a := mustResetAccount(t, env, "resetnewest")
	first := mustCreatePasswordReset(t, env, a)
	second := mustCreatePasswordReset(t, env, a)
	requireResetUnusable(t, env, first, "replaced link")
	if _, err := env.store.CheckPasswordReset(env.ctx, second); err != nil {
		t.Fatalf("newest link: %v", err)
	}
	var minutes float64
	if err := env.pool.QueryRow(env.ctx, `SELECT extract(epoch FROM expires_at - created_at) / 60 FROM password_resets WHERE user_id = $1`, a.user.ID).Scan(&minutes); err != nil || minutes != 30 {
		t.Fatalf("link lifetime = %v minutes, %v", minutes, err)
	}
	if _, err := env.pool.Exec(env.ctx, `UPDATE password_resets SET created_at = now() - interval '31 minutes', expires_at = now() - interval '1 minute' WHERE user_id = $1`, a.user.ID); err != nil {
		t.Fatalf("expire link: %v", err)
	}
	requireResetUnusable(t, env, second, "expired link")
	requireResetUnusable(t, env, "never-issued", "unknown link")
}

// A link is bound to the account and the verified address it was sent to:
// changing the password any other way, the address, or how the account signs
// in, stops it working.
func TestPasswordResetLinkStopsWhenTheAccountChanges(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	for _, tt := range []struct {
		name   string
		change func(t *testing.T, a resetAccount)
	}{
		{"password changed", func(t *testing.T, a resetAccount) {
			if err := env.store.ChangePassword(env.ctx, a.user.ID, resetOldPassword, "changed-horse-battery"); err != nil {
				t.Fatalf("ChangePassword: %v", err)
			}
		}},
		{"password sign-in turned off", func(t *testing.T, a resetAccount) {
			mustPasskeyOnly(t, env, a.user.ID)
		}},
		// The link is checked against how the account signs in when it is
		// used, not only when it is made.
		{"password turned off behind the store's back", func(t *testing.T, a resetAccount) {
			if _, err := env.pool.Exec(env.ctx, `UPDATE auth_credentials SET disabled_at = now() WHERE user_id = $1`, a.user.ID); err != nil {
				t.Fatalf("disable password: %v", err)
			}
		}},
		{"password revoked behind the store's back", func(t *testing.T, a resetAccount) {
			if _, err := env.pool.Exec(env.ctx, `UPDATE auth_credentials SET revoked_at = now() WHERE user_id = $1`, a.user.ID); err != nil {
				t.Fatalf("revoke password: %v", err)
			}
		}},
		{"email changed", func(t *testing.T, a resetAccount) {
			mustVerifyEmail(t, env, a.user.ID, "moved-"+uuid.NewString()[:8]+"@example.com")
		}},
		{"email verified again", func(t *testing.T, a resetAccount) {
			mustVerifyEmail(t, env, a.user.ID, a.email)
		}},
		{"email removed", func(t *testing.T, a resetAccount) {
			if _, err := env.store.RemoveUserEmail(env.ctx, a.user.ID); err != nil {
				t.Fatalf("RemoveUserEmail: %v", err)
			}
		}},
		{"email set unverified", func(t *testing.T, a resetAccount) {
			if _, err := env.store.UpdateUserProfile(env.ctx, a.user.ID, "Reset User", "typed-"+uuid.NewString()[:8]+"@example.com"); err != nil {
				t.Fatalf("UpdateUserProfile: %v", err)
			}
		}},
		{"account deleted", func(t *testing.T, a resetAccount) {
			if err := env.store.DeleteUser(env.ctx, a.user.ID); err != nil {
				t.Fatalf("DeleteUser: %v", err)
			}
		}},
	} {
		// Not parallel: every case shares env, whose context has a deadline.
		t.Run(tt.name, func(t *testing.T) {
			a := mustResetAccount(t, env, "resetbound")
			token := mustCreatePasswordReset(t, env, a)
			tt.change(t, a)
			requireResetUnusable(t, env, token, tt.name)
		})
	}

	// A new password set any other way, turning password sign-in off and
	// deleting the account all cancel the links still waiting.
	for _, tt := range []struct {
		name   string
		change func(a resetAccount) error
	}{
		{"password changed", func(a resetAccount) error {
			return env.store.ChangePassword(env.ctx, a.user.ID, resetOldPassword, "changed-horse-battery")
		}},
		{"password sign-in turned off", func(a resetAccount) error {
			mustPasskeyOnly(t, env, a.user.ID)
			return nil
		}},
		{"account deleted", func(a resetAccount) error { return env.store.DeleteUser(env.ctx, a.user.ID) }},
	} {
		a := mustResetAccount(t, env, "resetcancel")
		mustCreatePasswordReset(t, env, a)
		if err := tt.change(a); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var waiting int
		if err := env.pool.QueryRow(env.ctx, `SELECT count(*) FROM password_resets WHERE user_id = $1`, a.user.ID).Scan(&waiting); err != nil || waiting != 0 {
			t.Fatalf("%s: links still waiting = %d, %v", tt.name, waiting, err)
		}
	}
}

// Only an account that signs in with a password, at the verified address it
// still has, gets a link.
func TestCreatePasswordResetNeedsAPasswordAndTheVerifiedAddress(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	passkey := mustResetAccount(t, env, "resetpk")
	mustPasskeyOnly(t, env, passkey.user.ID)
	unverified := mustEmailUser(t, env, "plain-"+uuid.NewString()[:8]+"@example.com")
	deleted := mustResetAccount(t, env, "resetdel")
	if err := env.store.DeleteUser(env.ctx, deleted.user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	live := mustResetAccount(t, env, "resetlive")
	for _, tt := range []struct {
		name   string
		userID uuid.UUID
		email  string
	}{
		{"passkey only", passkey.user.ID, passkey.email},
		{"unverified", unverified.ID, unverified.Email},
		{"deleted", deleted.user.ID, deleted.email},
		{"another address", live.user.ID, "other@example.com"},
		{"unknown user", uuid.New(), live.email},
	} {
		if _, err := env.store.CreatePasswordReset(env.ctx, tt.userID, tt.email); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%s: CreatePasswordReset = %v, want ErrNotFound", tt.name, err)
		}
	}

	canceled, cancel := context.WithCancel(env.ctx)
	cancel()
	if _, err := env.store.CreatePasswordReset(canceled, live.user.ID, live.email); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("create without a database = %v", err)
	}
	token := mustCreatePasswordReset(t, env, live)
	if _, err := env.store.CheckPasswordReset(canceled, token); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("check without a database = %v", err)
	}
	if _, err := env.store.ResetPassword(canceled, token, "another-good-password"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reset without a database = %v", err)
	}
	if _, err := env.store.CheckPasswordReset(env.ctx, token); err != nil {
		t.Fatalf("a failed reset used up the link: %v", err)
	}
}
