package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/store"
)

func mustEmailUser(t *testing.T, env *sprintsTestEnv, email string) model.User {
	t.Helper()
	u, err := env.store.CreateUserProfile(env.ctx, "mail"+strings.ToLower(uniqueProjectKey(t))+strings.ReplaceAll(uuid.NewString()[:8], "-", ""), email, "Mail User")
	if err != nil {
		t.Fatalf("CreateUserProfile: %v", err)
	}
	return u
}

func mustEmailStatus(t *testing.T, env *sprintsTestEnv, userID uuid.UUID) model.EmailStatus {
	t.Helper()
	status, err := env.store.GetEmailStatus(env.ctx, userID)
	if err != nil {
		t.Fatalf("GetEmailStatus: %v", err)
	}
	return status
}

func mustCreateEmailVerification(t *testing.T, env *sprintsTestEnv, userID uuid.UUID, email string) store.CreatedEmailVerification {
	t.Helper()
	created, err := env.store.CreateEmailVerification(env.ctx, userID, email)
	if err != nil {
		t.Fatalf("CreateEmailVerification(%q): %v", email, err)
	}
	return created
}

func mustVerifyEmail(t *testing.T, env *sprintsTestEnv, userID uuid.UUID, email string) store.ConfirmedEmail {
	t.Helper()
	created := mustCreateEmailVerification(t, env, userID, email)
	confirmed, err := env.store.ConfirmEmailVerification(env.ctx, userID, created.Token)
	if err != nil {
		t.Fatalf("ConfirmEmailVerification(%q): %v", email, err)
	}
	return confirmed
}

