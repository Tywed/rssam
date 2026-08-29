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

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; !ok {
		return
	}
	delete(h.clients, c)
	close(c.send)
}

func (h *Hub) Publish(channels []string, event Envelope) {
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

	h.mu.RLock()
	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		if !c.isSubscribed(uniq) {
			continue
		}
		select {
		case c.send <- payload:
		default:
			// Медленного клиента отключаем, чтобы не тормозить broadcast.
			c.Close()
			h.Unregister(c)
		}
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
