package config

import (
	"strings"
	"testing"
)

var emailEnvKeys = []string{
	"TRACK_SLASH_SMTP_HOST",
	"TRACK_SLASH_SMTP_PORT",
	"TRACK_SLASH_SMTP_USERNAME",
	"TRACK_SLASH_SMTP_PASSWORD",
	"TRACK_SLASH_SMTP_TLS",
	"TRACK_SLASH_EMAIL_FROM",
}

func setSMTP2GOEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TRACK_SLASH_SMTP_HOST", "mail.smtp2go.com")
	t.Setenv("TRACK_SLASH_SMTP_PORT", "2525")
	t.Setenv("TRACK_SLASH_SMTP_USERNAME", "trackslash")
	t.Setenv("TRACK_SLASH_SMTP_PASSWORD", " secret ")
	t.Setenv("TRACK_SLASH_EMAIL_FROM", "Trackslash <no-reply@trackslash.com>")
}

func TestLoadEmailConfigIsOffWhenUnset(t *testing.T) {
	for _, key := range emailEnvKeys {
		unsetenv(t, key)
	}
	got, err := loadEmailConfig("")
	if err != nil || got.Enabled {
		t.Fatalf("unset email = %+v, %v", got, err)
	}
	// A blank password on its own is still unset.
	t.Setenv("TRACK_SLASH_SMTP_PASSWORD", "   ")
	if got, err := loadEmailConfig(""); err != nil || got.Enabled {
		t.Fatalf("blank password email = %+v, %v", got, err)
	}
}

func TestLoadEmailConfigSMTP(t *testing.T) {
	for _, key := range emailEnvKeys {
		unsetenv(t, key)
	}
	setSMTP2GOEnv(t)
	got, err := loadEmailConfig("https://trackslash.com")
	if err != nil {
		t.Fatalf("loadEmailConfig: %v", err)
	}
	want := EmailConfig{
		Enabled:  true,
		Host:     "mail.smtp2go.com",
		Port:     2525,
		Username: "trackslash",
		Password: " secret ",
		TLS:      EmailTLSStartTLS,
		From:     "Trackslash <no-reply@trackslash.com>",
	}
	if got != want {
		t.Fatalf("loadEmailConfig = %+v, want %+v", got, want)
	}

	t.Setenv("TRACK_SLASH_SMTP_TLS", " Implicit ")
	t.Setenv("TRACK_SLASH_SMTP_PORT", "465")
	got, err = loadEmailConfig("https://trackslash.com")
	if err != nil || got.TLS != EmailTLSImplicit || got.Port != 465 {
		t.Fatalf("implicit TLS email = %+v, %v", got, err)
	}
	t.Setenv("TRACK_SLASH_SMTP_TLS", "starttls")
	if got, err := loadEmailConfig("https://trackslash.com"); err != nil || got.TLS != EmailTLSStartTLS {
		t.Fatalf("explicit starttls email = %+v, %v", got, err)
	}
}

