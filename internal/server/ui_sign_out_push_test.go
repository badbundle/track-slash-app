package server

import (
	"strings"
	"testing"
)

// The sign-out form carries the subscription only because app.js adds it.
func TestSignOutScriptNamesThisBrowsersPushSubscription(t *testing.T) {
	t.Parallel()
	body, err := uiTemplateFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	for _, want := range []string{
		`event.target.matches("[data-sign-out]")`,
		`signOutWithPushEndpoint(event.target);`,
		`await navigator.serviceWorker.getRegistration("/")`,
		`input.name = "push_endpoint";`,
		`input.value = subscription.endpoint;`,
		`Promise.race([forgetSubscription(), timeout]).catch(() => {}).finally(() => form.submit());`,
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("app.js sign-out missing %q", want)
		}
	}
}
