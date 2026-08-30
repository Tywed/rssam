package reader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/model"
	"rssam/internal/storage"
)

const sampleRSS = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0">
  <channel>
    <title>Example</title>
    <link>https://example.com/</link>
    <description>Example feed</description>
    <item>
      <title>Hello</title>
      <link>https://example.com/a#frag</link>
      <description>World</description>
      <pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate>
      <author>alice@example.com (Alice)</author>
    </item>
  </channel>
</rss>`

func TestRSSFetcher_FetchAndNormalize(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	t.Cleanup(ts.Close)

	client := &http.Client{Timeout: 2 * time.Second}
	f := NewRSSFetcher(client, "rssam-test", nil, "")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := f.Fetch(ctx, ts.URL, "", "", false, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.NotModified {
		t.Fatalf("expected NotModified=false")
	}
	if res.ETag == "" || res.LastModified == "" {
		t.Fatalf("expected caching headers to be set")
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.Status != storage.EntryStatusUnread {
		t.Fatalf("status=%q", e.Status)
	}
	if e.URL != model.NormalizeURL("https://example.com/a#frag") {
		t.Fatalf("url=%q", e.URL)
	}
	if e.Hash != model.DedupHashFromURL(e.URL) {
		t.Fatalf("hash=%q", e.Hash)
	}
	if e.PublishedAt == nil {
		t.Fatalf("expected published_at")
	}
}

func TestRSSFetcher_NotModified(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != `"cached"` {
			t.Fatalf("If-None-Match=%q", r.Header.Get("If-None-Match"))
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(ts.Close)

	f := NewRSSFetcher(&http.Client{Timeout: 2 * time.Second}, "rssam-test", nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := f.Fetch(ctx, ts.URL, `"cached"`, "", false, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !res.NotModified {
		t.Fatalf("expected NotModified=true")
	}
	if len(res.Entries) != 0 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
}

func TestRSSFetcher_SanitizesContent(t *testing.T) {
	const rss = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0"><channel><title>X</title>
<item><title>t</title><link>https://example.com/x</link>
<description><![CDATA[<p>safe</p><script>alert(1)</script>]]></description>
</item></channel></rss>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rss))
	}))
	t.Cleanup(ts.Close)

	f := NewRSSFetcher(&http.Client{Timeout: 2 * time.Second}, "rssam-test", nil, "")
	res, err := f.Fetch(context.Background(), ts.URL, "", "", false, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	if strings.Contains(res.Entries[0].Content, "script") {
		t.Fatalf("content=%q", res.Entries[0].Content)
	}
	if !strings.Contains(res.Entries[0].Content, "safe") {
		t.Fatalf("content=%q", res.Entries[0].Content)
	}
}

const sampleJSONFeed = `{
  "version": "https://jsonfeed.org/version/1.1",
  "title": "JSON Example",
  "items": [{
    "id": "1",
    "url": "https://example.com/json/1",
    "title": "JSON item",
    "content_html": "<p>json</p>",
    "date_published": "2006-01-02T15:04:05Z"
  }]
}`

func TestRSSFetcher_JSONFeed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/feed+json")
		_, _ = w.Write([]byte(sampleJSONFeed))
	}))
	t.Cleanup(ts.Close)

	f := NewRSSFetcher(&http.Client{Timeout: 2 * time.Second}, "rssam-test", nil, "")
	res, err := f.Fetch(context.Background(), ts.URL, "", "", false, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	if res.Entries[0].Title != "JSON item" {
		t.Fatalf("title=%q", res.Entries[0].Title)
	}
}

// Sites like fedpress.ru answer 302+Set-Cookie to the same URL; without a jar
// the client loops until CheckRedirect stops.
func TestRSSFetcher_FollowsSetCookieRedirect(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if c, err := r.Cookie("session"); err != nil || c.Value != "ok" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "ok", Path: "/"})
			http.Redirect(w, r, r.URL.Path, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	t.Cleanup(ts.Close)

	f := NewRSSFetcher(&http.Client{Timeout: 2 * time.Second}, "rssam-test", nil, "")
	res, err := f.Fetch(context.Background(), ts.URL+"/feed", "", "", false, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if hits < 2 {
		t.Fatalf("expected redirect then body, hits=%d", hits)
	}
	if len(res.Entries) != 1 || res.Entries[0].Title != "Hello" {
		t.Fatalf("entries=%+v", res.Entries)
	}
}
