package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type refreshAllFeedStore struct {
	noopFeedStore
	feeds []storage.Feed
}

func (f *refreshAllFeedStore) ListAllFeeds(_ context.Context, _ int) ([]storage.Feed, error) {
	return f.feeds, nil
}

type enqueueAllStub struct {
	feeds  int
	queued int
	calls  int
}

func (j *enqueueAllStub) EnqueueRefreshAllPollJobs(context.Context) (int, int, error) {
	j.calls++
	return j.feeds, j.queued, nil
}

func TestRefreshAllFeeds_Admin(t *testing.T) {
	jobs := &enqueueAllStub{feeds: 2, queued: 2}
	s := New(Dependencies{
		FeedStore:     &refreshAllFeedStore{},
		EntryStore:    &fakeEntryStore{},
		CategoryStore: &fakeCategoryStore{},
		RefreshAll:    jobs,
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/refresh", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{UserID: 1, IsAdmin: true}))
	rec := httptest.NewRecorder()
	s.handleRefreshAllFeeds(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Feeds  int `json:"feeds"`
			Queued int `json:"queued"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Feeds != 2 || resp.Data.Queued != 2 || jobs.calls != 1 {
		t.Fatalf("unexpected: %+v calls=%d", resp.Data, jobs.calls)
	}
}
