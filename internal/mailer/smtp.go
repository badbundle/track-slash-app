package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// DefaultSMTPTimeout bounds one delivery, from connecting to the provider
// accepting the message. It stays well under the 15-second deadline of the
// form request that waits for it, so a stalled provider surfaces as a send
// error the page can show rather than as a dropped request.
const DefaultSMTPTimeout = 10 * time.Second

// SMTPConfig describes a submission server such as mail.smtp2go.com.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	// ImplicitTLS opens the connection over TLS, as on port 465. Otherwise the
	// connection must be upgraded with STARTTLS before anything else is sent.
	ImplicitTLS bool
	// From is the sender, such as "Trackslash <no-reply@trackslash.com>".
	From string
	// Timeout bounds one delivery. Zero means DefaultSMTPTimeout.
	Timeout time.Duration
	// TLSConfig replaces the default TLS settings. Tests use it to trust their
	// own certificate; production leaves it nil.
	TLSConfig *tls.Config
}

// SMTP delivers mail through an authenticated submission server. It always
// uses TLS: it never sends a message, and never offers its credentials, over
// a connection that is not encrypted.
type SMTP struct {
	cfg       SMTPConfig
	from      *mail.Address
	tlsConfig *tls.Config
	timeout   time.Duration
	now       func() time.Time
	random    io.Reader
}

var _ Mailer = (*SMTP)(nil)

func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if strings.TrimSpace(cfg.Host) == "" || cfg.Port < 1 || cfg.Port > 65535 {
		return nil, errors.New("smtp mailer requires a host and port")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("smtp mailer requires a username and password")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("smtp mailer sender: %w", err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSConfig != nil {
		tlsConfig = cfg.TLSConfig.Clone()
	}
	if tlsConfig.ServerName == "" {
		tlsConfig.ServerName = cfg.Host
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultSMTPTimeout
	}
	return &SMTP{cfg: cfg, from: from, tlsConfig: tlsConfig, timeout: timeout, now: time.Now, random: rand.Reader}, nil
}

func (m *SMTP) Send(ctx context.Context, msg Message) error {
	if err := msg.validate(); err != nil {
		return err
	}
	data, err := compose(m.from, msg, m.now(), m.random)
	if err != nil {
		return fmt.Errorf("smtp: compose: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port)))
	if err != nil {
		return fmt.Errorf("smtp: connect: %w", err)
	}
	defer conn.Close()
	// net/smtp has no context, so the deadline bounds every read and write,
	// and cancelling the request ends a delivery that is still waiting.
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	if m.cfg.ImplicitTLS {
		tlsConn := tls.Client(conn, m.tlsConfig.Clone())
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("smtp: tls: %w", err)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	defer client.Close()
	if !m.cfg.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not offer STARTTLS, so nothing was sent")
		}
		if err := client.StartTLS(m.tlsConfig.Clone()); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
	}
	// PlainAuth also refuses, on its own, to send credentials over a
	// connection without TLS, so this is never plaintext.
	if err := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
		return fmt.Errorf("smtp: auth: %w", err)
	}
	if err := client.Mail(m.from.Address); err != nil {
		return fmt.Errorf("smtp: sender refused: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp: recipient refused: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: data refused: %w", err)
	}
	_, writeErr := w.Write(data)
	// A provider over its quota refuses the message here, at the end of DATA.
	if err := errors.Join(writeErr, w.Close()); err != nil {
		return fmt.Errorf("smtp: message refused: %w", err)
	}
	// The provider accepted the message when DATA ended; a failed QUIT
	// changes nothing.
	_ = client.Quit()
	return nil
}
