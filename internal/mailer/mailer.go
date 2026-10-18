// Package mailer sends trackslash's email. A Mailer delivers one Message and
// waits for the result: there is no outbox and no retry, so a caller learns at
// once whether the provider took the message.
//
// Three Mailers exist: SMTP for real delivery, Log for local development, and
// Capturing for tests.
package mailer

import (
	"context"
	"errors"
	"net/mail"
	"strings"
)

// Message is one email with a plain-text body and a minimal HTML alternative.
// It never carries attachments, images or tracking.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer delivers a Message. Send returns once the provider has accepted or
// refused it; a refusal, such as a provider over its quota, is an error.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// ErrInvalidMessage refuses a message that could not be written safely, such
// as a recipient that is not a single plain address.
var ErrInvalidMessage = errors.New("invalid email message")

// MaxAddressLength is the longest address SMTP can carry in a path.
const MaxAddressLength = 254

// ValidAddress reports whether s is one bare address, such as
// someone@example.com, that can be written into a header and an SMTP command
// as it is: no display name, no comments, no whitespace and no line breaks.
func ValidAddress(s string) bool {
	if s == "" || len(s) > MaxAddressLength || strings.ContainsAny(s, " \t\r\n<>,;") {
		return false
	}
	parsed, err := mail.ParseAddress(s)
	return err == nil && parsed.Name == "" && parsed.Address == s
}

func (m Message) validate() error {
	if !ValidAddress(m.To) {
		return errors.Join(ErrInvalidMessage, errors.New("recipient must be a single plain address"))
	}
	if strings.TrimSpace(m.Subject) == "" || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.Join(ErrInvalidMessage, errors.New("subject must be one non-empty line"))
	}
	if strings.TrimSpace(m.Text) == "" {
		return errors.Join(ErrInvalidMessage, errors.New("plain-text body required"))
	}
	return nil
}
