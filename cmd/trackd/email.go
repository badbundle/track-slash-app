package main

import (
	"github.com/bradleymackey/track-slash/internal/config"
	"github.com/bradleymackey/track-slash/internal/mailer"
)

// newMailer builds the mailer the configuration asks for. It returns nil, and
// email stays off, when email is not configured.
func newMailer(cfg config.EmailConfig) (mailer.Mailer, error) {
	switch {
	case !cfg.Enabled:
		return nil, nil
	case cfg.Log:
		return mailer.NewLog(nil, cfg.From), nil
	}
	smtp, err := mailer.NewSMTP(mailer.SMTPConfig{
		Host:        cfg.Host,
		Port:        cfg.Port,
		Username:    cfg.Username,
		Password:    cfg.Password,
		ImplicitTLS: cfg.TLS == config.EmailTLSImplicit,
		From:        cfg.From,
	})
	if err != nil {
		return nil, err
	}
	return smtp, nil
}
