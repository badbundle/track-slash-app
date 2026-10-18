package config

import (
	"errors"
	"net/mail"
	"os"
	"strconv"
	"strings"
)

const (
	// EmailTLSStartTLS upgrades a plain connection with STARTTLS before
	// anything else is sent. It is the default, and what SMTP2GO recommends
	// on port 2525.
	EmailTLSStartTLS = "starttls"
	// EmailTLSImplicit opens the connection over TLS, as on port 465.
	EmailTLSImplicit = "implicit"
	// EmailLogHost selects the log mailer, which prints each message instead
	// of sending it, for local development.
	EmailLogHost = "log"
	// DefaultLogEmailFrom is the sender the log mailer shows when
	// TRACK_SLASH_EMAIL_FROM is unset.
	DefaultLogEmailFrom = "Trackslash <no-reply@localhost>"
)

// EmailConfig says how trackslash sends email. Email is optional: with every
// variable unset it is off, the app runs without it and shows no email UI.
type EmailConfig struct {
	Enabled bool
	// Log prints each message to the server log instead of sending it.
	Log      bool
	Host     string
	Port     int
	Username string
	Password string
	// TLS is EmailTLSStartTLS or EmailTLSImplicit. Mail is never sent, and
	// credentials never offered, over a connection without TLS.
	TLS  string
	From string
}

// loadEmailConfig reads the TRACK_SLASH_SMTP_* variables and
// TRACK_SLASH_EMAIL_FROM. Like the VAPID keys they are all set or all unset,
// and since every message links back to the app they need the public origin:
// a link is never built from a request's Host header.
func loadEmailConfig(publicOrigin string) (EmailConfig, error) {
	host := strings.TrimSpace(os.Getenv("TRACK_SLASH_SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("TRACK_SLASH_SMTP_PORT"))
	username := strings.TrimSpace(os.Getenv("TRACK_SLASH_SMTP_USERNAME"))
	// A password is used exactly as given; only an all-blank one counts as unset.
	password := os.Getenv("TRACK_SLASH_SMTP_PASSWORD")
	tlsMode := strings.ToLower(strings.TrimSpace(os.Getenv("TRACK_SLASH_SMTP_TLS")))
	from := strings.TrimSpace(os.Getenv("TRACK_SLASH_EMAIL_FROM"))
	if host == "" && port == "" && username == "" && strings.TrimSpace(password) == "" && tlsMode == "" && from == "" {
		return EmailConfig{}, nil
	}
	if strings.EqualFold(host, EmailLogHost) {
		if publicOrigin == "" {
			return EmailConfig{}, errors.New("TRACK_SLASH_PUBLIC_ORIGIN is required when email is configured, because email links use it")
		}
		if from == "" {
			from = DefaultLogEmailFrom
		}
		if _, err := mail.ParseAddress(from); err != nil {
			return EmailConfig{}, errors.New("TRACK_SLASH_EMAIL_FROM must be an address such as Trackslash <no-reply@example.com>")
		}
		return EmailConfig{Enabled: true, Log: true, Host: EmailLogHost, From: from}, nil
	}
	if host == "" || port == "" || username == "" || strings.TrimSpace(password) == "" || from == "" {
		return EmailConfig{}, errors.New("TRACK_SLASH_SMTP_HOST, TRACK_SLASH_SMTP_PORT, TRACK_SLASH_SMTP_USERNAME, TRACK_SLASH_SMTP_PASSWORD, and TRACK_SLASH_EMAIL_FROM must be set together")
	}
	if strings.ContainsAny(host, " \t/") {
		return EmailConfig{}, errors.New("TRACK_SLASH_SMTP_HOST must be a host name such as mail.smtp2go.com")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return EmailConfig{}, errors.New("TRACK_SLASH_SMTP_PORT must be a port number such as 2525")
	}
	switch tlsMode {
	case "":
		tlsMode = EmailTLSStartTLS
	case EmailTLSStartTLS, EmailTLSImplicit:
	default:
		return EmailConfig{}, errors.New("TRACK_SLASH_SMTP_TLS must be starttls or implicit")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return EmailConfig{}, errors.New("TRACK_SLASH_EMAIL_FROM must be an address such as Trackslash <no-reply@example.com>")
	}
	if publicOrigin == "" {
		return EmailConfig{}, errors.New("TRACK_SLASH_PUBLIC_ORIGIN is required when email is configured, because email links use it")
	}
	return EmailConfig{
		Enabled:  true,
		Host:     host,
		Port:     portNumber,
		Username: username,
		Password: password,
		TLS:      tlsMode,
		From:     from,
	}, nil
}
