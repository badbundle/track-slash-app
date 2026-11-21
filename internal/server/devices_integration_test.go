package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/server"
)

func TestRegisterAndUnregisterMyDevice(t *testing.T) {
	t.Parallel()
	token := strings.Repeat("0a", 32)

	off := newHTTPEnvWithOptions(t, server.Options{})
	if code, body := off.do(t, http.MethodPut, "/me/devices", map[string]string{"token": token}); code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured register = %d %s", code, body)
	}

	e := newHTTPEnvWithOptions(t, server.Options{APNsEnabled: true})
	if code, body := e.doUnauth(t, http.MethodPut, "/me/devices", map[string]string{"token": token}); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated register = %d %s", code, body)
	}
	if code, body := e.do(t, http.MethodPut, "/me/devices", map[string]string{"token": "xyz"}); code != http.StatusBadRequest {
		t.Fatalf("bad token = %d %s", code, body)
	}
	if code, body := e.do(t, http.MethodPut, "/me/devices", "not an object"); code != http.StatusBadRequest {
		t.Fatalf("bad body = %d %s", code, body)
	}
	if code, body := e.do(t, http.MethodPut, "/me/devices", map[string]string{"token": token, "name": "iPhone"}); code != http.StatusOK {
		t.Fatalf("register = %d %s", code, body)
	}
	// Registering twice is the app's normal launch behaviour.
	if code, body := e.do(t, http.MethodPut, "/me/devices", map[string]string{"token": token}); code != http.StatusOK {
		t.Fatalf("re-register = %d %s", code, body)
	}
	if n := e.countRows(t, `SELECT count(*) FROM push_subscriptions WHERE kind = 'apns' AND endpoint = $1 AND disabled_at IS NULL`, token); n != 1 {
		t.Fatalf("active apns rows = %d", n)
	}

	if code, body := e.do(t, http.MethodDelete, "/me/devices", map[string]string{}); code != http.StatusBadRequest {
		t.Fatalf("delete without token = %d %s", code, body)
	}
	if code, body := e.do(t, http.MethodDelete, "/me/devices", "not an object"); code != http.StatusBadRequest {
		t.Fatalf("delete bad body = %d %s", code, body)
	}
	if code, body := e.do(t, http.MethodDelete, "/me/devices", map[string]string{"token": token}); code != http.StatusOK {
		t.Fatalf("unregister = %d %s", code, body)
	}
	if n := e.countRows(t, `SELECT count(*) FROM push_subscriptions WHERE endpoint = $1 AND disabled_at IS NULL`, token); n != 0 {
		t.Fatalf("active rows after unregister = %d", n)
	}
	if code, body := e.do(t, http.MethodDelete, "/me/devices", map[string]string{"token": strings.Repeat("ff", 32)}); code != http.StatusOK {
		t.Fatalf("unregister unknown = %d %s", code, body)
	}
}
