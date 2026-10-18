package main

import (
	"testing"

	"github.com/bradleymackey/track-slash/internal/config"
	"github.com/bradleymackey/track-slash/internal/mailer"
)

func TestNewMailer(t *testing.T) {
	if m, err := newMailer(config.EmailConfig{}); err != nil || m != nil {
		t.Fatalf("email off = %v, %v; want a nil mailer", m, err)
	}
	if m, err := newMailer(config.EmailConfig{Enabled: true, Log: true, From: config.DefaultLogEmailFrom}); err != nil {
		t.Fatalf("log mailer: %v", err)
	} else if _, ok := m.(*mailer.Log); !ok {
		t.Fatalf("log mailer = %T", m)
	}
	smtpConfig := config.EmailConfig{Enabled: true, Host: "mail.smtp2go.com", Port: 2525, Username: "u", Password: "p", TLS: config.EmailTLSStartTLS, From: "Trackslash <no-reply@trackslash.com>"}
	if m, err := newMailer(smtpConfig); err != nil {
		t.Fatalf("smtp mailer: %v", err)
	} else if _, ok := m.(*mailer.SMTP); !ok {
		t.Fatalf("smtp mailer = %T", m)
	}
	smtpConfig.From = "not an address"
	if m, err := newMailer(smtpConfig); err == nil || m != nil {
		t.Fatalf("bad sender = %v, %v", m, err)
	}
}
