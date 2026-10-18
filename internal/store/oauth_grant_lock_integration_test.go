package store_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bradleymackey/track-slash/internal/store"
)

// These tests hold a row in a transaction of their own to stop a concurrent
// call at a known point, then wait until Postgres reports it blocked there, so
// each interleaving happens every run rather than by luck.

// hold runs statement in a transaction the test keeps open until the returned
// release is called, keeping whatever row locks it took.
func (e *oauthEnv) hold(t *testing.T, statement string, args ...any) (pgx.Tx, func()) {
	t.Helper()
	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := tx.Exec(e.ctx, statement, args...); err != nil {
		t.Fatalf("hold %q: %v", statement, err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if err := tx.Commit(e.ctx); err != nil {
				t.Errorf("release: %v", err)
			}
		})
	}
	t.Cleanup(release)
	return tx, release
}

// holdClient stops an exchange or refresh as it inserts the tokens it issues,
// with every lock it took before that still held: inserting a token checks
// the client as its foreign key.
func (e *oauthEnv) holdClient(t *testing.T, clientID uuid.UUID) func() {
	t.Helper()
	_, release := e.hold(t, `SELECT 1 FROM oauth_clients WHERE id = $1 FOR UPDATE`, clientID)
	return release
}

// waitForLockWaits waits until n statements in this test's database are
// blocked on a lock.
func (e *oauthEnv) waitForLockWaits(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := e.pool.QueryRow(e.ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
		`).Scan(&waiting); err != nil {
			t.Fatalf("count lock waits: %v", err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock waits = %d, want %d", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type oauthIssueOutcome struct {
	issued store.IssuedOAuthTokens
	err    error
}

func (e *oauthEnv) requireGrantEnded(t *testing.T, clientID uuid.UUID, issued store.IssuedOAuthTokens) {
	t.Helper()
	if _, err := e.store.AuthenticateToken(e.ctx, issued.AccessToken); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("access token issued during the disconnect = %v, want ErrUnauthorized", err)
	}
	if _, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, clientID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("refresh token issued during the disconnect = %v, want ErrUnauthorized", err)
	}
}

// A user disconnects a connector while it is refreshing. The disconnect waits
// for the refresh, then ends the pair it issued as well, so the connector
// cannot carry on with a refresh token the disconnect never saw.
func TestDisconnectEndsTheTokensOfARefreshInFlight(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	issued := e.mustTokens(t, created.Client)

	release := e.holdClient(t, created.Client.ID)
	rotated := make(chan oauthIssueOutcome, 1)
	go func() {
		next, err := e.store.RotateOAuthRefreshToken(e.ctx, issued.RefreshToken, created.Client.ID)
		rotated <- oauthIssueOutcome{next, err}
	}()
	e.waitForLockWaits(t, 1)
	disconnected := make(chan error, 1)
	go func() { disconnected <- e.store.DisconnectOAuthGrantForUser(e.ctx, e.user.ID, created.Client.ID) }()
	e.waitForLockWaits(t, 2)
	release()

	got := <-rotated
	if got.err != nil {
		t.Fatalf("RotateOAuthRefreshToken: %v", got.err)
	}
	if err := <-disconnected; err != nil {
		t.Fatalf("DisconnectOAuthGrantForUser: %v", err)
	}
	e.requireGrantEnded(t, created.Client.ID, got.issued)
}

// The same race for a connector's first tokens: the code is spent and the
// tokens issued in one transaction, and the disconnect ends them both.
func TestDisconnectEndsTheTokensOfAnExchangeInFlight(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	code := e.mustCode(t, created.Client.ID)

	release := e.holdClient(t, created.Client.ID)
	exchanged := make(chan oauthIssueOutcome, 1)
	go func() {
		issued, err := e.store.ExchangeOAuthAuthorizationCode(e.ctx, code, created.Client, acceptCode)
		exchanged <- oauthIssueOutcome{issued, err}
	}()
	e.waitForLockWaits(t, 1)
	disconnected := make(chan error, 1)
	go func() { disconnected <- e.store.DisconnectOAuthGrantForUser(e.ctx, e.user.ID, created.Client.ID) }()
	e.waitForLockWaits(t, 2)
	release()

	got := <-exchanged
	if got.err != nil {
		t.Fatalf("ExchangeOAuthAuthorizationCode: %v", got.err)
	}
	if err := <-disconnected; err != nil {
		t.Fatalf("DisconnectOAuthGrantForUser: %v", err)
	}
	e.requireGrantEnded(t, created.Client.ID, got.issued)
}

// The other order: an exchange that arrives while a disconnect is under way
// waits for it, then finds the approval gone and issues nothing.
func TestExchangeDuringADisconnectIssuesNothing(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	e.mustTokens(t, created.Client)
	code := e.mustCode(t, created.Client.ID)

	// Holding the grant's refresh token stops the disconnect just after it
	// has deleted the approval, before it reaches the outstanding code.
	_, release := e.hold(t, `SELECT 1 FROM oauth_refresh_tokens WHERE client_id = $1 FOR UPDATE`, created.Client.ID)
	disconnected := make(chan error, 1)
	go func() { disconnected <- e.store.DisconnectOAuthGrantForUser(e.ctx, e.user.ID, created.Client.ID) }()
	e.waitForLockWaits(t, 1)
	exchanged := make(chan error, 1)
	go func() {
		_, err := e.store.ExchangeOAuthAuthorizationCode(e.ctx, code, created.Client, acceptCode)
		exchanged <- err
	}()
	e.waitForLockWaits(t, 2)
	release()

	if err := <-disconnected; err != nil {
		t.Fatalf("DisconnectOAuthGrantForUser: %v", err)
	}
	if err := <-exchanged; !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("exchange during the disconnect = %v, want ErrUnauthorized", err)
	}
	if grants, err := e.store.ListOAuthGrantsForUser(e.ctx, e.user.ID); err != nil || len(grants) != 0 {
		t.Fatalf("grants after the disconnect = %+v, %v; want none", grants, err)
	}
}

// A code can vanish between the exchange reading it and locking it only to the
// purge of long-expired codes. That is an invalid code, not a server error.
func TestExchangeOfACodeDeletedMidwayIsRefused(t *testing.T) {
	t.Parallel()
	e := newOAuthEnv(t)
	created := e.mustClient(t, "Claude")
	code := e.mustCode(t, created.Client.ID)

	tx, release := e.hold(t, `SELECT 1 FROM oauth_client_consents WHERE client_id = $1 FOR UPDATE`, created.Client.ID)
	exchanged := make(chan error, 1)
	go func() {
		_, err := e.store.ExchangeOAuthAuthorizationCode(e.ctx, code, created.Client, acceptCode)
		exchanged <- err
	}()
	e.waitForLockWaits(t, 1)
	if _, err := tx.Exec(e.ctx, `DELETE FROM oauth_authorization_codes WHERE client_id = $1`, created.Client.ID); err != nil {
		t.Fatalf("delete code: %v", err)
	}
	release()

	if err := <-exchanged; !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("exchange of a deleted code = %v, want ErrUnauthorized", err)
	}
}
