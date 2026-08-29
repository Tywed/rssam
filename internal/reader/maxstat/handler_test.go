package maxstat

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rssam/internal/model"
	"rssam/internal/ssrf"
)

//go:embed testdata/posts_search.json
var fixturePostsResponse []byte

func TestHandler_Fetch_ParseFixture(t *testing.T) {
	var gotSearch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/posts" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-API-Token") != "test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		gotSearch = r.URL.Query().Get("search")
		_, _ = w.Write(fixturePostsResponse)
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}

	client, err := NewClient(srv.Client(), guard, Config{
		AccessToken: "test-token",
		APIBaseURL:  srv.URL + "/api/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(client, Config{AccessToken: "test-token"})

	res, err := h.Fetch(context.Background(), "https://maxstat.ru/posts?search=krasnodar", FetchState{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotSearch != "krasnodar" {
		t.Fatalf("search=%q", gotSearch)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.Title != "Тестовая публикация MaxStat" {
		t.Fatalf("title=%q", e.Title)
	}
	if e.URL != "https://max.ru/chp_krasnodar/AaAGlSweKR0" {
		t.Fatalf("url=%q", e.URL)
	}
	wantHash := model.DedupHashFromString("117100828071700765")
	if e.Hash != wantHash {
		t.Fatalf("hash mismatch")
	}
	if res.State.LastEndTime != time.Date(2026, 8, 15, 18, 0, 34, 0, time.UTC).Unix() {
		t.Fatalf("last_end_time=%d", res.State.LastEndTime)
	}
}

func TestHandler_Fetch_SkipsSeenPosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixturePostsResponse)
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(srv.Client(), guard, Config{
		AccessToken: "test-token",
		APIBaseURL:  srv.URL + "/api/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(client, Config{AccessToken: "test-token"})

	lastEnd := time.Date(2026, 8, 15, 18, 0, 34, 0, time.UTC).Unix()
	res, err := h.Fetch(context.Background(), "maxstat-search://krasnodar", FetchState{LastEndTime: lastEnd})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("entries=%d want 0", len(res.Entries))
	}
}
