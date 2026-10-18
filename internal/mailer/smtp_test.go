package mailer

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal submission server for one test. It records what the
// client sent and whether each AUTH arrived over TLS.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	tls      *tls.Config
	implicit bool
	// offerSTARTTLS advertises STARTTLS on a plain connection.
	offerSTARTTLS bool
	greeting      string
	rejectAuth    bool
	rejectMail    bool
	rejectRcpt    bool
	rejectDataCmd bool
	// rejectMessage refuses the message at the end of DATA, as a provider
	// over its quota does.
	rejectMessage bool
	stall         bool

	mu          sync.Mutex
	commands    []string
	authOverTLS []bool
	authPayload string
	data        string
	done        chan struct{}
}

func newTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func startFakeSMTP(t *testing.T, configure func(*fakeSMTP)) (*fakeSMTP, *x509.CertPool) {
	t.Helper()
	cert, pool := newTestCertificate(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	server := &fakeSMTP{
		t:             t,
		ln:            ln,
		tls:           &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		offerSTARTTLS: true,
		greeting:      "220 fake ESMTP ready",
		done:          make(chan struct{}),
	}
	if configure != nil {
		configure(server)
	}
	go server.serve()
	t.Cleanup(func() {
		close(server.done)
		_ = ln.Close()
	})
	return server, pool
}

func (s *fakeSMTP) port() int {
	return s.ln.Addr().(*net.TCPAddr).Port
}

func (s *fakeSMTP) record(command string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, command)
}

func (s *fakeSMTP) verbs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *fakeSMTP) serve() {
	for {
		raw, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.handle(raw)
	}
}

func (s *fakeSMTP) handle(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
	var conn net.Conn = raw
	secure := false
	if s.implicit {
		tlsConn := tls.Server(raw, s.tls)
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		conn, secure = tlsConn, true
	}
	if s.stall {
		<-s.done
		return
	}
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("%s", s.greeting)
	if !strings.HasPrefix(s.greeting, "220") {
		return
	}
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, _, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		s.record(verb)
		switch verb {
		case "EHLO":
			lines := []string{"250-fake"}
			if s.offerSTARTTLS && !secure {
				lines = append(lines, "250-STARTTLS")
			}
			// AUTH is advertised even on a plain connection, so a client that
			// would authenticate without TLS gets the chance to.
			lines = append(lines, "250-AUTH PLAIN", "250 8BITMIME")
			for _, l := range lines {
				_ = tp.PrintfLine("%s", l)
			}
		case "STARTTLS":
			_ = tp.PrintfLine("220 go ahead")
			tlsConn := tls.Server(conn, s.tls)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn, secure = tlsConn, true
			tp = textproto.NewConn(conn)
		case "AUTH":
			s.mu.Lock()
			s.authOverTLS = append(s.authOverTLS, secure)
			fields := strings.Fields(line)
			if len(fields) == 3 {
				decoded, _ := base64.StdEncoding.DecodeString(fields[2])
				s.authPayload = string(decoded)
			}
			s.mu.Unlock()
			if s.rejectAuth {
				_ = tp.PrintfLine("535 5.7.8 authentication failed")
			} else {
				_ = tp.PrintfLine("235 2.7.0 authenticated")
			}
		case "MAIL":
			if s.rejectMail {
				_ = tp.PrintfLine("550 5.7.1 sender not allowed")
			} else {
				_ = tp.PrintfLine("250 ok")
			}
		case "RCPT":
			if s.rejectRcpt {
				_ = tp.PrintfLine("550 5.1.1 no such user")
			} else {
				_ = tp.PrintfLine("250 ok")
			}
		case "DATA":
			if s.rejectDataCmd {
				_ = tp.PrintfLine("554 5.5.1 no valid recipients")
				continue
			}
			_ = tp.PrintfLine("354 end with .")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.data = string(data)
			s.mu.Unlock()
			if s.rejectMessage {
				_ = tp.PrintfLine("554 5.7.0 daily sending quota exceeded")
			} else {
				_ = tp.PrintfLine("250 queued")
			}
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("502 unrecognised")
		}
	}
}

func testMessage() Message {
	return Message{
		To:      "ada@example.com",
		Subject: "Confirm your email for Trackslash",
		Text:    "Open https://trackslash.com/settings/email/confirm?token=abc to confirm.",
		HTML:    `<p><a href="https://trackslash.com/settings/email/confirm?token=abc">Confirm this email</a></p>`,
	}
}

