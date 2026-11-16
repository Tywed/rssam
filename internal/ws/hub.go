package ws

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ChannelAll = "all"
)

type Envelope struct {
	Event string `json:"event"`
	Data  any    `json:"data"`
}

type Hub struct {
	mu           sync.RWMutex
	clients      map[*Client]struct{}
	perUser      map[int64]int
	clientBuffer int
	pingInterval time.Duration
}

func NewHub(clientBuffer int, pingInterval time.Duration) *Hub {
	if clientBuffer <= 0 {
		clientBuffer = 100
	}
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}
	return &Hub{
		clients:      make(map[*Client]struct{}),
		perUser:      make(map[int64]int),
		clientBuffer: clientBuffer,
		pingInterval: pingInterval,
	}
}

// HasClients reports whether anyone is connected; publishers that would
// have to load data for an event (feed status → subscriber list) ask first.
func (h *Hub) HasClients() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients) > 0
}

func (h *Hub) ClientBuffer() int {
	return h.clientBuffer
}

func (h *Hub) PingInterval() time.Duration {
	return h.pingInterval
}

// Register is a no-op for a client that was already unregistered: its send
// channel is closed and a later publish would panic on it.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.gone {
		return
	}
	h.clients[c] = struct{}{}
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	c.gone = true
	close(c.send)
}

// Reserve takes one of the user's MaxConnectionsPerUser slots before the
// upgrade, so the check and the count move together; the caller must
// Release when the socket is gone.
func (h *Hub) Reserve(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perUser[userID] >= MaxConnectionsPerUser {
		return false
	}
	h.perUser[userID]++
	return true
}

func (h *Hub) Release(userID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perUser[userID] <= 1 {
		delete(h.perUser, userID)
		return
	}
	h.perUser[userID]--
}

// UserConnections reports the user's reserved slots.
func (h *Hub) UserConnections(userID int64) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.perUser[userID]
}

// MaxConnectionsPerUser bounds sockets per account: a browser needs one per
// tab, anything beyond this is a leak or abuse. The limit is per process.
const MaxConnectionsPerUser = 10

// KickSession closes every socket opened with sessionID.
func (h *Hub) KickSession(sessionID string) {
	if sessionID == "" {
		return
	}
	h.kick(func(c *Client) bool { return c.SessionID == sessionID })
}

// KickUser closes every socket of userID; keepSessionID (may be empty) is
// spared, mirroring DeleteUserSessionsExcept.
func (h *Hub) KickUser(userID int64, keepSessionID string) {
	h.kick(func(c *Client) bool {
		return c.UserID == userID && (keepSessionID == "" || c.SessionID != keepSessionID)
	})
}

// KickKey closes every socket authenticated with API key keyID.
func (h *Hub) KickKey(keyID int64) {
	if keyID <= 0 {
		return
	}
	h.kick(func(c *Client) bool { return c.KeyID == keyID })
}

// kick collects matches under the read lock and closes them outside it:
// Close makes readPump return, which takes the write lock in Unregister.
func (h *Hub) kick(match func(*Client) bool) {
	h.mu.RLock()
	var victims []*Client
	for c := range h.clients {
		if match(c) {
			victims = append(victims, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range victims {
		c.Close()
	}
}

// PublishToUser delivers event only to clients authenticated as userID.
// There is no owner-less broadcast: every event carries tenant data.
func (h *Hub) PublishToUser(userID int64, channels []string, event Envelope) {
	if userID <= 0 {
		return
	}
	uniq := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		ch = strings.TrimSpace(ch)
		if ch == "" {
			continue
		}
		uniq[ch] = struct{}{}
	}
	if len(uniq) == 0 {
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}

	// The send happens under the read lock: Unregister closes c.send under
	// the write lock, so a client present in the map has an open channel.
	// Sending after a snapshot let a concurrent Unregister close the channel
	// first — an unrecovered "send on closed channel" in a worker goroutine.
	// The non-blocking send never waits on a client, so holding the lock
	// costs nothing; eviction needs the write lock and runs afterwards.
	var slow []*Client
	h.mu.RLock()
	for c := range h.clients {
		if c.UserID != userID {
			continue
		}
		if !c.isSubscribed(uniq) {
			continue
		}
		select {
		case c.send <- payload:
		default:
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range slow {
		c.Close()
		h.Unregister(c)
	}
}

func NormalizeChannels(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, ch := range in {
		ch = strings.ToLower(strings.TrimSpace(ch))
		if ch == "" {
			continue
		}
		if ch != ChannelAll && !strings.HasPrefix(ch, "feed:") && !strings.HasPrefix(ch, "category:") {
			continue
		}
		if strings.HasPrefix(ch, "feed:") && !validChannelID(ch, "feed:") {
			continue
		}
		if strings.HasPrefix(ch, "category:") && !validChannelID(ch, "category:") {
			continue
		}
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		out = append(out, ch)
	}
	return out
}

func validChannelID(ch, prefix string) bool {
	id, err := strconv.ParseInt(strings.TrimPrefix(ch, prefix), 10, 64)
	return err == nil && id > 0
}
