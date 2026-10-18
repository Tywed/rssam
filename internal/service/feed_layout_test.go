package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

// A page the bridge does not recognise is deferred like a proxy outage:
// one UPDATE with last_error "layout: …" and the ordinary next check, no
// failure counter, no breaker, no poll-log row, no status event; the next
// good poll clears it.
func TestRefreshLoadedFeed_LayoutErrorDeferred(t *testing.T) {
	feed := storage.Feed{ID: 9, OwnerID: 1, FeedURL: "https://t.me/chan", FeedType: "telegram", IntervalMinutes: 10, ParsingErrorCount: 2, ETag: `"e1"`}
	h := &stubHandler{err: &bridge.LayoutError{Bridge: "telegram", Marker: "tgme_channel_info"}}
	r, fs, pl, pub := newStatusRefresher(h, feed)

	start := time.Now()
	_, err := r.RefreshLoadedFeed(context.Background(), feed)
	at, ok := reader.RetryAt(err)
	if !ok || bridge.DeferReason(err) != "layout" || at.Before(start.Add(9*time.Minute)) {
		t.Fatalf("err=%v retry=%v", err, at)
	}
	var persisted interface{ NextCheckPersisted() bool }
	if !errors.As(err, &persisted) || !persisted.NextCheckPersisted() {
		t.Fatal("the worker must not write next_check_at a second time")
	}
	if fs.updates != 1 || fs.failures != 0 || len(pl.rows) != 0 || len(pub.feeds) != 0 {
		t.Fatalf("updates=%d failures=%d poll-log=%d events=%d", fs.updates, fs.failures, len(pl.rows), len(pub.feeds))
	}
	if fs.meta.LastError != "layout: tgme_channel_info" || fs.meta.ETag != `"e1"` || fs.meta.NextCheckAt == nil || !fs.meta.NextCheckAt.Equal(at) || fs.meta.BridgeState != nil {
		t.Fatalf("meta=%+v", fs.meta)
	}
	if fs.feed.ParsingErrorCount != 2 {
		t.Fatalf("error count changed: %d", fs.feed.ParsingErrorCount)
	}

	h.err = nil
	h.res = reader.FetchResponse{Entries: []bridge.Entry{{Title: "a", URL: "https://t.me/chan/1", Hash: "h1"}}}
	fs.feed.LastError = fs.meta.LastError
	if _, err := r.RefreshLoadedFeed(context.Background(), fs.feed); err != nil {
		t.Fatal(err)
	}
	if fs.feed.LastError != "" {
		t.Fatalf("good poll must clear the layout error: %q", fs.feed.LastError)
	}
}
