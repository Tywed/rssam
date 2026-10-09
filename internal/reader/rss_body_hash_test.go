package reader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const bodyHashFeed = `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>
<item><title>one</title><link>https://example.com/1</link></item></channel></rss>`

func TestRSSHandler_BodyHashSkipsUnchangedBody(t *testing.T) {
	var body atomic.Value
	body.Store(bodyHashFeed)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(ts.Close)

	h := NewRSSHandler(NewRSSFetcher(&http.Client{Timeout: 2 * time.Second}, "rssam-test", nil, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	first, err := h.Fetch(ctx, FetchRequest{FeedURL: ts.URL})
	if err != nil || first.NotModified || len(first.Entries) != 1 {
		t.Fatalf("first: %+v err=%v", first, err)
	}
	var st rssState
	if err := decodeRSSState(first.BridgeState, &st); err != nil || st.Body == "" {
		t.Fatalf("body hash not stored: %s", first.BridgeState.Marshal())
	}

	second, err := h.Fetch(ctx, FetchRequest{FeedURL: ts.URL, BridgeState: first.BridgeState})
	if err != nil || !second.NotModified || len(second.Entries) != 0 {
		t.Fatalf("same body must be NotModified: %+v err=%v", second, err)
	}
	if string(second.BridgeState.Marshal()) != string(first.BridgeState.Marshal()) {
		t.Fatal("unchanged body must keep the state")
	}

	body.Store(bodyHashFeed + "<!-- changed -->")
	third, err := h.Fetch(ctx, FetchRequest{FeedURL: ts.URL, BridgeState: first.BridgeState})
	if err != nil || third.NotModified || len(third.Entries) != 1 {
		t.Fatalf("changed body must be parsed: %+v err=%v", third, err)
	}
	var st3 rssState
	_ = decodeRSSState(third.BridgeState, &st3)
	if st3.Body == st.Body || st3.Body == "" {
		t.Fatal("changed body must store a new hash")
	}

	// Without a stored hash (manual refresh drops it) the body is parsed.
	fourth, err := h.Fetch(ctx, FetchRequest{FeedURL: ts.URL})
	if err != nil || fourth.NotModified {
		t.Fatalf("no stored hash must parse: %+v err=%v", fourth, err)
	}
}

func TestRSSHandler_BodyHashKeptOn304(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(ts.Close)
	h := NewRSSHandler(NewRSSFetcher(&http.Client{Timeout: 2 * time.Second}, "rssam-test", nil, ""))
	state := BridgeState{}.With(FeedTypeRSS, []byte(`{"body":"abc"}`))
	res, err := h.Fetch(context.Background(), FetchRequest{FeedURL: ts.URL, ETag: `"x"`, BridgeState: state})
	if err != nil || !res.NotModified {
		t.Fatalf("%+v err=%v", res, err)
	}
	if string(res.BridgeState.Marshal()) != string(state.Marshal()) {
		t.Fatalf("304 must keep the stored hash: %s", res.BridgeState.Marshal())
	}
}
