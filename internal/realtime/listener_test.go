package realtime

import (
	"context"
	"testing"
	"time"
)

func TestListenerRunOnceRejectsInvalidDatabaseURL(t *testing.T) {
	t.Parallel()
	listener := NewListener("://invalid", NewHub())
	listened, err := listener.runOnce(context.Background())
	if err == nil {
		t.Fatal("runOnce accepted invalid database URL")
	}
	if listened {
		t.Fatal("runOnce reported LISTEN without a connection")
	}
}

func TestReconnectBackoffDoublesUntilCapped(t *testing.T) {
	t.Parallel()
	var backoff reconnectBackoff
	base := minReconnectBackoff
	for range 10 {
		requireBackoffDelay(t, backoff.next(false), base)
		base = min(base*2, maxReconnectBackoff)
	}
	requireBackoffDelay(t, backoff.next(false), maxReconnectBackoff)
}

func TestReconnectBackoffRestartsAfterListening(t *testing.T) {
	t.Parallel()
	var backoff reconnectBackoff
	for range 10 {
		backoff.next(false)
	}
	requireBackoffDelay(t, backoff.next(true), minReconnectBackoff)
	requireBackoffDelay(t, backoff.next(false), 2*minReconnectBackoff)
}

// requireBackoffDelay checks delay lies in the jitter window around base.
func requireBackoffDelay(t *testing.T, delay, base time.Duration) {
	t.Helper()
	if delay < base/2 || delay >= base/2+base {
		t.Fatalf("delay = %s, want within [%s, %s)", delay, base/2, base/2+base)
	}
}

func TestListenersUseDistinctApplicationNames(t *testing.T) {
	t.Parallel()
	first := NewListener("postgres://example", NewHub())
	second := NewListener("postgres://example", NewHub())
	if first.applicationName == second.applicationName {
		t.Fatalf("application names are both %q", first.applicationName)
	}
}
