package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

type panickingHandler struct{ stubHandler }

func (h *panickingHandler) Fetch(context.Context, reader.FetchRequest) (reader.FetchResponse, error) {
	panic("bridge bug")
}

// A panic in a bridge is recorded like any other failure of that feed:
// error counter, poll-log row, status event — and the caller gets an error,
// not a crashed worker.
func TestRefreshLoadedFeed_PanicIsAFailure(t *testing.T) {
	feed := storage.Feed{ID: 77, OwnerID: 1, FeedURL: "https://example.com/f", FeedType: "rss", IntervalMinutes: 5}
	r, fs, pl, pub := newStatusRefresher(&panickingHandler{}, feed)
	_, err := r.RefreshLoadedFeed(context.Background(), fs.feed)
	if !errors.Is(err, ErrFetchFeed) || !strings.Contains(err.Error(), "panic: bridge bug") {
		t.Fatalf("err=%v", err)
	}
	if fs.failures != 1 || len(pl.rows) != 1 || fs.feed.ParsingErrorCount != 1 || len(pub.feeds) != 1 {
		t.Fatalf("failures=%d pollLog=%d count=%d events=%d", fs.failures, len(pl.rows), fs.feed.ParsingErrorCount, len(pub.feeds))
	}
}

type ctxErrHandler struct{ stubHandler }

func (h *ctxErrHandler) Fetch(ctx context.Context, _ reader.FetchRequest) (reader.FetchResponse, error) {
	return reader.FetchResponse{}, ctx.Err()
}

// A fetch cut short by the process stopping says nothing about the source:
// no error counter, no poll-log row, no status event.
func TestRefreshLoadedFeed_CancelledContextNotRecorded(t *testing.T) {
	feed := storage.Feed{ID: 78, OwnerID: 1, FeedURL: "https://example.com/f", FeedType: "rss", IntervalMinutes: 5}
	r, fs, pl, pub := newStatusRefresher(&ctxErrHandler{}, feed)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.RefreshLoadedFeed(ctx, fs.feed)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if fs.failures != 0 || len(pl.rows) != 0 || fs.feed.ParsingErrorCount != 0 || len(pub.feeds) != 0 {
		t.Fatalf("failures=%d pollLog=%d count=%d events=%d", fs.failures, len(pl.rows), fs.feed.ParsingErrorCount, len(pub.feeds))
	}
}