func TestEmailVerificationConfirmsANewAddress(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	legacy := "legacy-" + uuid.NewString() + "@example.com"
	user := mustEmailUser(t, env, legacy)
	other := mustEmailUser(t, env, "")

	// Every address that existed before verification starts unverified.
	if status := mustEmailStatus(t, env, user.ID); status.Email != legacy || status.Verified() || status.Pending != nil {
		t.Fatalf("legacy status = %+v", status)
	}
	for _, bad := range []string{"", "not-an-address", "Ada <ada@example.com>", "a@example.com\r\nBcc: b@example.com"} {
		if _, err := env.store.CreateEmailVerification(env.ctx, user.ID, bad); !errors.Is(err, store.ErrInvalidEmail) {
			t.Fatalf("CreateEmailVerification(%q) = %v, want ErrInvalidEmail", bad, err)
		}
	}

	next := "next-" + uuid.NewString() + "@example.com"
	created := mustCreateEmailVerification(t, env, user.ID, "  "+next+"  ")
	if created.Token == "" || created.Verification.Email != next || created.Verification.UserID != user.ID {
		t.Fatalf("created = %+v", created)
	}
	if ttl := created.Verification.ExpiresAt.Sub(created.Verification.CreatedAt); ttl != store.EmailVerificationTTL {
		t.Fatalf("link lifetime = %s, want %s", ttl, store.EmailVerificationTTL)
	}
	var stored []byte
	if err := env.pool.QueryRow(env.ctx, `SELECT token_hash FROM email_verifications WHERE id = $1`, created.Verification.ID).Scan(&stored); err != nil || strings.Contains(string(stored), created.Token) {
		t.Fatalf("stored token = %q, %v; want only a hash", stored, err)
	}
	// Until it is confirmed, the address in effect stays as it was.
	status := mustEmailStatus(t, env, user.ID)
	if status.Email != legacy || status.Pending == nil || status.Pending.Email != next || status.Pending.Sent {
		t.Fatalf("pending status = %+v (%+v)", status, status.Pending)
	}
	if err := env.store.MarkEmailVerificationSent(env.ctx, created.Verification.ID); err != nil {
		t.Fatalf("MarkEmailVerificationSent: %v", err)
	}
	if status := mustEmailStatus(t, env, user.ID); !status.Pending.Sent {
		t.Fatalf("sent status = %+v", status.Pending)
	}

	looked, err := env.store.LookupEmailVerification(env.ctx, user.ID, created.Token)
	if err != nil || looked.ID != created.Verification.ID || !looked.Usable(time.Now()) || looked.SentAt == nil {
		t.Fatalf("LookupEmailVerification = %+v, %v", looked, err)
	}
	// A link is its user's alone.
	if _, err := env.store.LookupEmailVerification(env.ctx, other.ID, created.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user's lookup = %v, want ErrNotFound", err)
	}
	if _, err := env.store.ConfirmEmailVerification(env.ctx, other.ID, created.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("other user's confirm = %v, want ErrNotFound", err)
	}
	if _, err := env.store.LookupEmailVerification(env.ctx, user.ID, "not-a-token"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown token lookup = %v, want ErrNotFound", err)
	}

	confirmed, err := env.store.ConfirmEmailVerification(env.ctx, user.ID, created.Token)
	// The legacy address was never verified, so nobody is told it changed.
	if err != nil || confirmed.Email != next || confirmed.Previous != "" {
		t.Fatalf("ConfirmEmailVerification = %+v, %v", confirmed, err)
	}
	status = mustEmailStatus(t, env, user.ID)
	if status.Email != next || !status.Verified() || status.Pending != nil {
		t.Fatalf("confirmed status = %+v", status)
	}
	if got, err := env.store.GetUser(env.ctx, user.ID); err != nil || got.Email != next {
		t.Fatalf("user after confirm = %+v, %v", got, err)
	}

	// A link works once.
	if _, err := env.store.ConfirmEmailVerification(env.ctx, user.ID, created.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reused link = %v, want ErrNotFound", err)
	}
	if used, err := env.store.LookupEmailVerification(env.ctx, user.ID, created.Token); err != nil || used.UsedAt == nil || used.Usable(time.Now()) {
		t.Fatalf("used link lookup = %+v, %v", used, err)
	}

	// Replacing a verified address names the old one, to be told.
	third := "third-" + uuid.NewString() + "@example.com"
	if confirmed := mustVerifyEmail(t, env, user.ID, third); confirmed.Previous != next {
		t.Fatalf("replacing a verified address = %+v, want previous %q", confirmed, next)
	}
	// A change of case is the same mailbox, so there is nobody else to tell.
	if confirmed := mustVerifyEmail(t, env, user.ID, strings.ToUpper(third[:5])+third[5:]); confirmed.Previous != "" {
		t.Fatalf("case-only change = %+v, want no previous", confirmed)
	}
}

func TestEmailVerificationLinksExpireAndOnlyTheNewestWorks(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	user := mustEmailUser(t, env, "")

	first := mustCreateEmailVerification(t, env, user.ID, "first-"+uuid.NewString()+"@example.com")
	second := mustCreateEmailVerification(t, env, user.ID, "second-"+uuid.NewString()+"@example.com")
	// Asking again replaces the link still waiting.
	if _, err := env.store.ConfirmEmailVerification(env.ctx, user.ID, first.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("replaced link = %v, want ErrNotFound", err)
	}
	if status := mustEmailStatus(t, env, user.ID); status.Pending == nil || status.Pending.Email != second.Verification.Email {
		t.Fatalf("pending after replacing = %+v", status.Pending)
	}

	if _, err := env.pool.Exec(env.ctx, `UPDATE email_verifications SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE id = $1`, second.Verification.ID); err != nil {
		t.Fatalf("expire link: %v", err)
	}
	if _, err := env.store.ConfirmEmailVerification(env.ctx, user.ID, second.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired link = %v, want ErrNotFound", err)
	}
	if expired, err := env.store.LookupEmailVerification(env.ctx, user.ID, second.Token); err != nil || expired.UsedAt != nil || expired.Usable(time.Now()) {
		t.Fatalf("expired link lookup = %+v, %v", expired, err)
	}
	if status := mustEmailStatus(t, env, user.ID); status.Email != "" || status.Verified() || status.Pending != nil {
		t.Fatalf("status after expiry = %+v", status)
	}
}

