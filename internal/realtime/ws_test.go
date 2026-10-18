package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		policy OriginPolicy
		want   bool
	}{
		{"empty policy rejects browser origin", "https://x.com", OriginPolicy{}, false},
		{"empty policy rejects missing origin", "", OriginPolicy{}, false},
		{"non-browser policy allows missing origin", "", OriginPolicy{AllowMissingOrigin: true}, true},
		{"exact match", "https://app.example.com", OriginPolicy{AllowedOrigins: []string{"https://app.example.com"}}, true},
		{"second entry matches", "http://localhost:3000", OriginPolicy{AllowedOrigins: []string{"https://app.example.com", "http://localhost:3000"}}, true},
		{"mismatch rejected", "https://evil.com", OriginPolicy{AllowedOrigins: []string{"https://app.example.com"}}, false},
		{"port mismatch rejected", "http://localhost:3001", OriginPolicy{AllowedOrigins: []string{"http://localhost:3000"}}, false},
		{"scheme mismatch rejected", "http://app.example.com", OriginPolicy{AllowedOrigins: []string{"https://app.example.com"}}, false},
		{"localhost development", "http://localhost:5173", OriginPolicy{AllowLocalhostOrigins: true}, true},
		{"loopback development", "http://127.0.0.1:5173", OriginPolicy{AllowLocalhostOrigins: true}, true},
		{"localhost trailing slash", "http://localhost:5173/", OriginPolicy{AllowLocalhostOrigins: true}, true},
		{"malformed development origin", "http://[::1", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"development origin requires HTTP", "ftp://localhost:5173", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"development origin requires host", "http:", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"development origin rejects userinfo", "http://user@localhost:5173", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"development origin rejects query", "http://localhost:5173?x=1", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"development origin rejects fragment", "http://localhost:5173#x", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"localhost path rejected", "http://localhost:5173/app", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"non-loopback IP rejected", "http://192.0.2.1:5173", OriginPolicy{AllowLocalhostOrigins: true}, false},
		{"non-local development origin rejected", "https://track.example.com", OriginPolicy{AllowLocalhostOrigins: true}, false},
	}
	for _, c := range cases {
		if got := c.policy.Allows(c.origin); got != c.want {
			t.Errorf("%s: Allows(%q) = %v, want %v", c.name, c.origin, got, c.want)
		}
	}
}

// TestHandlerRejectsDisallowedOrigin pins the 403 path so a regression in the
// origin gate is loud. A real WS upgrade isn't needed: the gate runs before
// Accept.
func TestHandlerRejectsDisallowedOrigin(t *testing.T) {
	hub := NewHub()
	h := hub.Handler(OriginPolicy{AllowedOrigins: []string{"https://app.example.com"}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
}

func TestHandlerAcceptsAllowedOriginPreUpgrade(t *testing.T) {
	// We can't complete a real upgrade against httptest.NewRecorder (no
	// hijacker). We only assert the gate doesn't 403 the request — the
	// downstream Accept will fail to upgrade, which is fine: nothing
	// writes a body and the handler returns.
	hub := NewHub()
	h := hub.Handler(OriginPolicy{AllowedOrigins: []string{"https://app.example.com"}}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("allowed origin was forbidden")
	}
}

func TestHandlerSendsResyncControlToSubscribedClient(t *testing.T) {
	hub := NewHub()
	ts := httptest.NewServer(hub.Handler(OriginPolicy{AllowMissingOrigin: true}, nil))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	topic := ProjectTopic(uuid.New())
	subscribe, err := json.Marshal(controlMsg{Action: "subscribe", Topic: topic})
	if err != nil {
		t.Fatalf("marshal subscribe: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, subscribe); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	for hub.TopicCount() != 1 {
		select {
		case <-ctx.Done():
			t.Fatal("subscription was not registered")
		case <-time.After(time.Millisecond):
		}
	}

	hub.ResyncAll(resyncOverflow)
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read resync: %v", err)
	}
	var got serverControl
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal resync: %v", err)
	}
	if got.Type != resyncMessageType || got.Reason != resyncOverflow {
		t.Fatalf("control = %#v, want overflow resync", got)
	}
}

// TestHandlerAppliesAuthorizedTopicAccess checks the access the authorizer
// grants decides whether members-only events reach the connection.
func TestHandlerAppliesAuthorizedTopicAccess(t *testing.T) {
	for _, tc := range []struct {
		name      string
		authorize TopicAuthorizer
		want      bool
	}{
		{"viewer", func(context.Context, string, uuid.UUID) (TopicAccess, error) { return TopicAccess{}, nil }, false},
		{"member", func(context.Context, string, uuid.UUID) (TopicAccess, error) {
			return TopicAccess{MembersOnly: true}, nil
		}, true},
		{"no authorizer", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := NewHub()
			ts := httptest.NewServer(hub.Handler(OriginPolicy{AllowMissingOrigin: true}, tc.authorize))
			defer ts.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer func() { _ = conn.CloseNow() }()

			projectID := uuid.New()
			subscribe, err := json.Marshal(controlMsg{Action: "subscribe", Topic: ProjectTopic(projectID)})
			if err != nil {
				t.Fatalf("marshal subscribe: %v", err)
			}
			if err := conn.Write(ctx, websocket.MessageText, subscribe); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			for hub.TopicCount() != 1 {
				select {
				case <-ctx.Done():
					t.Fatal("subscription was not registered")
				case <-time.After(time.Millisecond):
				}
			}

			hidden := uuid.New()
			hub.Publish(Event{Op: OpInsert, Entity: EntityComment, ID: hidden, ProjectID: &projectID, MembersOnly: true})
			shared := uuid.New()
			hub.Publish(Event{Op: OpInsert, Entity: EntityComment, ID: shared, ProjectID: &projectID})

			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var got Event
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			want := shared
			if tc.want {
				want = hidden
			}
			if got.ID != want {
				t.Fatalf("first event = %s, want %s", got.ID, want)
			}
		})
	}
}

// TestHandlerClosesSubscriptionAuthorizedBeforeDisconnectAll checks a
// subscription whose authorization raced a DisconnectAll, which couldn't
// reach a connection with no topics yet, closes the connection instead of
// keeping access granted before the change.
func TestHandlerClosesSubscriptionAuthorizedBeforeDisconnectAll(t *testing.T) {
	hub := NewHub()
	authorize := func(context.Context, string, uuid.UUID) (TopicAccess, error) {
		// Access changes, and every registered client is closed, while
		// this subscription is being authorized.
		hub.DisconnectAll()
		return TopicAccess{MembersOnly: true}, nil
	}
	ts := httptest.NewServer(hub.Handler(OriginPolicy{AllowMissingOrigin: true}, authorize))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	subscribe, err := json.Marshal(controlMsg{Action: "subscribe", Topic: ProjectTopic(uuid.New())})
	if err != nil {
		t.Fatalf("marshal subscribe: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, subscribe); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, data, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("connection stayed open: %s, %v", data, err)
	}
	for hub.TopicCount() != 0 {
		select {
		case <-ctx.Done():
			t.Fatalf("stale subscription is still registered: %d topics", hub.TopicCount())
		case <-time.After(time.Millisecond):
		}
	}
}
