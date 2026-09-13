package reader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const discoverTestFeed = `<?xml version="1.0"?><rss version="2.0"><channel><title>Site feed</title><item><title>a</title><link>http://e.com/a</link></item></channel></rss>`

func newDiscoverServer(t *testing.T, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for p, h := range routes {
		mux.HandleFunc(p, h)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func serveFeed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/rss+xml")
	fmt.Fprint(w, discoverTestFeed)
}

func TestDiscover_HTMLPageWithAlternateLink(t *testing.T) {
	var hits []string
	ts := newDiscoverServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			hits = append(hits, r.URL.Path)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Site</title>
<link rel="stylesheet" href="/s.css">
<link rel="alternate" type="application/rss+xml" title="RSS" href="/blog/feed.xml">
<link rel="alternate" type="application/atom+xml" href="https://other.example/atom">
</head><body><link rel="alternate" type="application/rss+xml" href="/ignored"></body></html>`)
		},
		"/blog/feed.xml": func(w http.ResponseWriter, r *http.Request) {
			hits = append(hits, r.URL.Path)
			serveFeed(w, r)
		},
	})
	f := NewRSSFetcher(ts.Client(), "test", nil, "")
	d, err := f.Discover(context.Background(), ts.URL+"/", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.FeedURL != ts.URL+"/blog/feed.xml" || d.Title != "Site feed" {
		t.Fatalf("discovery = %+v", d)
	}
	if strings.Join(hits, ",") != "/,/blog/feed.xml" {
		t.Fatalf("requests = %v (relative link must resolve against the page, body links ignored)", hits)
	}
}

func TestDiscover_WellKnownFallbackAndNoFeed(t *testing.T) {
	var hits []string
	ts := newDiscoverServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			hits = append(hits, r.URL.Path)
			if r.URL.Path == "/rss.xml" {
				serveFeed(w, r)
				return
			}
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>No links</title></head><body>hi</body></html>`)
		},
	})
	f := NewRSSFetcher(ts.Client(), "test", nil, "")
	d, err := f.Discover(context.Background(), ts.URL+"/", false, false)
	if err != nil || d.FeedURL != ts.URL+"/rss.xml" {
		t.Fatalf("d=%+v err=%v hits=%v", d, err, hits)
	}
	if strings.Join(hits, ",") != "/,/feed,/rss,/feed.xml,/rss.xml" {
		t.Fatalf("well-known order: %v", hits)
	}

	none := newDiscoverServer(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, `<!doctype html><html><body>plain</body></html>`)
		},
	})
	if _, err := f.Discover(context.Background(), none.URL+"/", false, false); !errors.Is(err, ErrNoFeedFound) {
		t.Fatalf("err=%v, want ErrNoFeedFound", err)
	}
}

func TestDiscover_FeedServedAsTextHTML(t *testing.T) {
	ts := newDiscoverServer(t, map[string]http.HandlerFunc{
		"/f": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "\n  "+discoverTestFeed)
		},
	})
	f := NewRSSFetcher(ts.Client(), "test", nil, "")
	d, err := f.Discover(context.Background(), ts.URL+"/f", false, false)
	if err != nil || d.FeedURL != ts.URL+"/f" || d.Title != "Site feed" {
		t.Fatalf("d=%+v err=%v", d, err)
	}
}

func TestFetchAndDiscover_PermanentRedirectReported(t *testing.T) {
	ts := newDiscoverServer(t, map[string]http.HandlerFunc{
		"/old": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/mid", http.StatusMovedPermanently) },
		"/mid": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/new", http.StatusPermanentRedirect)
		},
		"/temp": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/new", http.StatusFound) },
		"/mix": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/temp", http.StatusMovedPermanently)
		},
		"/new": serveFeed,
	})
	f := NewRSSFetcher(ts.Client(), "test", nil, "")

	res, err := f.Fetch(context.Background(), ts.URL+"/old", "", "", false, false)
	if err != nil || res.NewURL != ts.URL+"/new" || len(res.Entries) != 1 {
		t.Fatalf("301→308 chain: res=%+v err=%v", res, err)
	}
	res, err = f.Fetch(context.Background(), ts.URL+"/temp", "", "", false, false)
	if err != nil || res.NewURL != "" {
		t.Fatalf("302 must not report a new URL: %q err=%v", res.NewURL, err)
	}
	res, err = f.Fetch(context.Background(), ts.URL+"/mix", "", "", false, false)
	if err != nil || res.NewURL != "" {
		t.Fatalf("301 followed by 302 is not permanent: %q err=%v", res.NewURL, err)
	}
	res, err = f.Fetch(context.Background(), ts.URL+"/new", "", "", false, false)
	if err != nil || res.NewURL != "" {
		t.Fatalf("no redirect: %q err=%v", res.NewURL, err)
	}

	d, err := f.Discover(context.Background(), ts.URL+"/old", false, false)
	if err != nil || d.FeedURL != ts.URL+"/new" {
		t.Fatalf("discover follows permanent redirects: d=%+v err=%v", d, err)
	}
	d, err = f.Discover(context.Background(), ts.URL+"/temp", false, false)
	if err != nil || d.FeedURL != ts.URL+"/temp" {
		t.Fatalf("discover keeps the typed URL on 302: d=%+v err=%v", d, err)
	}
}

func TestFeedLinksFromHTML_Dedup(t *testing.T) {
	base, _ := url.Parse("https://site.example/blog/post")
	links := feedLinksFromHTML(strings.NewReader(`<head>
<link rel="ALTERNATE" type="application/rss+xml" href="feed">
<link rel="alternate" type="application/rss+xml" href="/blog/feed#x">
<link rel="alternate" type="application/feed+json" href="//cdn.example/feed.json">
<link rel="alternate" type="text/html" href="/other">
<link rel="alternate" type="application/atom+xml" href="ftp://x/y">
</head>`), base)
	want := []string{"https://site.example/blog/feed", "https://cdn.example/feed.json"}
	if strings.Join(links, " ") != strings.Join(want, " ") {
		t.Fatalf("links = %v, want %v", links, want)
	}
}
