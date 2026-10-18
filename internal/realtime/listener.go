package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bradleymackey/track-slash/internal/config"
)

// pgChannel is the single Postgres NOTIFY channel all triggers emit on.
// In-memory fanout in Hub does the per-topic routing.
const pgChannel = "track_events"

// Listener owns a dedicated Postgres connection that LISTENs on pgChannel
// and forwards every notification into the Hub.
//
// A dedicated connection is required because LISTEN is session-scoped:
// pooled connections would silently lose the subscription on release.
type Listener struct {
	dbURL           string
	hub             *Hub
	applicationName string
}

func NewListener(dbURL string, hub *Hub) *Listener {
	return &Listener{
		dbURL:           dbURL,
		hub:             hub,
		applicationName: fmt.Sprintf("track-slash-realtime-%016x", rand.Uint64()),
	}
}

// Run blocks until ctx is cancelled. On any connection / decode error it
// logs, backs off, and reconnects. Every successful LISTEN registration asks
// subscribed clients to refetch because events may have been lost while the
// listener was disconnected.
func (l *Listener) Run(ctx context.Context) {
	var backoff reconnectBackoff

	for {
		if ctx.Err() != nil {
			return
		}
		listened, err := l.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		delay := backoff.next(listened)
		if err != nil {
			log.Printf("realtime listener: %v (reconnect in %s)", err, delay)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

const (
	minReconnectBackoff = 250 * time.Millisecond
	maxReconnectBackoff = 30 * time.Second
)

// reconnectBackoff spaces out the listener's reconnect attempts.
type reconnectBackoff struct {
	current time.Duration
}

// next returns a jittered delay before the next reconnect attempt. Each
// attempt that fails before LISTEN doubles the delay, up to the maximum. An
// attempt that reached LISTEN held a working connection, so its drop starts
// over from the minimum instead of inheriting earlier failures' delay.
func (b *reconnectBackoff) next(listened bool) time.Duration {
	if listened || b.current == 0 {
		b.current = minReconnectBackoff
	}
	delay := b.current/2 + time.Duration(rand.Int64N(int64(b.current)))
	b.current = min(b.current*2, maxReconnectBackoff)
	return delay
}

// runOnce holds one listening connection until it fails. listened reports
// whether it got as far as LISTEN.
func (l *Listener) runOnce(ctx context.Context) (listened bool, err error) {
	connConfig, err := config.DirectConnConfig(l.dbURL)
	if err != nil {
		return false, err
	}
	connConfig.RuntimeParams["application_name"] = l.applicationName
	conn, err := pgx.ConnectConfig(ctx, connConfig)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close(context.Background()) }()

	if _, err := conn.Exec(ctx, "LISTEN "+pgChannel); err != nil {
		return false, err
	}
	log.Printf("realtime listener: connected, LISTEN %s", pgChannel)
	l.hub.ResyncAll(resyncListener)

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, err
		}
		var ev Event
		if err := json.Unmarshal([]byte(n.Payload), &ev); err != nil {
			log.Printf("realtime listener: bad payload %q: %v", n.Payload, err)
			continue
		}
		l.hub.Publish(ev)
	}
}
