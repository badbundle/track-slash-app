package server_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
	"github.com/bradleymackey/track-slash/internal/server"
)

func TestUIPushNotificationSettingsAndBrowserSubscription(t *testing.T) {
	t.Parallel()
	publicKey := base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	e := newHTTPEnvWithOptions(t, server.Options{WebPushPublicKey: publicKey})
	user, token := e.mustUserToken(t, "ui-push-settings")
	body := e.uiGet(t, "/settings/notifications", token)
	for _, want := range []string{
		"Browser notifications", "Enable on this browser", "Notification categories", "Mentions",
		"New assignments", "Relevant comments", "Status changes", "Due-date changes",
		`data-push-enabled="true"`, `data-push-public-key="` + publicKey + `"`, "0 browsers",
		`name="mentions"`, `name="assignments"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("push settings missing %q: %s", want, body)
		}
	}
	if !strings.Contains(body, `name="mentions" class="mt-1 h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" checked`) ||
		!strings.Contains(body, `name="assignments" class="mt-1 h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" checked`) ||
		strings.Contains(body, `name="comments" class="mt-1 h-4 w-4 rounded border-slate-300 text-indigo-600 focus:ring-indigo-500" checked`) {
		t.Fatalf("push preference defaults incorrect: %s", body)
	}

	form := url.Values{"comments": {"on"}, "status_changes": {"on"}, "due_date_changes": {"on"}}
	res := e.uiDoNoRedirect(t, http.MethodPost, "/settings/push/preferences", token, strings.NewReader(form.Encode()))
	defer res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/settings/notifications" {
		t.Fatalf("preference update code=%d location=%q body=%s", res.StatusCode, res.Header.Get("Location"), readBody(t, res))
	}
	preferences, err := e.store.GetPushNotificationPreferences(e.ctx, user.ID)
	wantPreferences := model.PushNotificationPreferences{Comments: true, StatusChanges: true, DueDateChanges: true}
	if err != nil || preferences != wantPreferences {
		t.Fatalf("updated preferences = %+v, %v", preferences, err)
	}

	endpoint := "https://push.example.test/browser"
	request := map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(make([]byte, 65)),
			"auth":   base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		},
	}
	requestBody, _ := json.Marshal(request)
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/settings/push/subscription", token, strings.NewReader(string(requestBody)), map[string]string{
		"Content-Type": "application/json", "User-Agent": "Push Browser",
	})
	defer res.Body.Close()
	if body := readBody(t, res); res.StatusCode != http.StatusCreated || !strings.Contains(body, `"subscribed":true`) {
		t.Fatalf("subscription create code=%d body=%s", res.StatusCode, body)
	}
	stateBody := e.uiGet(t, "/settings/push/subscription?endpoint="+url.QueryEscape(endpoint), token)
	if !strings.Contains(stateBody, `"subscribed":true`) {
		t.Fatalf("subscription state = %s", stateBody)
	}
	if body := e.uiGet(t, "/settings/notifications", token); !strings.Contains(body, "1 browser") {
		t.Fatalf("notifications page missing browser count: %s", body)
	}

	deleteBody, _ := json.Marshal(map[string]string{"endpoint": endpoint})
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodDelete, "/settings/push/subscription", token, strings.NewReader(string(deleteBody)), map[string]string{"Content-Type": "application/json"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("subscription delete code=%d body=%s", res.StatusCode, readBody(t, res))
	}
	stateBody = e.uiGet(t, "/settings/push/subscription?endpoint="+url.QueryEscape(endpoint), token)
	if !strings.Contains(stateBody, `"subscribed":false`) {
		t.Fatalf("disabled subscription state = %s", stateBody)
	}
}

func TestUIPushNotificationValidationConfigurationAndServiceWorker(t *testing.T) {
	t.Parallel()
	e := newHTTPEnv(t)
	_, token := e.mustUserToken(t, "ui-push-validation")
	body := e.uiGet(t, "/settings/notifications", token)
	if !strings.Contains(body, "Browser push is not configured on this deployment.") || !strings.Contains(body, `data-push-enabled="false"`) {
		t.Fatalf("disabled push settings = %s", body)
	}

	res := e.uiDoNoRedirect(t, http.MethodPost, "/settings/push/preferences", token, strings.NewReader("mentions=%zz"))
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed preferences code=%d body=%s", res.StatusCode, readBody(t, res))
	}

	res = e.uiDoNoRedirectWithHeaders(t, http.MethodPost, "/settings/push/subscription", token, strings.NewReader(`{}`), map[string]string{"Content-Type": "application/json"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("disabled subscription code=%d body=%s", res.StatusCode, readBody(t, res))
	}
	res = e.uiDoNoRedirect(t, http.MethodGet, "/settings/push/subscription", token, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing endpoint state code=%d body=%s", res.StatusCode, readBody(t, res))
	}
	res = e.uiDoNoRedirectWithHeaders(t, http.MethodDelete, "/settings/push/subscription", token, strings.NewReader(`{}`), map[string]string{"Content-Type": "application/json"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing endpoint delete code=%d body=%s", res.StatusCode, readBody(t, res))
	}

	res = e.uiDoNoRedirect(t, http.MethodGet, "/service-worker.js", "", nil)
	defer res.Body.Close()
	workerBody := readBody(t, res)
	if res.StatusCode != http.StatusOK || res.Header.Get("Service-Worker-Allowed") != "/" ||
		!strings.Contains(res.Header.Get("Content-Type"), "text/javascript") ||
		!strings.Contains(workerBody, `addEventListener("push"`) || !strings.Contains(workerBody, `addEventListener("notificationclick"`) {
		t.Fatalf("service worker code=%d headers=%v body=%s", res.StatusCode, res.Header, workerBody)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "worker-src 'self'") {
		t.Fatalf("service worker CSP = %q", res.Header.Get("Content-Security-Policy"))
	}
}
