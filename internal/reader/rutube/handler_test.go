package rutube

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/model"
	"rssam/internal/ssrf"
)

//go:embed testdata/person_videos.json
var fixturePersonVideos []byte

func TestHandler_Fetch_ParseFixture(t *testing.T) {
	var gotChannel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/video/person/26119699/" {
			http.NotFound(w, r)
			return
		}
		gotChannel = strings.Trim(strings.TrimPrefix(r.URL.Path, "/video/person/"), "/")
		_, _ = w.Write(fixturePersonVideos)
	}))
	t.Cleanup(srv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}

	client, err := NewClient(srv.Client(), guard, Config{APIBaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(client, Config{})

	res, err := h.Fetch(context.Background(), "https://rutube.ru/video/person/26119699/", FetchState{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotChannel != "26119699" {
		t.Fatalf("channel=%q", gotChannel)
	}
	if res.State.ChannelID != "26119699" {
		t.Fatalf("state channel=%q", res.State.ChannelID)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.Title != "Test Rutube Video" {
		t.Fatalf("title=%q", e.Title)
	}
	if e.URL != "https://rutube.ru/video/a1b2c3d4e5f6789012345678/" {
		t.Fatalf("url=%q", e.URL)
	}
	author := ""
	if e.Author != nil {
		author = *e.Author
	}
	if author != "Test Author" {
		t.Fatalf("author=%q", author)
	}
	wantHash := model.DedupHashFromString("a1b2c3d4e5f6789012345678")
	if e.Hash != wantHash {
		t.Fatalf("hash=%q want %q", e.Hash, wantHash)
	}
	if e.PublishedAt == nil || !e.PublishedAt.Equal(time.Unix(1716400000, 0).UTC()) {
		t.Fatalf("published_at=%v", e.PublishedAt)
	}
	if !strings.Contains(e.Content, "pic.rutube.ru") {
		t.Fatalf("missing thumbnail: %q", e.Content)
	}
	if !strings.Contains(e.Content, "<br>") {
		t.Fatalf("expected nl2br: %q", e.Content)
	}
	if !strings.Contains(e.Content, `href="https://example.com/page"`) {
		t.Fatalf("expected linkify: %q", e.Content)
	}
	if res.FeedTitle != "Test Author - Rutube" {
		t.Fatalf("feed title=%q", res.FeedTitle)
	}
	if res.FeedURI != "https://rutube.ru/video/person/26119699/" {
		t.Fatalf("feed uri=%q", res.FeedURI)
	}
}

func TestDedupHash_FallsBackToURL(t *testing.T) {
	url := "https://rutube.ru/video/xyz/"
	got := DedupHash("", url)
	want := model.DedupHashFromURL(url)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