func newTestSMTP(t *testing.T, server *fakeSMTP, pool *x509.CertPool, configure func(*SMTPConfig)) *SMTP {
	t.Helper()
	cfg := SMTPConfig{
		Host:      "127.0.0.1",
		Port:      server.port(),
		Username:  "trackslash",
		Password:  "s3cret",
		From:      "Trackslash <no-reply@trackslash.com>",
		Timeout:   3 * time.Second,
		TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
	if configure != nil {
		configure(&cfg)
	}
	mailer, err := NewSMTP(cfg)
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	return mailer
}

func TestSMTPSendsOverSTARTTLS(t *testing.T) {
	server, pool := startFakeSMTP(t, nil)
	mailer := newTestSMTP(t, server, pool, nil)
	if err := mailer.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := []string{"EHLO", "STARTTLS", "EHLO", "AUTH", "MAIL", "RCPT", "DATA", "QUIT"}
	if got := server.verbs(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.authOverTLS) != 1 || !server.authOverTLS[0] {
		t.Fatalf("AUTH over TLS = %v", server.authOverTLS)
	}
	if server.authPayload != "\x00trackslash\x00s3cret" {
		t.Fatalf("AUTH PLAIN payload = %q", server.authPayload)
	}
	for _, want := range []string{"From: \"Trackslash\" <no-reply@trackslash.com>", "To: ada@example.com", "Subject: Confirm your email for Trackslash", "multipart/alternative", "https://trackslash.com/settings/email/confirm?token=3Dabc"} {
		if !strings.Contains(server.data, want) {
			t.Fatalf("message missing %q:\n%s", want, server.data)
		}
	}
}

func TestSMTPSendsOverImplicitTLS(t *testing.T) {
	server, pool := startFakeSMTP(t, func(s *fakeSMTP) {
		s.implicit = true
		s.offerSTARTTLS = false
	})
	mailer := newTestSMTP(t, server, pool, func(cfg *SMTPConfig) { cfg.ImplicitTLS = true })
	if err := mailer.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := []string{"EHLO", "AUTH", "MAIL", "RCPT", "DATA", "QUIT"}
	if got := server.verbs(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.authOverTLS) != 1 || !server.authOverTLS[0] {
		t.Fatalf("AUTH over TLS = %v", server.authOverTLS)
	}
}

// A server that cannot upgrade the connection never sees the credentials or
// the message.
func TestSMTPRefusesAServerWithoutSTARTTLS(t *testing.T) {
	server, pool := startFakeSMTP(t, func(s *fakeSMTP) { s.offerSTARTTLS = false })
	mailer := newTestSMTP(t, server, pool, nil)
	err := mailer.Send(context.Background(), testMessage())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send = %v, want a STARTTLS error", err)
	}
	for _, verb := range server.verbs() {
		if verb == "AUTH" || verb == "MAIL" || verb == "DATA" {
			t.Fatalf("client sent %s without TLS: %v", verb, server.verbs())
		}
	}
}

