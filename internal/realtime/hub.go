package realtime

import (
	"sync"
	"sync/atomic"
)

// Hub fans events out from the single Postgres listener to the set of
// WebSocket clients that have subscribed to matching topics.
//
// Publish is non-blocking per client: if a client's send buffer is full,
// the event is dropped and a separate recovery control tells that client to
// refetch authoritative state. This prevents one slow consumer from stalling
// the listener without allowing it to diverge silently.
type Hub struct {
	mu      sync.RWMutex
	topics  map[string]map[*Client]TopicAccess
	dropped atomic.Uint64
	// disconnects counts DisconnectAll calls, so a subscription authorized
	// before one can tell its access may be out of date.
	disconnects atomic.Uint64
}

func NewHub() *Hub {
	return &Hub{topics: make(map[string]map[*Client]TopicAccess)}
}

// Subscribe joins the client to a topic without members-only access.
func (h *Hub) Subscribe(c *Client, topic string) {
	h.SubscribeWithAccess(c, topic, TopicAccess{})
}

// SubscribeWithAccess joins the client to a topic with the access its
// authorizer granted. Subscribing again replaces the earlier access.
func (h *Hub) SubscribeWithAccess(c *Client, topic string, access TopicAccess) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribeLocked(c, topic, access)
}

// subscribeAuthorized is SubscribeWithAccess for access authorized when
// DisconnectAll had run disconnects times. If it has run since, the client,
// which it may not have reached, isn't joined and false is returned.
func (h *Hub) subscribeAuthorized(c *Client, topic string, access TopicAccess, disconnects uint64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.disconnects.Load() != disconnects {
		return false
	}
	h.subscribeLocked(c, topic, access)
	return true
}

func (h *Hub) subscribeLocked(c *Client, topic string, access TopicAccess) {
	set, ok := h.topics[topic]
	if !ok {
		set = make(map[*Client]TopicAccess)
		h.topics[topic] = set
	}
	set[c] = access
}

func (h *Hub) Unsubscribe(c *Client, topic string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.topics[topic]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.topics, topic)
		}
	}
}

// Remove drops the client from every topic it was subscribed to.
// Call on disconnect.
func (h *Hub) Remove(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for topic, set := range h.topics {
		if _, ok := set[c]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.topics, topic)
			}
		}
	}
}

// Publish fans an event out to every subscriber of every matching topic.
// Each client is visited at most once even when both its topics match.
func (h *Hub) Publish(ev Event) {
	topics := ev.Topics()
	if len(topics) == 0 {
		return
	}

	h.mu.RLock()
	seen := make(map[*Client]struct{})
	for _, t := range topics {
		for c, access := range h.topics[t] {
			if ev.MembersOnly && !access.MembersOnly {
				continue
			}
			if _, dup := seen[c]; dup {
				continue
			}
			seen[c] = struct{}{}
		}
	}
	h.mu.RUnlock()

	for c := range seen {
		select {
		case c.send <- ev:
		default:
			h.dropped.Add(1)
			c.requestResync(resyncOverflow)
		}
	}
}

// ResyncAll tells every subscribed client to refetch authoritative state.
// Controls use a separate one-slot channel, so the signal still reaches a
// client whose event queue is full and repeated signals are coalesced.
func (h *Hub) ResyncAll(reason string) {
	h.mu.RLock()
	clients := make(map[*Client]struct{})
	for _, set := range h.topics {
		for c := range set {
			clients[c] = struct{}{}
		}
	}
	h.mu.RUnlock()

	for c := range clients {
		c.requestResync(reason)
	}
}

// DisconnectAll closes every subscribed client. Access-control mutations use
// this rare, coarse operation so revoked clients must reauthorize before they
// can receive another event without adding database work to normal fanout.
func (h *Hub) DisconnectAll() {
	// Counted first, so a subscription authorized before this either
	// registers in time to be closed below or sees the count change.
	h.disconnects.Add(1)
	h.mu.RLock()
	clients := make(map[*Client]struct{})
	for _, set := range h.topics {
		for c := range set {
			clients[c] = struct{}{}
		}
	}
	h.mu.RUnlock()
	for c := range clients {
		c.requestDisconnect()
	}
}

func (c *Client) requestResync(reason string) {
	select {
	case c.control <- serverControl{Type: resyncMessageType, Reason: reason}:
	default:
	}
}

// Dropped returns the total number of events dropped due to slow consumers.
// Exposed for metrics / tests.
func (h *Hub) Dropped() uint64 { return h.dropped.Load() }

// TopicCount returns the number of distinct topics currently subscribed to.
// Exposed for tests / debugging.
func (h *Hub) TopicCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.topics)
}