// Only a verified address is unique, without regard to case. An address
// typed but never confirmed, or still waiting, keeps nobody out.
func TestOnlyVerifiedEmailsAreUnique(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	shared := "Shared-" + uuid.NewString() + "@Example.com"
	squatter := mustEmailUser(t, env, shared)
	owner := mustEmailUser(t, env, strings.ToLower(shared))
	rival := mustEmailUser(t, env, "")

	ownerLink := mustCreateEmailVerification(t, env, owner.ID, shared)
	rivalLink := mustCreateEmailVerification(t, env, rival.ID, strings.ToUpper(shared))
	if confirmed, err := env.store.ConfirmEmailVerification(env.ctx, owner.ID, ownerLink.Token); err != nil || confirmed.Email != shared {
		t.Fatalf("owner confirm = %+v, %v", confirmed, err)
	}
	// The unverified holder keeps the address it typed; it just isn't verified.
	if status := mustEmailStatus(t, env, squatter.ID); status.Email != shared || status.Verified() {
		t.Fatalf("squatter status = %+v", status)
	}

	if _, err := env.store.ConfirmEmailVerification(env.ctx, rival.ID, rivalLink.Token); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second verification of the address = %v, want ErrConflict", err)
	}
	// Nothing changed, and the link was not used up.
	if status := mustEmailStatus(t, env, rival.ID); status.Email != "" || status.Verified() || status.Pending == nil {
		t.Fatalf("rival status after conflict = %+v", status)
	}
	// Once the owner's account is deleted, the address is free again.
	if err := env.store.DeleteUser(env.ctx, owner.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if confirmed, err := env.store.ConfirmEmailVerification(env.ctx, rival.ID, rivalLink.Token); err != nil || confirmed.Email != strings.ToUpper(shared) {
		t.Fatalf("rival confirm after delete = %+v, %v", confirmed, err)
	}
}

func TestRemoveUserEmail(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	verified := mustEmailUser(t, env, "")
	address := "remove-" + uuid.NewString() + "@example.com"
	mustVerifyEmail(t, env, verified.ID, address)
	mustCreateEmailVerification(t, env, verified.ID, "waiting-"+uuid.NewString()+"@example.com")

	previous, err := env.store.RemoveUserEmail(env.ctx, verified.ID)
	if err != nil || previous != address {
		t.Fatalf("RemoveUserEmail verified = %q, %v", previous, err)
	}
	if status := mustEmailStatus(t, env, verified.ID); status.Email != "" || status.Verified() || status.Pending != nil {
		t.Fatalf("status after removal = %+v", status)
	}

	unverified := mustEmailUser(t, env, "unverified-"+uuid.NewString()+"@example.com")
	if previous, err := env.store.RemoveUserEmail(env.ctx, unverified.ID); err != nil || previous != "" {
		t.Fatalf("RemoveUserEmail unverified = %q, %v", previous, err)
	}
	if got, err := env.store.GetUser(env.ctx, unverified.ID); err != nil || got.Email != "" {
		t.Fatalf("unverified user after removal = %+v, %v", got, err)
	}
}

func TestEmailVerificationUnknownUser(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	missing := uuid.New()
	if _, err := env.store.GetEmailStatus(env.ctx, missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetEmailStatus = %v", err)
	}
	if _, err := env.store.CreateEmailVerification(env.ctx, missing, "a@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateEmailVerification = %v", err)
	}
	if _, err := env.store.ConfirmEmailVerification(env.ctx, missing, "token"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ConfirmEmailVerification = %v", err)
	}
	if _, err := env.store.RemoveUserEmail(env.ctx, missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RemoveUserEmail = %v", err)
	}
	if _, err := env.store.UpdateUserName(env.ctx, missing, "Nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateUserName = %v", err)
	}
	if _, err := env.store.UpdateUserProfile(env.ctx, missing, "Nobody", "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateUserProfile = %v", err)
	}
	// A deleted account's links stop working with it.
	user := mustEmailUser(t, env, "")
	created := mustCreateEmailVerification(t, env, user.ID, "deleted-"+uuid.NewString()+"@example.com")
	if err := env.store.DeleteUser(env.ctx, user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := env.store.ConfirmEmailVerification(env.ctx, user.ID, created.Token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted user's confirm = %v", err)
	}
}

