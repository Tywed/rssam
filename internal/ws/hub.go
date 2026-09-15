package ws

import (
	"encoding/json"
	"fmt"
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
		clientBuffer: clientBuffer,
		pingInterval: pingInterval,
	}
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
		if strings.HasPrefix(ch, "feed:") {
			if _, err := parseChannelID(ch, "feed:"); err != nil {
				continue
			}
		}
		if strings.HasPrefix(ch, "category:") {
			if _, err := parseChannelID(ch, "category:"); err != nil {
				continue
			}
		}
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		out = append(out, ch)
	}
	return out
}

func parseChannelID(ch, prefix string) (int64, error) {
	raw := strings.TrimPrefix(ch, prefix)
	if raw == "" {
		return 0, fmt.Errorf("invalid channel %q", ch)
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid channel %q", ch)
	}
	return id, nil
}
