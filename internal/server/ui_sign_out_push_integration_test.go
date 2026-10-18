package server_test

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/store"
	"github.com/google/uuid"
)

const wantSignOutClearSiteData = `"cache", "storage"`

func (e *httpEnv) mustPushSubscription(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	endpoint := "https://push.example.test/" + uuid.NewString()
	if _, err := e.store.UpsertPushSubscription(e.ctx, store.UpsertPushSubscriptionParams{
		UserID: userID, Endpoint: endpoint,
		P256DH:     base64.RawURLEncoding.EncodeToString(make([]byte, 65)),
		AuthSecret: base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
	}); err != nil {
		t.Fatalf("UpsertPushSubscription: %v", err)
	}
	return endpoint
}

func (e *httpEnv) requirePushSubscriptionActive(t *testing.T, label string, userID uuid.UUID, endpoint string, want bool) {
	t.Helper()
	active, err := e.store.PushSubscriptionActive(e.ctx, userID, endpoint)
	if err != nil || active != want {
		t.Fatalf("%s: subscription active = %v, %v; want %v", label, active, err, want)
	}
}

// Signing out stops the notifications this browser would otherwise keep
// showing, and only this browser's: the account's other browsers are still
// signed in.
func TestLogoutDisablesThisBrowsersPushSubscription(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustSessionToken(t, "logout-push")
	_, otherAccountSession := e.mustSessionToken(t, "logout-push-other-account")
	stranger, _ := e.mustUserToken(t, "logout-push-stranger")
	thisBrowser := e.mustPushSubscription(t, user.ID)
	otherBrowser := e.mustPushSubscription(t, user.ID)
	strangersBrowser := e.mustPushSubscription(t, stranger.ID)

	logout := func(session, endpoint string) {
		t.Helper()
		form := url.Values{"push_endpoint": {endpoint}}
		if session != "" {
			form.Set("csrf_token", uiCSRFTokenForTest("session", session))
		}
		res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/logout", session,
			strings.NewReader(form.Encode()), map[string]string{"Origin": e.ts.URL, "X-CSRF-Token": ""})
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
			t.Fatalf("logout code = %d location = %q", res.StatusCode, res.Header.Get("Location"))
		}
		if got := res.Header.Get("Clear-Site-Data"); got != wantSignOutClearSiteData {
			t.Fatalf("logout Clear-Site-Data = %q, want %q", got, wantSignOutClearSiteData)
		}
	}

	logout(session, thisBrowser)
	e.requirePushSubscriptionActive(t, "signed-out browser", user.ID, thisBrowser, false)
	e.requirePushSubscriptionActive(t, "other browser", user.ID, otherBrowser, true)

	// A session names only its own account's subscriptions.
	logout(otherAccountSession, strangersBrowser)
	e.requirePushSubscriptionActive(t, "another account's browser", stranger.ID, strangersBrowser, true)

	// With no session there is no account to act for.
	logout("", otherBrowser)
	e.requirePushSubscriptionActive(t, "signed-out request", user.ID, otherBrowser, true)
}

func TestSigningOutEverywhereDisablesPushOnEveryBrowser(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	user, session := e.mustSessionToken(t, "everywhere-push")
	endpoints := []string{e.mustPushSubscription(t, user.ID), e.mustPushSubscription(t, user.ID)}

	res := e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/token-sessions/revoke", session,
		strings.NewReader(url.Values{}.Encode()), map[string]string{"Origin": e.ts.URL})
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke sessions code = %d", res.StatusCode)
	}
	if got := res.Header.Get("Clear-Site-Data"); got != wantSignOutClearSiteData {
		t.Fatalf("revoke sessions Clear-Site-Data = %q, want %q", got, wantSignOutClearSiteData)
	}
	for _, endpoint := range endpoints {
		e.requirePushSubscriptionActive(t, "after signing out everywhere", user.ID, endpoint, false)
	}
}

func TestPasswordChangeDisablesPushOnEveryBrowser(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	const password = "correct-horse-battery"
	user, current, _ := passwordUserWithSessions(t, e, password)
	endpoints := []string{e.mustPushSubscription(t, user.ID), e.mustPushSubscription(t, user.ID)}

	form := url.Values{"current_password": {password}, "new_password": {"a-brand-new-password"}}
	res := e.uiDoNoRedirect(t, http.MethodPost, "/settings/password", current, strings.NewReader(form.Encode()))
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("password change code = %d", res.StatusCode)
	}
	// The browser that changed the password stays signed in, but its push
	// subscription goes with the rest and is enabled again from that browser.
	for _, endpoint := range endpoints {
		e.requirePushSubscriptionActive(t, "after password change", user.ID, endpoint, false)
	}
}
