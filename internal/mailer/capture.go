package mailer

import (
	"context"
	"sync"
)

// Capturing keeps every message it is given instead of sending it, for tests.
// Fail makes it refuse messages the way a provider that is down or over its
// quota would.
type Capturing struct {
	mu       sync.Mutex
	err      error
	attempts int
	messages []Message
}

var _ Mailer = (*Capturing)(nil)

func NewCapturing() *Capturing {
	return &Capturing{}
}

func (c *Capturing) Send(_ context.Context, msg Message) error {
	if err := msg.validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	if c.err != nil {
		return c.err
	}
	c.messages = append(c.messages, msg)
	return nil
}

// Fail makes every later Send return err, keeping nothing. Fail(nil) makes it
// accept messages again.
func (c *Capturing) Fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
}

// Messages returns the messages accepted so far, oldest first.
func (c *Capturing) Messages() []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Message(nil), c.messages...)
}

// Attempts counts every valid message offered, accepted or refused.
func (c *Capturing) Attempts() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}
