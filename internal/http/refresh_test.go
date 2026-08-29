package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type fakeEntryStore struct {
	noopEntryStore
	inserted int
}

func (f *fakeEntryStore) CreateEntries(_ context.Context, _ int64, entries []storage.CreateEntryParams) (int, []storage.Entry, error) {
	f.inserted += len(entries)
	return len(entries), make([]storage.Entry, len(entries)), nil
}

type refreshFeedStore struct {
	noopFeedStore
	feed storage.Feed
	meta storage.UpdateFeedRefreshMetaParams
}

func (f *refreshFeedStore) GetFeed(_ context.Context, _, id int64) (storage.Feed, error) {
	if id != f.feed.ID {
		return storage.Feed{}, storage.ErrNotFound
	}
	return f.feed, nil
}
func (f *refreshFeedStore) GetFeedByID(ctx context.Context, id int64) (storage.Feed, error) {
	return f.GetFeed(ctx, 0, id)
}
func (f *refreshFeedStore) UpdateFeedRefreshMeta(_ context.Context, params storage.UpdateFeedRefreshMetaParams) error {
	f.meta = params
	return nil
}

func TestRefreshFeed_Smoke(t *testing.T) {
	const rss = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0"><channel><title>Example</title>
<item><title>One</title><link>https://example.com/1</link></item>
<item><title>Two</title><link>https://example.com/2</link></item>
</channel></rss>`

	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Header().Set("ETag", `"etag1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		_, _ = w.Write([]byte(rss))
	}))
	t.Cleanup(feedSrv.Close)

	fs := &refreshFeedStore{
		feed: storage.Feed{ID: 7, FeedURL: feedSrv.URL, IntervalMinutes: 60},
	}
	es := &fakeEntryStore{}

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Dependencies{
		AuthToken:            "secret",
		FeedStore:            fs,
		EntryStore:           es,
		CategoryStore:        &fakeCategoryStore{},
		HTTPClient:           guard.HTTPClient(2 * time.Second),
		FetchUserAgent:       "rssam-test",
		FetchTimeoutSec:      2,
		FetchAllowPrivateNet: true,
		SSRFGuard:            guard,
	})

	api := http.NewServeMux()
	api.HandleFunc("POST /v1/feeds/{feedID}/refresh", s.handleRefreshFeed)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/7/refresh", bytes.NewBuffer(nil))
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Data struct {
			Inserted int `json:"inserted"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || resp.Data.Inserted != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if es.inserted != 2 {
		t.Fatalf("inserted=%d", es.inserted)
	}
	if fs.meta.ID != 7 || fs.meta.LastError != "" {
		t.Fatalf("meta not updated: %+v", fs.meta)
	}
	if fs.meta.LastCheckedAt.IsZero() {
		t.Fatalf("expected last_checked_at to be set")
	}
}
