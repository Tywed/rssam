package vk

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rssam/internal/model"
	"rssam/internal/ssrf"
)

//go:embed testdata/newsfeed_search.json
var fixtureSearchResponse []byte

func TestHandler_Fetch_ParseFixture(t *testing.T) {
	var gotQ, gotStart, gotEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/method/newsfeed.search" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		gotQ = r.URL.Query().Get("q")
		gotStart = r.URL.Query().Get("start_time")
		gotEnd = r.URL.Query().Get("end_time")
		_, _ = w.Write(fixtureSearchResponse)
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}

	apiURL := srv.URL + "/method"
	client := &Client{
		http:   srv.Client(),
		guard:  guard,
		cfg:    Config{AccessToken: "test-token", APIVersion: "5.199", DefaultCount: 100},
		apiURL: apiURL + newsfeedSearchPath,
	}
	h := NewHandler(client, Config{AccessToken: "test-token", DefaultLookback: 24 * time.Hour, Overlap: 2 * time.Minute})

	res, err := h.Fetch(context.Background(), "https://vk.com/feed?section=search&q=Golang", FetchState{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotQ != "golang" {
		t.Fatalf("q=%q", gotQ)
	}
	if gotStart == "" || gotEnd == "" {
		t.Fatalf("missing time params start=%q end=%q", gotStart, gotEnd)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.Title != "Hello VK search" {
		t.Fatalf("title=%q", e.Title)
	}
	if e.URL != "https://vk.com/wall-12345_42" {
		t.Fatalf("url=%q", e.URL)
	}
	author := ""
	if e.Author != nil {
		author = *e.Author
	}
	if author != "Test Group" {
		t.Fatalf("author=%q", author)
	}
	wantHash := model.DedupHashFromString("-12345_42")
	if e.Hash != wantHash {
		t.Fatalf("hash=%q want %q", e.Hash, wantHash)
	}
	if !strings.Contains(e.Content, "large.jpg") {
		t.Fatalf("content missing attachment: %s", e.Content)
	}
	if res.State.LastEndTime <= 0 {
		t.Fatalf("cursor not set: %d", res.State.LastEndTime)
	}
}

func TestHandler_Fetch_SkipsOldPosts(t *testing.T) {
	body, _ := json.Marshal(searchResponse{
		Response: &searchResponseBody{
			Items: []wallPost{
				{ID: 1, OwnerID: 1, Date: 100, Text: "old"},
				{ID: 2, OwnerID: 1, Date: 200, Text: "new"},
			},
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	guard, _ := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	client := &Client{
		http:   srv.Client(),
		guard:  guard,
		cfg:    Config{AccessToken: "tok"},
		apiURL: srv.URL + newsfeedSearchPath,
	}
	h := NewHandler(client, Config{AccessToken: "tok"})

	res, err := h.Fetch(context.Background(), "vk-search://test", FetchState{LastEndTime: 150})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Title != "new" {
		t.Fatalf("entries=%+v", res.Entries)
	}
}

func TestHandler_Fetch_RateLimit(t *testing.T) {
	errBody, _ := json.Marshal(searchResponse{Error: &apiError{ErrorCode: 6, ErrorMsg: "Too many requests"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(errBody)
	}))
	t.Cleanup(srv.Close)

	guard, _ := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	client := &Client{
		http:   srv.Client(),
		guard:  guard,
		cfg:    Config{AccessToken: "tok", RateLimitCooldown: 5 * time.Second},
		apiURL: srv.URL + newsfeedSearchPath,
	}
	h := NewHandler(client, Config{AccessToken: "tok", RateLimitCooldown: 5 * time.Second})

	_, err := h.Fetch(context.Background(), "vk-search://q", FetchState{})
	if err == nil {
		t.Fatal("expected rate limit error")
	}
}

func TestClient_Search_BuildsURL(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.String()
		_, _ = w.Write(fixtureSearchResponse)
	}))
	t.Cleanup(srv.Close)

	guard, _ := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	c := &Client{
		http:   srv.Client(),
		guard:  guard,
		cfg:    Config{AccessToken: "tok", APIVersion: "5.199"}.withDefaults(),
		apiURL: srv.URL,
	}

	_, apiErr, err := c.Search(context.Background(), SearchParams{
		Query: "test", Count: 50, StartTime: 1, EndTime: 2,
	})
	if err != nil || apiErr != nil {
		t.Fatalf("search: err=%v apiErr=%v", err, apiErr)
	}
	u, _ := url.Parse(raw)
	if u.Query().Get("extended") != "1" || u.Query().Get("v") != "5.199" {
		t.Fatalf("query=%v", u.Query())
	}
}
