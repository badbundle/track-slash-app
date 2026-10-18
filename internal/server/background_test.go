package server

import (
	"context"
	"errors"
	"testing"
)

type backgroundTestKey struct{}

// Work left running after a response keeps the request's values but not its
// cancellation, has a deadline of its own, and can be waited for.
func TestGoBackgroundOutlivesTheRequest(t *testing.T) {
	t.Parallel()
	s := &Server{}
	request, endRequest := context.WithCancel(context.WithValue(context.Background(), backgroundTestKey{}, "request value"))
	started := make(chan struct{})
	release := make(chan struct{})
	var value any
	var hasDeadline bool
	var ctxErr error
	s.goBackground(request, "test", func(ctx context.Context) {
		close(started)
		<-release
		value = ctx.Value(backgroundTestKey{})
		_, hasDeadline = ctx.Deadline()
		ctxErr = ctx.Err()
	})
	<-started
	endRequest()

	// A wait that runs out first says so and leaves the work running.
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.WaitBackground(expired); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitBackground with an ended context = %v, want context.Canceled", err)
	}
	close(release)
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
	if value != "request value" || !hasDeadline || ctxErr != nil {
		t.Fatalf("background context: value %v, deadline %v, err %v", value, hasDeadline, ctxErr)
	}
}

// A panic in background work is logged, not fatal to the process.
func TestGoBackgroundRecoversAPanic(t *testing.T) {
	t.Parallel()
	s := &Server{}
	s.goBackground(context.Background(), "test panic", func(context.Context) { panic("boom") })
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
}
