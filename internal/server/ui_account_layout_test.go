package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bradleymackey/track-slash/internal/model"
)

// Account pages stack their cards in one column, so every card spans the page
// and their edges line up. The Login page used to put Password in the left half
// of a two-column grid, with Passkeys spanning both columns underneath.
func TestUIAccountPagesStackFullWidthCards(t *testing.T) {
	t.Parallel()

	const card = `class="rounded-lg border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900"`
	for _, tt := range []struct {
		name     string
		template string
		data     any
		cards    []string
		fields   []string
	}{
		{
			name:     "profile",
			template: "profile-panel",
			data:     uiProfilePanelData{User: model.User{Username: "ada", Name: "Ada Lovelace", Email: "ada@example.com"}},
			cards:    []string{`<section data-profile-panel `},
			fields:   []string{`for="name">Display name`, `for="email">Email`},
		},
		{
			name:     "login",
			template: "login-panel",
			data:     uiLoginPanelData{PasswordLogin: model.PasswordLoginState{HasPassword: true, Enabled: true, CanDisable: true, ActivePasskeys: 1}},
			cards:    []string{`<section data-password-login-panel `, `<section data-passkeys-panel `},
			fields:   []string{`for="current_password">Current password`, `for="new_password">New password`},
		},
		{
			name:     "notifications",
			template: "notifications-panel",
			data:     uiNotificationPanelData{},
			cards:    []string{`<section id="notifications" `},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := uiTemplates.ExecuteTemplate(&buf, tt.template, tt.data); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			body := buf.String()
			if !strings.Contains(body, `<section class="grid gap-6 py-6">`) {
				t.Fatalf("%s cards are not stacked in one column: %s", tt.name, body)
			}
			for _, notWant := range []string{"lg:grid-cols-2", "lg:col-span-2"} {
				if strings.Contains(body, notWant) {
					t.Fatalf("%s still lays its cards out in two columns (%q): %s", tt.name, notWant, body)
				}
			}
			for _, start := range tt.cards {
				if tag := markupFromForTest(t, body, start, ">"); !strings.HasSuffix(tag, card) {
					t.Fatalf("%s card %q should span the column with the shared card class: %s", tt.name, start, tag)
				}
			}
			if tt.fields == nil {
				return
			}
			// Short fields share a row on wider screens rather than stretching
			// across the whole card, and stack on narrow ones.
			fields := markupFromForTest(t, body, `<div data-account-form-fields class="grid gap-4 sm:grid-cols-2">`, `</form>`)
			requireMarkupOrder(t, fields, tt.fields[0], tt.fields[1])
			requireMarkupOrder(t, fields, tt.fields[1], `type="submit"`)
		})
	}
}

// The password form only renders while password login is on, so the other two
// states keep a single line of explanation in the full-width Password card.
func TestUILoginPagePasswordStatesKeepTheFullWidthCard(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		state model.PasswordLoginState
		want  string
	}{
		{name: "password login off", state: model.PasswordLoginState{HasPassword: true}, want: "Password login is off."},
		{name: "no password", state: model.PasswordLoginState{}, want: "No password is set."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := uiTemplates.ExecuteTemplate(&buf, "login-panel", uiLoginPanelData{PasswordLogin: tt.state}); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			body := buf.String()
			panel := markupFromForTest(t, body, `<section data-password-login-panel `, `<section data-passkeys-panel `)
			if !strings.Contains(panel, tt.want) {
				t.Fatalf("password card missing %q: %s", tt.want, panel)
			}
			if strings.Contains(panel, `data-account-form-fields`) || strings.Contains(panel, `action="/settings/password"`) {
				t.Fatalf("password form should not render when %s: %s", tt.name, panel)
			}
			if strings.Contains(body, "lg:grid-cols-2") || strings.Contains(body, "lg:col-span-2") {
				t.Fatalf("login page still lays its cards out in two columns: %s", body)
			}
		})
	}
}