func TestSMTPReportsEachRefusal(t *testing.T) {
	for _, tt := range []struct {
		name      string
		configure func(*fakeSMTP)
		want      string
	}{
		{name: "greeting", configure: func(s *fakeSMTP) { s.greeting = "554 5.3.2 not accepting mail" }, want: "smtp: greeting"},
		{name: "auth", configure: func(s *fakeSMTP) { s.rejectAuth = true }, want: "smtp: auth"},
		{name: "sender", configure: func(s *fakeSMTP) { s.rejectMail = true }, want: "smtp: sender refused"},
		{name: "recipient", configure: func(s *fakeSMTP) { s.rejectRcpt = true }, want: "smtp: recipient refused"},
		{name: "data command", configure: func(s *fakeSMTP) { s.rejectDataCmd = true }, want: "smtp: data refused"},
		{name: "over quota", configure: func(s *fakeSMTP) { s.rejectMessage = true }, want: "smtp: message refused"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server, pool := startFakeSMTP(t, tt.configure)
			mailer := newTestSMTP(t, server, pool, nil)
			err := mailer.Send(context.Background(), testMessage())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Send = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSMTPRefusesAnUntrustedCertificate(t *testing.T) {
	server, _ := startFakeSMTP(t, nil)
	mailer := newTestSMTP(t, server, x509.NewCertPool(), nil)
	err := mailer.Send(context.Background(), testMessage())
	if err == nil || !strings.Contains(err.Error(), "smtp: starttls") {
		t.Fatalf("Send = %v, want a starttls error", err)
	}
	for _, verb := range server.verbs() {
		if verb == "AUTH" {
			t.Fatalf("client authenticated to an untrusted server: %v", server.verbs())
		}
	}
}

func TestSMTPImplicitTLSAgainstAPlainServer(t *testing.T) {
	server, pool := startFakeSMTP(t, nil)
	mailer := newTestSMTP(t, server, pool, func(cfg *SMTPConfig) { cfg.ImplicitTLS = true })
	err := mailer.Send(context.Background(), testMessage())
	if err == nil || !strings.Contains(err.Error(), "smtp: tls") {
		t.Fatalf("Send = %v, want a tls error", err)
	}
}

func TestSMTPGivesUpOnAStalledServer(t *testing.T) {
	server, pool := startFakeSMTP(t, func(s *fakeSMTP) { s.stall = true })
	mailer := newTestSMTP(t, server, pool, func(cfg *SMTPConfig) { cfg.Timeout = 200 * time.Millisecond })
	start := time.Now()
	err := mailer.Send(context.Background(), testMessage())
	if err == nil || !strings.Contains(err.Error(), "smtp: greeting") {
		t.Fatalf("Send = %v, want a greeting timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Send took %s, want it bounded by the timeout", elapsed)
	}
}

func TestSMTPStopsWhenTheRequestIsCancelled(t *testing.T) {
	server, pool := startFakeSMTP(t, func(s *fakeSMTP) { s.stall = true })
	mailer := newTestSMTP(t, server, pool, nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	if err := mailer.Send(ctx, testMessage()); err == nil {
		t.Fatal("Send after cancel = nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Send took %s after cancel", elapsed)
	}
}

func TestSMTPConnectFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	mailer, err := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, Username: "u", Password: "p", From: "no-reply@trackslash.com", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	if err := mailer.Send(context.Background(), testMessage()); err == nil || !strings.Contains(err.Error(), "smtp: connect") {
		t.Fatalf("Send = %v, want a connect error", err)
	}
}

func TestSMTPRefusesAnInvalidMessageWithoutConnecting(t *testing.T) {
	server, pool := startFakeSMTP(t, nil)
	mailer := newTestSMTP(t, server, pool, nil)
	msg := testMessage()
	msg.To = "ada@example.com\r\nBcc: everyone@example.com"
	if err := mailer.Send(context.Background(), msg); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Send = %v, want ErrInvalidMessage", err)
	}
	if got := server.verbs(); len(got) != 0 {
		t.Fatalf("invalid message reached the server: %v", got)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no randomness") }

func TestSMTPComposeFailure(t *testing.T) {
	server, pool := startFakeSMTP(t, nil)
	mailer := newTestSMTP(t, server, pool, nil)
	mailer.random = failingReader{}
	if err := mailer.Send(context.Background(), testMessage()); err == nil || !strings.Contains(err.Error(), "smtp: compose") {
		t.Fatalf("Send = %v, want a compose error", err)
	}
}

func TestNewSMTPValidatesItsConfig(t *testing.T) {
	valid := SMTPConfig{Host: "mail.smtp2go.com", Port: 2525, Username: "u", Password: "p", From: "Trackslash <no-reply@trackslash.com>"}
	for _, tt := range []struct {
		name   string
		mutate func(*SMTPConfig)
	}{
		{name: "no host", mutate: func(c *SMTPConfig) { c.Host = " " }},
		{name: "no port", mutate: func(c *SMTPConfig) { c.Port = 0 }},
		{name: "port too large", mutate: func(c *SMTPConfig) { c.Port = 70000 }},
		{name: "no username", mutate: func(c *SMTPConfig) { c.Username = "" }},
		{name: "no password", mutate: func(c *SMTPConfig) { c.Password = "" }},
		{name: "bad sender", mutate: func(c *SMTPConfig) { c.From = "Trackslash" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			if _, err := NewSMTP(cfg); err == nil {
				t.Fatalf("NewSMTP(%+v) = nil error", cfg)
			}
		})
	}
	mailer, err := NewSMTP(valid)
	if err != nil {
		t.Fatalf("NewSMTP: %v", err)
	}
	if mailer.timeout != DefaultSMTPTimeout || mailer.tlsConfig.ServerName != "mail.smtp2go.com" || mailer.tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("defaults = timeout %s, server name %q, min version %x", mailer.timeout, mailer.tlsConfig.ServerName, mailer.tlsConfig.MinVersion)
	}
	if DefaultSMTPTimeout >= 15*time.Second {
		t.Fatalf("DefaultSMTPTimeout = %s, want it under the 15s request deadline", DefaultSMTPTimeout)
	}
	withName := valid
	withName.TLSConfig = &tls.Config{ServerName: "relay.example.com"}
	mailer, err = NewSMTP(withName)
	if err != nil || mailer.tlsConfig.ServerName != "relay.example.com" {
		t.Fatalf("NewSMTP with TLS config = %+v, %v", mailer, err)
	}
}

// readMessage parses composed bytes the way a receiving server would.
func readMessage(t *testing.T, data []byte) (textproto.MIMEHeader, string) {
	t.Helper()
	reader := textproto.NewReader(bufio.NewReader(strings.NewReader(string(data))))
	header, err := reader.ReadMIMEHeader()
	if err != nil {
		t.Fatalf("ReadMIMEHeader: %v\n%s", err, data)
	}
	body, err := io.ReadAll(reader.R)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return header, string(body)
}
