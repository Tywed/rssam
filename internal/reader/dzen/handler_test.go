package dzen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/model"
	"rssam/internal/ssrf"
)

func TestHandler_Fetch_ParseFixture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/news/search") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("text") != "python" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(fixtureNeoHTML))
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(srv.Client(), guard, Config{SearchURL: srv.URL + "/news/search"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(client, Config{SearchURL: srv.URL + "/news/search"})

	res, err := h.Fetch(context.Background(), "dzen-news://python", FetchState{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.State.Query != "python" {
		t.Fatalf("query=%q", res.State.Query)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.Title != "Test headline" {
		t.Fatalf("title=%q", e.Title)
	}
	wantHash := model.DedupHashFromString("doc-1")
	if e.Hash != wantHash {
		t.Fatalf("hash=%q want %q", e.Hash, wantHash)
	}
	if e.PublishedAt == nil || !e.PublishedAt.Equal(time.Unix(1786373691, 0).UTC()) {
		t.Fatalf("published_at=%v", e.PublishedAt)
	}
	if !strings.Contains(e.Content, `href="https://example.com/news/1"`) {
		t.Fatalf("content missing link: %q", e.Content)
	}
	if res.FeedTitle != "Dzen: python" {
		t.Fatalf("feed title=%q", res.FeedTitle)
	}
}
