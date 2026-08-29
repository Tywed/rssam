package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type bulkEntryStore struct {
	noopEntryStore
	updated int
	marked  int
}

func (b *bulkEntryStore) BulkUpdateEntries(_ context.Context, _ int64, ids []int64, update storage.BulkEntryUpdate) (int, error) {
	if update.Status != nil && *update.Status == storage.EntryStatusRead {
		b.updated = len(ids)
		return len(ids), nil
	}
	if update.Starred != nil && *update.Starred {
		b.updated = len(ids)
		return len(ids), nil
	}
	return 0, nil
}
func (b *bulkEntryStore) MarkAllFeedEntriesRead(_ context.Context, _, feedID int64) (int, error) {
	if feedID != 3 {
		return 0, storage.ErrNotFound
	}
	b.marked = 5
	return 5, nil
}

func TestBulkUpdateEntries(t *testing.T) {
	store := &bulkEntryStore{}
	s := New(Dependencies{AuthToken: "secret", EntryStore: store})
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(http.HandlerFunc(s.handleBulkUpdateEntries)))

	body := `{"entry_ids":[1,2,3],"status":"read"}`
	req := httptest.NewRequest(http.MethodPut, "/v1/entries", bytes.NewBufferString(body))
	req.Header.Set("X-Auth-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Updated int `json:"updated"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Updated != 3 || store.updated != 3 {
		t.Fatalf("unexpected: %+v store=%d", resp, store.updated)
	}
}

func TestBulkUpdateEntriesStarred(t *testing.T) {
	store := &bulkEntryStore{}
	s := New(Dependencies{AuthToken: "secret", EntryStore: store})
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(http.HandlerFunc(s.handleBulkUpdateEntries)))

	body := `{"entry_ids":[1,2],"starred":true}`
	req := httptest.NewRequest(http.MethodPut, "/v1/entries", bytes.NewBufferString(body))
	req.Header.Set("X-Auth-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.updated != 2 {
		t.Fatalf("expected 2 updated, got %d", store.updated)
	}
}

func TestMarkFeedAllRead(t *testing.T) {
	store := &bulkEntryStore{}
	s := New(Dependencies{AuthToken: "secret", EntryStore: store})
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/feeds/{feedID}/mark-all-as-read", s.handleMarkFeedAllRead)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))

	req := httptest.NewRequest(http.MethodPut, "/v1/feeds/3/mark-all-as-read", nil)
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Marked int `json:"marked"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Marked != 5 {
		t.Fatalf("unexpected: %+v", resp)
	}
}

type categoryMarkStore struct {
	noopEntryStore
}

func (c *categoryMarkStore) MarkAllCategoryEntriesRead(_ context.Context, _, categoryID int64) (int, error) {
	if categoryID != 2 {
		return 0, storage.ErrNotFound
	}
	return 7, nil
}

func TestMarkCategoryAllRead(t *testing.T) {
	store := &categoryMarkStore{}
	s := New(Dependencies{AuthToken: "secret", EntryStore: store})
	api := http.NewServeMux()
	api.HandleFunc("PUT /v1/categories/{categoryID}/mark-all-as-read", s.handleMarkCategoryAllRead)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))

	req := httptest.NewRequest(http.MethodPut, "/v1/categories/2/mark-all-as-read", nil)
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Marked int `json:"marked"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.Marked != 7 {
		t.Fatalf("unexpected: %+v", resp)
	}
}

func TestRefreshAllFeedsRequiresAdmin(t *testing.T) {
	s := New(Dependencies{
		FeedStore:  &fakeFeedStore{},
		EntryStore: &bulkEntryStore{},
		RefreshAll: &enqueueAllStub{feeds: 1, queued: 1},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/refresh", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{UserID: 2, IsAdmin: false}))
	rec := httptest.NewRecorder()
	s.handleRefreshAllFeeds(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/feeds/refresh", nil)
	req2 = req2.WithContext(auth.WithPrincipal(req2.Context(), auth.Principal{UserID: 1, IsAdmin: true}))
	rec2 := httptest.NewRecorder()
	s.handleRefreshAllFeeds(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for admin, got %d: %s", rec2.Code, rec2.Body.String())
	}
}
