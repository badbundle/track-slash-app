package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	sendBuffer        = 64
	writeTimeout      = 5 * time.Second
	readMessageLimit  = 1 << 14 // 16 KiB; control frames are tiny
	resyncMessageType = "resync"
	resyncOverflow    = "overflow"
	resyncListener    = "listener_reconnected"
)

// Client wraps a single WebSocket connection. The read pump handles
// subscribe/unsubscribe control frames; the write pump drains events
// that the Hub publishes into the bounded send channel.
type Client struct {
	conn           *websocket.Conn
	hub            *Hub
	authorize      TopicAuthorizer
	send           chan Event
	control        chan serverControl
	disconnect     chan struct{}
	disconnectOnce sync.Once
}

func newClient(conn *websocket.Conn, hub *Hub, authorize TopicAuthorizer) *Client {
	conn.SetReadLimit(readMessageLimit)
	return &Client{
		conn:       conn,
		hub:        hub,
		authorize:  authorize,
		send:       make(chan Event, sendBuffer),
		control:    make(chan serverControl, 1),
		disconnect: make(chan struct{}),
	}
}

type controlMsg struct {
	Action string `json:"action"`
	Topic  string `json:"topic"`
}

type serverError struct {
	Error string `json:"error"`
}

type serverControl struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// run blocks until either pump exits, then tears down. It is called
// inline from the HTTP handler so the request goroutine stays alive
// for the duration of the WS connection.
func (c *Client) run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer c.hub.Remove(c)
	defer func() { _ = c.conn.CloseNow() }()

	go c.writePump(ctx, cancel)
	c.readPump(ctx)
}

func (c *Client) readPump(ctx context.Context) {
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var msg controlMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			c.writeError(ctx, "invalid json")
			continue
		}
		kind, id, terr := ParseTopic(msg.Topic)
		if terr != nil {
			c.writeError(ctx, terr.Error())
			continue
		}
		switch msg.Action {
		case "subscribe":
			disconnects := c.hub.disconnects.Load()
			// Without an authorizer every topic is open, members-only
			// events included.
			access := TopicAccess{MembersOnly: true}
			if c.authorize != nil {
				var err error
				access, err = c.authorize(ctx, kind, id)
				if err != nil {
					c.writeError(ctx, "forbidden")
					continue
				}
			}
			// Access changed while this was authorized: close the
			// connection, as DisconnectAll does, so the client reauthorizes.
			if !c.hub.subscribeAuthorized(c, msg.Topic, access, disconnects) {
				return
			}
		case "unsubscribe":
			c.hub.Unsubscribe(c, msg.Topic)
		default:
			c.writeError(ctx, `unknown action; want "subscribe" or "unsubscribe"`)
		}
	}
}

func (c *Client) writePump(ctx context.Context, cancel context.CancelFunc) {
	defer cancel()

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		// Recovery controls take priority over queued events so a slow client
		// learns that it must refetch before consuming more incremental state.
		select {
		case <-c.disconnect:
			return
		case msg := <-c.control:
			if err := c.writeJSON(ctx, msg); err != nil {
				return
			}
			continue
		default:
		}

		select {
		case <-ctx.Done():
			return
		case <-c.disconnect:
			return
		case msg := <-c.control:
			if err := c.writeJSON(ctx, msg); err != nil {
				return
			}
		case ev, ok := <-c.send:
			if !ok {
				return
			}
			if err := c.writeJSON(ctx, ev); err != nil {
				return
			}
		case <-pingTicker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		}
	}
}

func (c *Client) requestDisconnect() {
	if c.disconnect == nil {
		return
	}
	c.disconnectOnce.Do(func() { close(c.disconnect) })
}

func (c *Client) writeJSON(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := c.conn.Write(wctx, websocket.MessageText, data); err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("realtime client write: %v", err)
		}
		return err
	}
	return nil
}

func (c *Client) writeError(ctx context.Context, msg string) {
	_ = c.writeJSON(ctx, serverError{Error: msg})
}