func TestUpdateUserProfileResetsVerificationOnlyWhenTheAddressChanges(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	user := mustEmailUser(t, env, "")
	address := "profile-" + uuid.NewString() + "@example.com"
	mustVerifyEmail(t, env, user.ID, address)

	if _, err := env.store.UpdateUserProfile(env.ctx, user.ID, "Renamed", address); err != nil {
		t.Fatalf("UpdateUserProfile same address: %v", err)
	}
	if status := mustEmailStatus(t, env, user.ID); !status.Verified() {
		t.Fatalf("unchanged address lost its verification: %+v", status)
	}
	changed := "changed-" + uuid.NewString() + "@example.com"
	if _, err := env.store.UpdateUserProfile(env.ctx, user.ID, "Renamed", changed); err != nil {
		t.Fatalf("UpdateUserProfile new address: %v", err)
	}
	if status := mustEmailStatus(t, env, user.ID); status.Email != changed || status.Verified() {
		t.Fatalf("new address status = %+v", status)
	}
	// An unverified address no longer conflicts with anyone's.
	twin := mustEmailUser(t, env, "")
	if got, err := env.store.UpdateUserProfile(env.ctx, twin.ID, "Twin", changed); err != nil || got.Email != changed {
		t.Fatalf("UpdateUserProfile shared unverified address = %+v, %v", got, err)
	}
	if got, err := env.store.UpdateUserProfile(env.ctx, twin.ID, "Twin", ""); err != nil || got.Email != "" {
		t.Fatalf("UpdateUserProfile clear = %+v, %v", got, err)
	}
}

func TestUpdateUserNameKeepsTheEmail(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)
	user := mustEmailUser(t, env, "")
	address := "name-" + uuid.NewString() + "@example.com"
	mustVerifyEmail(t, env, user.ID, address)
	got, err := env.store.UpdateUserName(env.ctx, user.ID, "  New Name  ")
	if err != nil || got.Name != "New Name" || got.Email != address {
		t.Fatalf("UpdateUserName = %+v, %v", got, err)
	}
	if status := mustEmailStatus(t, env, user.ID); !status.Verified() {
		t.Fatalf("UpdateUserName lost the verification: %+v", status)
	}
	if _, err := env.store.UpdateUserName(env.ctx, user.ID, "  "); err == nil {
		t.Fatal("UpdateUserName blank = nil error")
	}
}

