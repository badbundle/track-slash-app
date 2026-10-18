package mailer

import (
	"bytes"
	"encoding/hex"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"
)

// compose writes msg as an RFC 5322 message: a plain-text part, and the HTML
// alternative when there is one, both quoted-printable UTF-8. Every header
// value is either fixed, from configuration, or checked by Message.validate,
// so nothing a user typed can start a header of its own.
func compose(from *mail.Address, msg Message, now time.Time, random io.Reader) ([]byte, error) {
	id := make([]byte, 16)
	if _, err := io.ReadFull(random, id); err != nil {
		return nil, err
	}
	_, domain, _ := strings.Cut(from.Address, "@")

	var body bytes.Buffer
	contentType := "text/plain; charset=utf-8"
	if msg.HTML == "" {
		writeQuotedPrintable(&body, msg.Text)
	} else {
		parts := multipart.NewWriter(&body)
		contentType = mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": parts.Boundary()})
		for _, part := range []struct{ contentType, content string }{
			{"text/plain; charset=utf-8", msg.Text},
			{"text/html; charset=utf-8", msg.HTML},
		} {
			header := textproto.MIMEHeader{}
			header.Set("Content-Type", part.contentType)
			header.Set("Content-Transfer-Encoding", "quoted-printable")
			// Writes to a bytes.Buffer cannot fail.
			w, _ := parts.CreatePart(header)
			writeQuotedPrintable(w, part.content)
		}
		_ = parts.Close()
	}

	var out bytes.Buffer
	for _, header := range [][2]string{
		{"From", from.String()},
		{"To", msg.To},
		{"Subject", mime.QEncoding.Encode("utf-8", msg.Subject)},
		{"Date", now.UTC().Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		// Asks other systems not to answer it with an autoreply.
		{"Auto-Submitted", "auto-generated"},
		{"Content-Type", contentType},
	} {
		out.WriteString(header[0] + ": " + header[1] + "\r\n")
	}
	if msg.HTML == "" {
		out.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	}
	out.WriteString("\r\n")
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// writeQuotedPrintable encodes text with CRLF line endings. Its writers are
// in-memory buffers, which cannot fail.
func writeQuotedPrintable(w io.Writer, text string) {
	qp := quotedprintable.NewWriter(w)
	_, _ = qp.Write([]byte(text))
	_ = qp.Close()
}
