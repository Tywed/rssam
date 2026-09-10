package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingTransport struct {
	inFlight, maxInFlight atomic.Int32
	body                  []byte
}

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	n := c.inFlight.Add(1)
	for {
		cur := c.maxInFlight.Load()
		if n <= cur || c.maxInFlight.CompareAndSwap(cur, n) {
			break
		}
	}
	time.Sleep(30 * time.Millisecond)
	c.inFlight.Add(-1)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(c.body)), Header: http.Header{}}, nil
}

func TestHandler_Fetch_ConcurrentSlots(t *testing.T) {
	tr := &countingTransport{body: fixtureMessageHTML}
	h := NewHandler(nil, &http.Client{Transport: tr}, Config{UseProxy: false, ConcurrentSlots: 2})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := h.Fetch(context.Background(), "https://t.me/s/demo", nil); err != nil {
				t.Errorf("fetch: %v", err)
			}
		})
	}
	wg.Wait()
	if got := tr.maxInFlight.Load(); got != 2 {
		t.Fatalf("max in-flight = %d, want 2", got)
	}
}

func TestHandler_Fetch_SlotWaitTimesOutAsRetry(t *testing.T) {
	h := NewHandler(nil, &http.Client{Transport: &countingTransport{}}, Config{ConcurrentSlots: 1})
	if err := h.slots.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer h.slots.release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := h.Fetch(ctx, "https://t.me/s/demo", nil)
	var busy *ErrSlotsBusy
	if !errors.As(err, &busy) || time.Until(busy.RetryAt()) <= 0 {
		t.Fatalf("want ErrSlotsBusy with a future RetryAt, got %v", err)
	}
}

func TestFetchSlots_RaisingLimitWakesWaiters(t *testing.T) {
	s := newFetchSlots(1)
	if err := s.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.acquire(context.Background()) }()
	select {
	case <-done:
		t.Fatal("second acquire must block at limit 1")
	case <-time.After(20 * time.Millisecond):
	}
	s.setLimit(2)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not woken after setLimit")
	}
}