// With the email column no longer unique, bootstrapping an admin by email
// still never guesses between accounts.
func TestAdminBootstrapWithSharedEmails(t *testing.T) {
	t.Parallel()
	env := newSprintsEnv(t)

	shared := "shared-admin-" + uuid.NewString() + "@example.com"
	first := mustEmailUser(t, env, shared)
	mustEmailUser(t, env, shared)
	if _, err := env.store.PromoteUserToAdminByEmail(env.ctx, shared); !errors.Is(err, store.ErrEmailHeldBySeveralAccounts) || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("PromoteUserToAdminByEmail shared = %v, want ErrEmailHeldBySeveralAccounts", err)
	}
	if _, err := env.store.CreateOrUpdateAdminUser(env.ctx, shared, "Operator"); !errors.Is(err, store.ErrEmailBelongsToNonAdmin) {
		t.Fatalf("CreateOrUpdateAdminUser over non-admins = %v", err)
	}
	if got, err := env.store.GetUser(env.ctx, first.ID); err != nil || got.IsAdmin {
		t.Fatalf("first holder after refusals = %+v, %v", got, err)
	}

	// Two admins with one address can't be told apart either.
	twins := "twin-admins-" + uuid.NewString() + "@example.com"
	for i := 0; i < 2; i++ {
		if _, err := env.pool.Exec(env.ctx, `INSERT INTO users (username, email, name, is_admin) VALUES ($1, $2, 'Twin', true)`, "twin"+strings.ToLower(uniqueProjectKey(t))+uuid.NewString()[:4], twins); err != nil {
			t.Fatalf("insert twin admin: %v", err)
		}
	}
	if _, err := env.store.CreateOrUpdateAdminUser(env.ctx, twins, "Operator"); !errors.Is(err, store.ErrEmailHeldBySeveralAccounts) {
		t.Fatalf("CreateOrUpdateAdminUser over two admins = %v", err)
	}

	// A deleted account still blocks the address, as before.
	deletedHolder := "deleted-holder-" + uuid.NewString() + "@example.com"
	gone := mustEmailUser(t, env, deletedHolder)
	if err := env.store.DeleteUser(env.ctx, gone.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := env.store.CreateOrUpdateAdminUser(env.ctx, deletedHolder, "Operator"); !errors.Is(err, store.ErrEmailBelongsToDeletedUser) {
		t.Fatalf("CreateOrUpdateAdminUser over a deleted non-admin = %v", err)
	}
	if _, err := env.store.PromoteUserToAdminByEmail(env.ctx, deletedHolder); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("PromoteUserToAdminByEmail over a deleted holder = %v, want ErrNotFound", err)
	}

	// The email names the admin's username, which another account may have.
	taken := "taken" + strings.ToLower(uniqueProjectKey(t))
	if _, err := env.store.CreateUserProfile(env.ctx, taken, "", "Taken"); err != nil {
		t.Fatalf("CreateUserProfile taken: %v", err)
	}
	if _, err := env.store.CreateOrUpdateAdminUser(env.ctx, taken+"@example.com", "Operator"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("CreateOrUpdateAdminUser over a taken username = %v, want ErrConflict", err)
	}

	// Refreshing a live admin keeps its verification; a deleted one stays
	// deleted.
	adminEmail := "bootstrap-" + uuid.NewString() + "@example.com"
	admin, err := env.store.CreateOrUpdateAdminUser(env.ctx, adminEmail, "Operator")
	if err != nil || !admin.IsAdmin {
		t.Fatalf("CreateOrUpdateAdminUser new = %+v, %v", admin, err)
	}
	mustVerifyEmail(t, env, admin.ID, adminEmail)
	if again, err := env.store.CreateOrUpdateAdminUser(env.ctx, adminEmail, "Operator Again"); err != nil || again.ID != admin.ID || again.Name != "Operator Again" {
		t.Fatalf("CreateOrUpdateAdminUser refresh = %+v, %v", again, err)
	}
	if status := mustEmailStatus(t, env, admin.ID); !status.Verified() {
		t.Fatalf("refreshed admin lost its verification: %+v", status)
	}
	if err := env.store.DeleteUser(env.ctx, admin.ID); err != nil {
		t.Fatalf("DeleteUser admin: %v", err)
	}
	if _, err := env.store.CreateOrUpdateAdminUser(env.ctx, adminEmail, "Operator"); !errors.Is(err, store.ErrEmailBelongsToDeletedUser) {
		t.Fatalf("CreateOrUpdateAdminUser over a deleted admin = %v, want ErrEmailBelongsToDeletedUser", err)
	}
	if _, err := env.store.GetUser(env.ctx, admin.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted admin came back: %v", err)
	}

	// Someone who verified a deleted admin's address while it was free holds
	// it now, and is not promoted by the bootstrap.
	taker := mustEmailUser(t, env, "")
	mustVerifyEmail(t, env, taker.ID, adminEmail)
	if _, err := env.store.CreateOrUpdateAdminUser(env.ctx, adminEmail, "Operator"); !errors.Is(err, store.ErrEmailBelongsToNonAdmin) {
		t.Fatalf("CreateOrUpdateAdminUser over a verified non-admin = %v", err)
	}
}
