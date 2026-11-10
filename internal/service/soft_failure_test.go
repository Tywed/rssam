package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

func TestIsSoftFailure(t *testing.T) {
	soft := []error{
		errors.New("telegram: channel is private or does not exist"),
		errors.New(`max: unexpected status 400: {"detail":"Неожиданная ошибка: Not connected to the server"}`),
		errors.New(`max: unexpected status 400: {"detail":"Неожиданная ошибка: Connection error: no close frame received or sent"}`),
		errors.New(`max: unexpected status 400: {"detail":"Неожиданная ошибка: sent 1011 (internal error) keepalive ping timeout"}`),
		fmt.Errorf("http request: %w", context.DeadlineExceeded),
		errors.New(`http request: Get "https://x": net/http: TLS handshake timeout`),
		errors.New("http request: read tcp 10.0.0.1:1->1.2.3.4:443: read: connection reset by peer"),
		errors.New("ssrf: dns lookup failed: lookup dzen.ru on 127.0.0.53:53: server misbehaving"),
		&net.DNSError{Err: "timeout", IsTimeout: true},
	}
	for _, err := range soft {
		if !IsSoftFailure(err) {
			t.Errorf("%v must be soft", err)
		}
	}
	hard := []error{
		errors.New("unexpected status code: 404"),
		errors.New(`max: unexpected status 404: {"detail":"Канал 'x' не найден"}`),
		errors.New("parse feed: failed to detect feed type"),
		errors.New(`tls: failed to verify certificate: x509: certificate is valid for a.example, not b.example`),
		errors.New("maxstat api error insufficient_credits: Not enough credits"),
	}
	for _, err := range hard {
		if IsSoftFailure(err) {
			t.Errorf("%v must not be soft", err)
		}
	}
}

// Three soft failures in a row leave a healthy feed healthy: no counter, no
// backoff, no poll-log row, no status event; the fourth is a failure. A
// success in between starts over.
func TestRefreshLoadedFeed_SoftFailuresWithinLimitAreNotFailures(t *testing.T) {
	feed := storage.Feed{ID: 41, OwnerID: 1, FeedURL: "https://t.me/s/x", FeedType: "rss", IntervalMinutes: 5, LastError: "", BridgeState: []byte(`{"c":1}`)}
	h := &stubHandler{err: errors.New("telegram: channel is private or does not exist")}
	r, fs, pl, pub := newStatusRefresher(h, feed)

	for i := 1; i <= softFailureLimit; i++ {
		_, err := r.RefreshLoadedFeed(context.Background(), fs.feed)
		var soft ErrSoftFailure
		if !errors.As(err, &soft) {
			t.Fatalf("poll %d: err=%v, want ErrSoftFailure", i, err)
		}
		if at, ok := reader.RetryAt(err); !ok || time.Until(at) < 4*time.Minute || time.Until(at) > 6*time.Minute {
			t.Fatalf("poll %d: retry at %s, want the feed's own interval", i, at)
		}
		if fs.failures != 0 || len(pl.rows) != 0 || len(pub.feeds) != 0 {
			t.Fatalf("poll %d: failures=%d pollLog=%d events=%d, want none", i, fs.failures, len(pl.rows), len(pub.feeds))
		}
		if fs.updates != i || fs.meta.LastError != "" || string(fs.meta.BridgeState) != `{"c":1}` || fs.meta.NextCheckAt == nil {
			t.Fatalf("poll %d: meta=%+v updates=%d", i, fs.meta, fs.updates)
		}
	}

	if _, err := r.RefreshLoadedFeed(context.Background(), fs.feed); !errors.Is(err, ErrFetchFeed) {
		t.Fatalf("poll %d: err=%v, want ErrFetchFeed", softFailureLimit+1, err)
	}
	if fs.failures != 1 || len(pl.rows) != 1 || fs.feed.ParsingErrorCount != 1 {
		t.Fatalf("fourth soft failure must count: failures=%d pollLog=%d count=%d", fs.failures, len(pl.rows), fs.feed.ParsingErrorCount)
	}

	// Already failing: no soft treatment, the counter walks on.
	if _, err := r.RefreshLoadedFeed(context.Background(), fs.feed); !errors.Is(err, ErrFetchFeed) {
		t.Fatalf("failing feed: err=%v, want ErrFetchFeed", err)
	}
	if fs.failures != 2 {
		t.Fatalf("failures=%d, want 2", fs.failures)
	}

	// A success resets the streak.
	h.err = nil
	h.res = reader.FetchResponse{NotModified: true}
	fs.feed.ParsingErrorCount, fs.feed.LastError = 0, ""
	if _, err := r.RefreshLoadedFeed(context.Background(), fs.feed); err != nil {
		t.Fatal(err)
	}
	h.err = errors.New("telegram: channel is private or does not exist")
	failuresBefore := fs.failures
	if _, err := r.RefreshLoadedFeed(context.Background(), fs.feed); !errors.As(err, new(ErrSoftFailure)) {
		t.Fatalf("after success: err=%v, want ErrSoftFailure", err)
	}
	if fs.failures != failuresBefore {
		t.Fatalf("failures=%d, want %d", fs.failures, failuresBefore)
	}
}

// A manual refresh reports the error at once.
func TestRefreshFeedManual_SoftFailureIsReported(t *testing.T) {
	feed := storage.Feed{ID: 42, OwnerID: 1, FeedURL: "https://example.com/f.xml", IntervalMinutes: 5}
	r, fs, _, _ := newStatusRefresher(&stubHandler{err: fmt.Errorf("http request: %w", context.DeadlineExceeded)}, feed)
	if _, err := r.RefreshFeedManual(context.Background(), feed.ID); !errors.Is(err, ErrFetchFeed) {
		t.Fatalf("err=%v, want ErrFetchFeed", err)
	}
	if fs.failures != 1 {
		t.Fatalf("failures=%d, want 1", fs.failures)
	}
}
