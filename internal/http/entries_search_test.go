package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/storage"
)

type searchEntryStore struct {
	noopEntryStore
	lastFilter storage.SearchEntriesFilter
	results    []storage.Entry
	total      int
	err        error
	listCalled bool
}

func (s *searchEntryStore) ListEntries(_ context.Context, _ int64, _ storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	s.listCalled = true
	return nil, 0, nil
}
func (s *searchEntryStore) SearchEntries(_ context.Context, _ int64, filter storage.SearchEntriesFilter) ([]storage.Entry, int, error) {
	s.lastFilter = filter
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.results, s.total, nil
}

func TestListEntriesUsesFTSWhenQueryPresent(t *testing.T) {
	now := time.Now().UTC()
	store := &searchEntryStore{
		results: []storage.Entry{{
			ID:        7,
			FeedID:    2,
			Title:     "Kubernetes release",
			URL:       "https://example.com/k8s",
			Status:    storage.EntryStatusUnread,
			CreatedAt: now,
			UpdatedAt: now,
		}},
		total: 1,
	}
	srv := New(Dependencies{
		AuthToken:  "secret",
		EntryStore: store,
	})
	h := srv.wrapAPI(http.HandlerFunc(srv.handleListEntries))

	req := httptest.NewRequest(http.MethodGet, "/v1/entries?q=kubernetes&feed_id=2&status=unread&limit=10&offset=5", nil)
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.listCalled {
		t.Fatal("expected SearchEntries, not ListEntries")
	}
	if store.lastFilter.Query != "kubernetes" {
		t.Fatalf("Query=%q", store.lastFilter.Query)
	}
	if store.lastFilter.FeedID == nil || *store.lastFilter.FeedID != 2 {
		t.Fatalf("FeedID=%v", store.lastFilter.FeedID)
	}
	if store.lastFilter.Status == nil || *store.lastFilter.Status != storage.EntryStatusUnread {
		t.Fatalf("Status=%v", store.lastFilter.Status)
	}
	if store.lastFilter.Limit != 10 || store.lastFilter.Offset != 5 {
		t.Fatalf("limit/offset=%d/%d", store.lastFilter.Limit, store.lastFilter.Offset)
	}
	if !store.lastFilter.Rank {
		t.Fatal("expected Rank=true")
	}

	var resp listResponse[[]entryDTO]
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || len(resp.Data) != 1 || resp.Data[0].Title != "Kubernetes release" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestListEntriesWithoutQueryUsesList(t *testing.T) {
	store := &searchEntryStore{}
	srv := New(Dependencies{AuthToken: "secret", EntryStore: store})
	h := srv.wrapAPI(http.HandlerFunc(srv.handleListEntries))

	req := httptest.NewRequest(http.MethodGet, "/v1/entries?limit=5", nil)
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !store.listCalled {
		t.Fatal("expected ListEntries when q is absent")
	}
	if strings.TrimSpace(store.lastFilter.Query) != "" {
		t.Fatalf("unexpected search filter: %+v", store.lastFilter)
	}
}
