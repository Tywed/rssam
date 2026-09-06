package reader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRSSFetcher_RejectsOversizedFeed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>big</title>`)
		filler := strings.Repeat("<item><title>x</title><description>"+strings.Repeat("A", 1000)+"</description></item>", 1)
		for written := 0; written < MaxFeedBodyBytes+1<<20; written += len(filler) {
			if _, err := fmt.Fprint(w, filler); err != nil {
				return
			}
		}
		fmt.Fprint(w, `</channel></rss>`)
	}))
	t.Cleanup(ts.Close)

	f := NewRSSFetcher(ts.Client(), "test", nil, "")
	_, err := f.Fetch(context.Background(), ts.URL, "", "", false, false)
	if err == nil {
		t.Fatal("expected error for oversized feed")
	}
	if !errors.Is(err, ErrFeedTooLarge) {
		t.Fatalf("expected ErrFeedTooLarge, got %v", err)
	}
}

func TestRSSFetcher_NormalFeedStillParses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>ok</title><item><title>a</title><link>http://e.com/a</link></item></channel></rss>`)
	}))
	t.Cleanup(ts.Close)
	f := NewRSSFetcher(ts.Client(), "test", nil, "")
	res, err := f.Fetch(context.Background(), ts.URL, "", "", false, false)
	if err != nil || len(res.Entries) != 1 {
		t.Fatalf("err=%v entries=%d", err, len(res.Entries))
	}
	title, err := f.DiscoverTitle(context.Background(), ts.URL, false, false)
	if err != nil || title != "ok" {
		t.Fatalf("title=%q err=%v", title, err)
	}
}
