package mailer

import (
	"context"
	"log"
)

// Log prints each message, link included, to the server log instead of
// sending it. TRACK_SLASH_SMTP_HOST=log selects it for local development.
type Log struct {
	logger *log.Logger
	from   string
}

var _ Mailer = (*Log)(nil)

// NewLog returns a Log that writes to logger, or to the standard logger when
// logger is nil.
func NewLog(logger *log.Logger, from string) *Log {
	if logger == nil {
		logger = log.Default()
	}
	return &Log{logger: logger, from: from}
}

func (l *Log) Send(_ context.Context, msg Message) error {
	if err := msg.validate(); err != nil {
		return err
	}
	l.logger.Printf("email not sent (TRACK_SLASH_SMTP_HOST=log)\nFrom: %s\nTo: %s\nSubject: %s\n\n%s\n", l.from, msg.To, msg.Subject, msg.Text)
	return nil
}
