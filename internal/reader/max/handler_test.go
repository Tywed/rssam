package max

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/ssrf"
)

func TestHandler_Fetch_InitialNoAfter(t *testing.T) {
	var gotAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAfter = r.URL.Query().Get("after")
		_ = json.NewEncoder(w).Encode(APIResponse{
			Messages: []APIMessage{
				{Time: 1000, Text: "one", ID: "1"},
				{Time: 2000, Text: "two", ID: "2"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(srv.Client(), guard, Config{
		APIBaseURL:      srv.URL,
		DefaultLookback: 24 * time.Hour,
		AllowPrivateAPI: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := h.Fetch(context.Background(), "https://max.ru/rosgvard_krd", FetchState{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotAfter != "" {
		t.Fatalf("first fetch must not send after, got %q", gotAfter)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("entries=%d, want 2", len(res.Entries))
	}
}

func TestHandler_Fetch_Incremental(t *testing.T) {
	var gotAfter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channel/rosgvard_krd/messages" {
			http.NotFound(w, r)
			return
		}
		gotAfter = r.URL.Query().Get("after")
		_ = json.NewEncoder(w).Encode(APIResponse{
			Messages: []APIMessage{
				{Time: 2000, Text: "old", ID: "1"},
				{Time: 3000, Text: "new", ID: "2", MessageURL: "https://max.ru/rosgvard_krd/2"},
			},
		})
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(srv.Client(), guard, Config{
		APIBaseURL:        srv.URL,
		DefaultLimit:      50,
		DefaultLookback:   24 * time.Hour,
		Overlap:           2 * time.Minute,
		RateLimitCooldown: time.Minute,
		AllowPrivateAPI:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	lastEnd := int64(2500)
	res, err := h.Fetch(context.Background(), "https://max.ru/rosgvard_krd", FetchState{
		LastEndTimeMs: lastEnd,
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotAfter == "" {
		t.Fatal("expected after query param")
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d, want 1 skipped old message", len(res.Entries))
	}
	if res.Entries[0].Title != "new" {
		t.Fatalf("title=%q", res.Entries[0].Title)
	}
	if res.State.LastEndTimeMs <= lastEnd {
		t.Fatalf("expected cursor advance, state=%+v", res.State)
	}
}

func TestHandler_Fetch_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(srv.Client(), guard, Config{
		APIBaseURL:        srv.URL,
		RateLimitCooldown: 40 * time.Millisecond,
		AllowPrivateAPI:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.Fetch(context.Background(), "https://max.ru/test_channel", FetchState{})
	if _, ok := IsBackoff(err); !ok {
		t.Fatalf("expected backoff after 429, err=%v", err)
	}

	start := time.Now()
	_, err = h.Fetch(context.Background(), "https://max.ru/other_channel", FetchState{})
	if _, ok := IsBackoff(err); !ok {
		t.Fatalf("second fetch should backoff immediately, err=%v", err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatalf("busy slot must not block the worker, took %s", time.Since(start))
	}
}

func TestHandler_Fetch_SerializesRequests(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			cur := maxInFlight.Load()
			if n <= cur || maxInFlight.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		inFlight.Add(-1)
		_ = json.NewEncoder(w).Encode(APIResponse{})
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(srv.Client(), guard, Config{
		APIBaseURL:      srv.URL,
		RequestInterval: 30 * time.Millisecond,
		AllowPrivateAPI: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var okN atomic.Int32
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.Fetch(context.Background(), "https://max.ru/ch"+string(rune('a'+i)), FetchState{})
			if err == nil {
				okN.Add(1)
				return
			}
			if _, yes := IsBackoff(err); !yes {
				t.Errorf("fetch: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if maxInFlight.Load() != 1 {
		t.Fatalf("max in-flight=%d, want 1", maxInFlight.Load())
	}
	if okN.Load() < 1 {
		t.Fatal("expected at least one fetch to reach the API")
	}
}

func TestHandler_Fetch_ConcurrentSlots(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			cur := maxInFlight.Load()
			if n <= cur || maxInFlight.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		inFlight.Add(-1)
		_ = json.NewEncoder(w).Encode(APIResponse{})
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(srv.Client(), guard, Config{
		APIBaseURL:      srv.URL,
		ConcurrentSlots: 3,
		AllowPrivateAPI: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var okN atomic.Int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := h.Fetch(context.Background(), "https://max.ru/ch"+string(rune('a'+i)), FetchState{})
			if err == nil {
				okN.Add(1)
				return
			}
			if _, yes := IsBackoff(err); !yes {
				t.Errorf("fetch: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if maxInFlight.Load() != 3 {
		t.Fatalf("max in-flight=%d, want 3", maxInFlight.Load())
	}
	if okN.Load() < 3 {
		t.Fatalf("expected at least 3 fetches to reach the API, got %d", okN.Load())
	}
}
