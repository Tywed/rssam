package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rssam/internal/scraper"
	"rssam/internal/service"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type fetchEntryStore struct {
	noopEntryStore
	entry storage.Entry
}

func (f *fetchEntryStore) GetEntry(_ context.Context, _, id int64) (storage.Entry, error) {
	if id != f.entry.ID {
		return storage.Entry{}, storage.ErrNotFound
	}
	return f.entry, nil
}
func (f *fetchEntryStore) GetEntryByID(_ context.Context, id int64) (storage.Entry, error) {
	return f.GetEntry(context.Background(), 0, id)
}
func (f *fetchEntryStore) UpdateEntryContent(_ context.Context, _ int64, params storage.UpdateEntryContentParams) (storage.Entry, error) {
	e := f.entry
	e.Content = params.Content
	e.OriginalContent = params.OriginalContent
	e.ContentFetched = params.ContentFetched
	f.entry = e
	return e, nil
}

type fetchFeedStore struct {
	noopFeedStore
	feed storage.Feed
}

func (f *fetchFeedStore) GetFeed(_ context.Context, _, id int64) (storage.Feed, error) {
	if id != f.feed.ID {
		return storage.Feed{}, storage.ErrNotFound
	}
	return f.feed, nil
}
func (f *fetchFeedStore) GetFeedByID(_ context.Context, id int64) (storage.Feed, error) {
	return f.GetFeed(context.Background(), 0, id)
}
func (f *fetchFeedStore) ListAllFeeds(_ context.Context, _ int) ([]storage.Feed, error) {
	return nil, nil
}
func (f *fetchFeedStore) ListFeeds(_ context.Context, _ int64, _, _ int) ([]storage.Feed, int, error) {
	return nil, 0, nil
}
func (f *fetchFeedStore) UpdateFeed(_ context.Context, _ int64, _ storage.UpdateFeedParams) (storage.Feed, error) {
	return storage.Feed{}, nil
}
func (f *fetchFeedStore) UpdateFeedRefreshMeta(_ context.Context, _ storage.UpdateFeedRefreshMetaParams) error {
	return nil
}
func (f *fetchFeedStore) SetFeedNextCheckAt(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (f *fetchFeedStore) DeleteFeed(_ context.Context, _ int64, _ int64) error { return nil }

func TestFetchEntryContent_Smoke(t *testing.T) {
	page := `<html><body><div class="entry-content"><p>Full article text.</p></div></body></html>`
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(pageSrv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	sc := scraper.NewFetcher(scraper.Options{
		HTTPClient:   client,
		UserAgent:    "rssam-test",
		MaxBodyBytes: 1 << 20,
		SSRFGuard:    guard,
	})

	es := &fetchEntryStore{
		entry: storage.Entry{
			ID:      9,
			FeedID:  3,
			URL:     pageSrv.URL,
			Content: "<p>snippet</p>",
		},
	}
	fs := &fetchFeedStore{
		feed: storage.Feed{
			ID:           3,
			ScraperRules: "content=.entry-content",
		},
	}

	s := New(Dependencies{
		AuthToken:      "secret",
		FeedStore:      fs,
		EntryStore:     es,
		HTTPClient:     client,
		FetchUserAgent: "rssam-test",
	})
	s.contentFetcher = &service.ContentFetcher{
		Feeds:   fs,
		Entries: es,
		Scraper: sc,
	}

	h := s.wrapAPI(http.HandlerFunc(s.handleFetchEntryContent))
	req := httptest.NewRequest(http.MethodGet, "/v1/entries/9/fetch-content", nil)
	req.SetPathValue("id", "9")
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp listResponse[entryDTO]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 {
		t.Fatalf("total=%d", resp.Total)
	}
	if !resp.Data.ContentFetched {
		t.Fatal("expected content_fetched")
	}
	if resp.Data.OriginalContent != "<p>snippet</p>" {
		t.Fatalf("original=%q", resp.Data.OriginalContent)
	}
	if resp.Data.Content == "" || resp.Data.Content == "<p>snippet</p>" {
		t.Fatalf("content not scraped: %q", resp.Data.Content)
	}
}
