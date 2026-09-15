package ws

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// Real connections, tiny buffer, clients that never read: publish overflows
// and evicts (Close + Unregister) from the publishing goroutine while the
// peer disconnects and readPump unregisters concurrently. Neither side may
// send on, or close, an already closed channel — a panic there is not
// recovered and takes the whole process down.
func TestHub_PublishRacesUnregister(t *testing.T) {
	hub := NewHub(1, time.Minute)
	srv := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		c := NewUserClient(hub, conn, 1)
		hub.Register(c)
		c.Run()
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	for round := 0; round < 30; round++ {
		conns := make([]*websocket.Conn, 16)
		for i := range conns {
			conn, err := websocket.Dial(url, "", "http://localhost/")
			if err != nil {
				t.Fatal(err)
			}
			conns[i] = conn
			if err := websocket.Message.Send(conn, `{"action":"subscribe","channels":["all"]}`); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			hub.mu.RLock()
			n := len(hub.clients)
			hub.mu.RUnlock()
			if n == len(conns) || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				hub.PublishToUser(1, []string{ChannelAll}, Envelope{Event: "e", Data: i})
			}
		}()
		go func() {
			defer wg.Done()
			for _, c := range conns {
				_ = c.Close()
			}
		}()
		wg.Wait()
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hub.mu.RLock()
		n := len(hub.clients)
		hub.mu.RUnlock()
		if n == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	t.Fatalf("%d clients still registered after all connections closed", len(hub.clients))
}

func TestHub_RegisterAfterUnregisterIsIgnored(t *testing.T) {
	hub := NewHub(4, time.Minute)
	c := &Client{hub: hub, UserID: 1, send: make(chan []byte, 4), subscriptions: map[string]struct{}{ChannelAll: {}}}
	hub.Register(c)
	hub.Unregister(c)
	hub.Register(c)
	hub.PublishToUser(1, []string{ChannelAll}, Envelope{Event: "e"})
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	if len(hub.clients) != 0 {
		t.Fatal("unregistered client came back")
	}
}
