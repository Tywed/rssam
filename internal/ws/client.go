package ws

import (
	"encoding/json"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

type subscribeRequest struct {
	Action   string   `json:"action"`
	Channels []string `json:"channels"`
}

type Client struct {
	conn *websocket.Conn
	hub  *Hub

	// UserID is the authenticated owner of this connection. Tenant-scoped
	// events are only delivered when it matches the publishing user.
	UserID int64

	send chan []byte

	mu            sync.RWMutex
	subscriptions map[string]struct{}
	closed        bool
}

func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return NewUserClient(hub, conn, 0)
}

// NewUserClient creates a client bound to userID.
// MaxInboundFrameBytes caps a single client→server WebSocket frame.
const MaxInboundFrameBytes = 16 << 10

func NewUserClient(hub *Hub, conn *websocket.Conn, userID int64) *Client {
	return &Client{
		conn:          conn,
		hub:           hub,
		UserID:        userID,
		send:          make(chan []byte, hub.ClientBuffer()),
		subscriptions: make(map[string]struct{}),
	}
}

func (c *Client) Run() {
	go c.writePump()
	c.readPump()
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.conn.Close()
}

func (c *Client) readPump() {
	defer c.Close()
	defer c.hub.Unregister(c)

	readTimeout := max(c.hub.PingInterval()*3, 5*time.Second)
	_ = c.conn.SetDeadline(time.Now().Add(readTimeout))

	for {
		var data []byte
		if err := websocket.Message.Receive(c.conn, &data); err != nil {
			return
		}
		_ = c.conn.SetDeadline(time.Now().Add(readTimeout))
		if string(data) == `{"action":"pong"}` {
			continue
		}
		var req subscribeRequest
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		if req.Action != "subscribe" {
			continue
		}
		channels := NormalizeChannels(req.Channels)
		c.setSubscriptions(channels)
		// Acknowledge so a client can know when events will start flowing
		// instead of guessing with a delay. Older clients ignore unknown events.
		if ack, err := json.Marshal(Envelope{Event: "subscribed", Data: map[string]any{"channels": channels}}); err == nil {
			select {
			case c.send <- ack:
			default:
			}
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(c.hub.PingInterval())
	defer ticker.Stop()
	defer c.Close()

	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				return
			}
			if err := websocket.Message.Send(c.conn, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := websocket.Message.Send(c.conn, []byte(`{"event":"ping"}`)); err != nil {
				return
			}
		}
	}
}

func (c *Client) setSubscriptions(channels []string) {
	next := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		next[ch] = struct{}{}
	}
	c.mu.Lock()
	c.subscriptions = next
	c.mu.Unlock()
}

func (c *Client) isSubscribed(channels map[string]struct{}) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.subscriptions) == 0 {
		return false
	}
	for ch := range channels {
		if _, ok := c.subscriptions[ch]; ok {
			return true
		}
	}
	return false
}
