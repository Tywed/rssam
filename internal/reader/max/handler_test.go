package max

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		RateLimitCooldown: 30 * time.Second,
		AllowPrivateAPI:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.Fetch(context.Background(), "https://max.ru/test_channel", FetchState{})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
}
