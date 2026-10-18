package mailer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestValidAddress(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"ada@example.com", true},
		{"ada.lovelace+trackslash@mail.example.co.uk", true},
		{"", false},
		{"ada", false},
		{"@example.com", false},
		{"ada@", false},
		{"Ada <ada@example.com>", false},
		{"ada@example.com (Ada)", false},
		{"ada@example.com\r\nBcc: x@example.com", false},
		{"ada@example.com, bob@example.com", false},
		{" ada@example.com", false},
		{`"ada"@example.com`, false},
		{strings.Repeat("a", 250) + "@example.com", false},
	} {
		if got := ValidAddress(tt.in); got != tt.want {
			t.Errorf("ValidAddress(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestMessageValidate(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*Message)
	}{
		{name: "recipient", mutate: func(m *Message) { m.To = "not an address" }},
		{name: "empty subject", mutate: func(m *Message) { m.Subject = " " }},
		{name: "multi-line subject", mutate: func(m *Message) { m.Subject = "Hi\r\nBcc: x@example.com" }},
		{name: "no text", mutate: func(m *Message) { m.Text = "" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			msg := testMessage()
			tt.mutate(&msg)
			if err := msg.validate(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("validate = %v, want ErrInvalidMessage", err)
			}
		})
	}
	if err := testMessage().validate(); err != nil {
		t.Fatalf("validate valid message = %v", err)
	}
}

func TestComposeWritesTextAndHTMLAlternatives(t *testing.T) {
	from := &mail.Address{Name: "Trackslash", Address: "no-reply@trackslash.com"}
	msg := testMessage()
	msg.Subject = "Confirm your email for Trackslash ✓"
	msg.Text = "Hi Zoë,\n\n" + strings.Repeat("long line ", 20) + "\nhttps://trackslash.com/settings/email/confirm?token=abc"
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	data, err := compose(from, msg, now, bytes.NewReader(bytes.Repeat([]byte{0xab}, 16)))
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	header, body := readMessage(t, data)
	for key, want := range map[string]string{
		"From":           `"Trackslash" <no-reply@trackslash.com>`,
		"To":             "ada@example.com",
		"Date":           "Mon, 28 Sep 2026 12:00:00 +0000",
		"Message-Id":     "<abababababababababababababababab@trackslash.com>",
		"Mime-Version":   "1.0",
		"Auto-Submitted": "auto-generated",
	} {
		if got := header.Get(key); got != want {
			t.Errorf("header %s = %q, want %q", key, got, want)
		}
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(header.Get("Subject"))
	if err != nil || subject != msg.Subject {
		t.Fatalf("subject = %q, %v", subject, err)
	}
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("content type = %q, %v", header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(strings.NewReader(body), params["boundary"])
	var types, contents []string
	for {
		part, err := reader.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("NextRawPart: %v", err)
		}
		if part.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
			t.Fatalf("part encoding = %q", part.Header.Get("Content-Transfer-Encoding"))
		}
		raw, _ := io.ReadAll(part)
		for _, line := range strings.Split(string(raw), "\r\n") {
			if len(line) > 76 {
				t.Fatalf("quoted-printable line longer than 76: %q", line)
			}
		}
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(raw)))
		if err != nil {
			t.Fatalf("decode part: %v", err)
		}
		types = append(types, part.Header.Get("Content-Type"))
		contents = append(contents, string(decoded))
	}
	if strings.Join(types, ",") != "text/plain; charset=utf-8,text/html; charset=utf-8" {
		t.Fatalf("part types = %v", types)
	}
	if contents[0] != strings.ReplaceAll(msg.Text, "\n", "\r\n") || contents[1] != msg.HTML {
		t.Fatalf("decoded parts = %q", contents)
	}
}

func TestComposeTextOnly(t *testing.T) {
	from := &mail.Address{Address: "no-reply@trackslash.com"}
	msg := testMessage()
	msg.HTML = ""
	data, err := compose(from, msg, time.Now(), bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	header, body := readMessage(t, data)
	if header.Get("Content-Type") != "text/plain; charset=utf-8" || header.Get("Content-Transfer-Encoding") != "quoted-printable" || header.Get("From") != "<no-reply@trackslash.com>" {
		t.Fatalf("headers = %v", header)
	}
	decoded, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if string(decoded) != msg.Text {
		t.Fatalf("body = %q", decoded)
	}
}

func TestComposeNeedsRandomness(t *testing.T) {
	if _, err := compose(&mail.Address{Address: "a@example.com"}, testMessage(), time.Now(), failingReader{}); err == nil {
		t.Fatal("compose without randomness = nil error")
	}
}

func TestLogMailerPrintsTheMessage(t *testing.T) {
	var out bytes.Buffer
	mailer := NewLog(log.New(&out, "", 0), "Trackslash <no-reply@localhost>")
	if err := mailer.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for _, want := range []string{"To: ada@example.com", "Subject: Confirm your email for Trackslash", "https://trackslash.com/settings/email/confirm?token=abc", "From: Trackslash <no-reply@localhost>"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("log missing %q: %s", want, out.String())
		}
	}
	bad := testMessage()
	bad.To = "nobody"
	if err := mailer.Send(context.Background(), bad); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Send invalid = %v", err)
	}
	if NewLog(nil, "x@example.com").logger != log.Default() {
		t.Fatal("NewLog(nil) should use the standard logger")
	}
}

func TestCapturingMailer(t *testing.T) {
	mailer := NewCapturing()
	if err := mailer.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	refused := errors.New("554 over quota")
	mailer.Fail(refused)
	if err := mailer.Send(context.Background(), testMessage()); !errors.Is(err, refused) {
		t.Fatalf("Send while failing = %v", err)
	}
	bad := testMessage()
	bad.To = "nobody"
	if err := mailer.Send(context.Background(), bad); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Send invalid = %v", err)
	}
	mailer.Fail(nil)
	if err := mailer.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("Send after recovering: %v", err)
	}
	messages := mailer.Messages()
	if len(messages) != 2 || mailer.Attempts() != 3 {
		t.Fatalf("messages = %d, attempts = %d", len(messages), mailer.Attempts())
	}
	messages[0].To = "changed@example.com"
	if mailer.Messages()[0].To != "ada@example.com" {
		t.Fatal("Messages returned the mailer's own slice")
	}
}
