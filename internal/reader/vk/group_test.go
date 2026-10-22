package vk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/ssrf"
)

func TestParseWallFromFeedURL(t *testing.T) {
	cases := []struct {
		in   string
		want Wall
		ok   bool
	}{
		{"https://vk.com/tass", Wall{Domain: "tass"}, true},
		{"https://m.vk.com/Tass/", Wall{Domain: "tass"}, true},
		{"https://vk.ru/public36441046", Wall{OwnerID: -36441046}, true},
		{"https://vk.com/club123", Wall{OwnerID: -123}, true},
		{"https://vk.com/event55", Wall{OwnerID: -55}, true},
		{"https://vk.com/id1", Wall{OwnerID: 1}, true},
		{"vk-group://durov", Wall{Domain: "durov"}, true},
		{"https://vk.com/feed?section=search&q=x", Wall{}, false},
		{"https://vk.com/wall-1_2", Wall{}, false},
		{"https://vk.com/tass?w=wall-1_2", Wall{}, false},
		{"https://vk.com/video/@tass", Wall{}, false},
		{"https://vk.com/im", Wall{}, false},
		{"https://vk.com/", Wall{}, false},
		{"https://example.com/tass", Wall{}, false},
		{"https://vk.com/ab", Wall{}, false},
	}
	for _, c := range cases {
		got, ok := ParseWallFromFeedURL(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func newWallServer(t *testing.T, status int, body []byte, seen *[]string) (*GroupHandler, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("authorization header %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	guard, _ := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	client := &Client{
		http:   srv.Client(),
		guard:  guard,
		cfg:    Config{AccessToken: "tok", RateLimitCooldown: 7 * time.Second}.withDefaults(),
		apiURL: srv.URL,
	}
	return NewGroupHandler(client), srv
}

func TestGroupHandler_Fetch(t *testing.T) {
	fixture, err := os.ReadFile("testdata/wall_get.json")
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	h, _ := newWallServer(t, http.StatusOK, fixture, &seen)

	res, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://vk.com/tass"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("requests=%d", len(seen))
	}
	for _, want := range []string{"/wall.get?", "domain=tass", "count=20", "filter=all", "extended=1", "v=5.199"} {
		if !strings.Contains(seen[0], want) {
			t.Errorf("request %s lacks %s", seen[0], want)
		}
	}
	if len(res.Entries) != 4 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	if res.State != nil {
		t.Fatalf("group bridge keeps no state, got %s", res.State)
	}
	byURL := map[string]bridge.Entry{}
	for _, e := range res.Entries {
		byURL[e.URL] = e
	}
	fresh, ok := byURL["https://vk.com/wall-36441046_90120"]
	if !ok {
		t.Fatalf("missing fresh post: %v", byURL)
	}
	if fresh.Title != "Свежая новость" || fresh.Author == nil || *fresh.Author != "ТАСС" {
		t.Fatalf("fresh=%+v", fresh)
	}
	if !strings.Contains(fresh.Content, `src="https://img.example/x.jpg"`) || !strings.Contains(fresh.Content, `href="https://example.com/x"`) {
		t.Fatalf("content=%s", fresh.Content)
	}
	if fresh.PublishedAt == nil || fresh.PublishedAt.Unix() != 1760870000 {
		t.Fatalf("published=%v", fresh.PublishedAt)
	}
	repost := byURL["https://vk.com/wall-36441046_90115"]
	if repost.Title != "Репост из другого сообщества" || !strings.Contains(repost.Content, "<strong>Источник</strong>") {
		t.Fatalf("repost=%+v", repost)
	}
	if h, _ := byURL["https://vk.com/wall-36441046_90012"]; h.Hash == "" || h.Hash == fresh.Hash {
		t.Fatalf("hash: pinned=%s fresh=%s", h.Hash, fresh.Hash)
	}

	title, err := h.DiscoverTitle(context.Background(), "https://vk.com/public36441046")
	if err != nil || title != "ТАСС" {
		t.Fatalf("title=%q err=%v", title, err)
	}
	title, err = h.DiscoverTitle(context.Background(), "https://vk.com/id12345")
	if err != nil || title != "Иван Петров" {
		t.Fatalf("profile title=%q err=%v", title, err)
	}
}

func TestGroupHandler_OwnerIDRequest(t *testing.T) {
	var seen []string
	h, _ := newWallServer(t, http.StatusOK, []byte(`{"response":{"count":0,"items":[],"profiles":[],"groups":[]}}`), &seen)
	res, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://vk.com/club123"})
	if err != nil || len(res.Entries) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !strings.Contains(seen[0], "owner_id=-123") || strings.Contains(seen[0], "domain=") {
		t.Fatalf("request %s", seen[0])
	}
}

func TestGroupHandler_Errors(t *testing.T) {
	cases := []struct {
		code int
		want error
	}{
		{15, ErrWallClosed},
		{18, ErrWallClosed},
		{100, ErrWallNotFound},
		{113, ErrWallNotFound},
		{5, ErrTokenRejected},
		{27, ErrTokenRejected},
	}
	for _, c := range cases {
		body, _ := json.Marshal(searchResponse{Error: &apiError{ErrorCode: c.code, ErrorMsg: "x"}})
		h, _ := newWallServer(t, http.StatusOK, body, nil)
		_, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://vk.com/tass"})
		if !errors.Is(err, c.want) {
			t.Errorf("code %d: err=%v want %v", c.code, err, c.want)
		}
		if _, retry := bridge.RetryAt(err); retry {
			t.Errorf("code %d must not defer", c.code)
		}
	}

	for _, code := range []int{6, 9, 29} {
		body, _ := json.Marshal(searchResponse{Error: &apiError{ErrorCode: code, ErrorMsg: "slow down"}})
		h, _ := newWallServer(t, http.StatusOK, body, nil)
		before := time.Now()
		_, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://vk.com/tass"})
		at, ok := bridge.RetryAt(err)
		if !ok {
			t.Fatalf("code %d: expected RetryAtError, got %v", code, err)
		}
		min := 7 * time.Second
		if code == 29 {
			min = time.Hour
		}
		if d := at.Sub(before); d < min-time.Second || d > min+5*time.Second {
			t.Fatalf("code %d: retry in %v", code, d)
		}
	}

	h, _ := newWallServer(t, http.StatusOK, []byte(`<html>maintenance</html>`), nil)
	_, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://vk.com/tass"})
	var le *bridge.LayoutError
	if !errors.As(err, &le) {
		t.Fatalf("non-JSON body must be a layout error, got %v", err)
	}

	if _, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://vk.com/feed"}); !errors.Is(err, errInvalidWallURL) {
		t.Fatalf("bad url err=%v", err)
	}
}

// Every call of one token waits for the previous slot, whichever bridge
// made it.
func TestClient_PacesRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"response":{"count":0,"items":[]}}`))
	}))
	t.Cleanup(srv.Close)
	guard, _ := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	c := &Client{http: srv.Client(), guard: guard, cfg: Config{AccessToken: "tok"}.withDefaults(), apiURL: srv.URL}
	start := time.Now()
	for range 3 {
		if _, _, err := c.WallGet(context.Background(), Wall{Domain: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := c.Search(context.Background(), SearchParams{Query: "q", StartTime: 1, EndTime: 2}); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 3*minRequestGap-20*time.Millisecond {
		t.Fatalf("4 calls took %v, want ≥ %v", el, 3*minRequestGap)
	}
	if calls.Load() != 4 {
		t.Fatalf("calls=%d", calls.Load())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := c.WallGet(ctx, Wall{Domain: "a"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled wait err=%v", err)
	}
}