func TestLoadEmailConfigRejectsPartialOrInvalidSettings(t *testing.T) {
	for _, tt := range []struct {
		name    string
		set     map[string]string
		origin  string
		wantErr string
	}{
		{name: "host alone", set: map[string]string{"TRACK_SLASH_SMTP_PASSWORD": "", "TRACK_SLASH_SMTP_PORT": "", "TRACK_SLASH_SMTP_USERNAME": "", "TRACK_SLASH_EMAIL_FROM": ""}, origin: "https://trackslash.com", wantErr: "must be set together"},
		{name: "no host", set: map[string]string{"TRACK_SLASH_SMTP_HOST": ""}, origin: "https://trackslash.com", wantErr: "must be set together"},
		{name: "no port", set: map[string]string{"TRACK_SLASH_SMTP_PORT": ""}, origin: "https://trackslash.com", wantErr: "must be set together"},
		{name: "no username", set: map[string]string{"TRACK_SLASH_SMTP_USERNAME": ""}, origin: "https://trackslash.com", wantErr: "must be set together"},
		{name: "blank password", set: map[string]string{"TRACK_SLASH_SMTP_PASSWORD": "  "}, origin: "https://trackslash.com", wantErr: "must be set together"},
		{name: "no from", set: map[string]string{"TRACK_SLASH_EMAIL_FROM": ""}, origin: "https://trackslash.com", wantErr: "must be set together"},
		{name: "host is a URL", set: map[string]string{"TRACK_SLASH_SMTP_HOST": "smtp://mail.smtp2go.com"}, origin: "https://trackslash.com", wantErr: "TRACK_SLASH_SMTP_HOST must be a host name"},
		{name: "port not a number", set: map[string]string{"TRACK_SLASH_SMTP_PORT": "smtp"}, origin: "https://trackslash.com", wantErr: "TRACK_SLASH_SMTP_PORT must be a port number"},
		{name: "port zero", set: map[string]string{"TRACK_SLASH_SMTP_PORT": "0"}, origin: "https://trackslash.com", wantErr: "TRACK_SLASH_SMTP_PORT must be a port number"},
		{name: "port too large", set: map[string]string{"TRACK_SLASH_SMTP_PORT": "65536"}, origin: "https://trackslash.com", wantErr: "TRACK_SLASH_SMTP_PORT must be a port number"},
		{name: "plaintext TLS mode", set: map[string]string{"TRACK_SLASH_SMTP_TLS": "none"}, origin: "https://trackslash.com", wantErr: "TRACK_SLASH_SMTP_TLS must be starttls or implicit"},
		{name: "bad from", set: map[string]string{"TRACK_SLASH_EMAIL_FROM": "Trackslash"}, origin: "https://trackslash.com", wantErr: "TRACK_SLASH_EMAIL_FROM must be an address"},
		{name: "no public origin", set: map[string]string{}, origin: "", wantErr: "TRACK_SLASH_PUBLIC_ORIGIN is required"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range emailEnvKeys {
				unsetenv(t, key)
			}
			setSMTP2GOEnv(t)
			for key, value := range tt.set {
				t.Setenv(key, value)
			}
			got, err := loadEmailConfig(tt.origin)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("loadEmailConfig = %+v, %v; want error containing %q", got, err, tt.wantErr)
			}
		})
	}
}

func TestLoadEmailConfigLogMailer(t *testing.T) {
	for _, key := range emailEnvKeys {
		unsetenv(t, key)
	}
	t.Setenv("TRACK_SLASH_SMTP_HOST", "LOG")
	got, err := loadEmailConfig("http://localhost:8080")
	if err != nil || !got.Enabled || !got.Log || got.Host != EmailLogHost || got.From != DefaultLogEmailFrom {
		t.Fatalf("log email = %+v, %v", got, err)
	}
	t.Setenv("TRACK_SLASH_EMAIL_FROM", "Dev <dev@example.com>")
	if got, err := loadEmailConfig("http://localhost:8080"); err != nil || got.From != "Dev <dev@example.com>" {
		t.Fatalf("log email with from = %+v, %v", got, err)
	}
	t.Setenv("TRACK_SLASH_EMAIL_FROM", "not an address")
	if _, err := loadEmailConfig("http://localhost:8080"); err == nil || !strings.Contains(err.Error(), "TRACK_SLASH_EMAIL_FROM") {
		t.Fatalf("log email with a bad from = %v", err)
	}
	t.Setenv("TRACK_SLASH_EMAIL_FROM", "")
	if _, err := loadEmailConfig(""); err == nil || !strings.Contains(err.Error(), "TRACK_SLASH_PUBLIC_ORIGIN is required") {
		t.Fatalf("log email without a public origin = %v", err)
	}
}

func TestLoadIncludesEmailConfig(t *testing.T) {
	for _, key := range emailEnvKeys {
		unsetenv(t, key)
	}
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("TRACK_SLASH_PUBLIC_ORIGIN", "")
	t.Setenv("TRACK_SLASH_SMTP_HOST", "log")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRACK_SLASH_PUBLIC_ORIGIN is required") {
		t.Fatalf("Load with email but no public origin = %v", err)
	}
	t.Setenv("TRACK_SLASH_PUBLIC_ORIGIN", "https://trackslash.com")
	cfg, err := Load()
	if err != nil || !cfg.Email.Enabled || !cfg.Email.Log {
		t.Fatalf("Load email = %+v, %v", cfg.Email, err)
	}
}
