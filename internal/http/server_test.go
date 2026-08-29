package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type fakeCategoryStore struct{}

func (f *fakeCategoryStore) CreateCategory(_ context.Context, _ int64, title, color string) (storage.Category, error) {
	return storage.Category{ID: 1, Title: title, Color: color}, nil
}
func (f *fakeCategoryStore) ListCategories(_ context.Context, _ int64, _, _ int) ([]storage.Category, int, error) {
	return nil, 0, nil
}
func (f *fakeCategoryStore) UpdateCategory(_ context.Context, _ int64, id int64, title, color string) (storage.Category, error) {
	return storage.Category{ID: id, Title: title, Color: color}, nil
}
func (f *fakeCategoryStore) DeleteCategory(_ context.Context, _ int64, _ int64) error {
	return nil
}
func (f *fakeCategoryStore) ReorderCategories(_ context.Context, _ int64, _ []int64) error {
	return nil
}

type fakeFeedStore struct {
	noopFeedStore
	createErr error
}

func (f *fakeFeedStore) CreateFeed(_ context.Context, _ int64, params storage.CreateFeedParams) (storage.Feed, error) {
	if f.createErr != nil {
		return storage.Feed{}, f.createErr
	}
	return storage.Feed{
		ID:              42,
		FeedURL:         params.FeedURL,
		Title:           params.Title,
		CategoryID:      params.CategoryID,
		IntervalMinutes: params.IntervalMinutes,
	}, nil
}
func (f *fakeFeedStore) GetFeed(_ context.Context, _ int64, id int64) (storage.Feed, error) {
	return storage.Feed{ID: id, FeedURL: "https://example.com/feed.xml", IntervalMinutes: 60}, nil
}
func (f *fakeFeedStore) GetFeedByID(_ context.Context, id int64) (storage.Feed, error) {
	return f.GetFeed(context.Background(), 0, id)
}
func (f *fakeFeedStore) UpdateFeed(_ context.Context, _ int64, params storage.UpdateFeedParams) (storage.Feed, error) {
	return storage.Feed{ID: params.ID, FeedURL: params.FeedURL, IntervalMinutes: params.IntervalMinutes}, nil
}

func TestCreateFeedValidation(t *testing.T) {
	s := New(Dependencies{
		AuthToken:     "secret",
		CategoryStore: &fakeCategoryStore{},
		FeedStore:     &fakeFeedStore{},
	})
	h := s.wrapAPI(http.HandlerFunc(s.handleCreateFeed))

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds", bytes.NewBufferString(`{"title":"x"}`))
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func publicExampleLookup(ctx context.Context, host string) ([]string, error) {
	if host == "example.com" {
		return []string{"93.184.216.34"}, nil
	}
	return nil, context.Canceled
}

func testSSRFGuard(t *testing.T) *ssrf.Guard {
	t.Helper()
	g, err := ssrf.New(ssrf.Config{LookupHost: publicExampleLookup})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestCreateFeedDuplicate(t *testing.T) {
	s := New(Dependencies{
		AuthToken:     "secret",
		CategoryStore: &fakeCategoryStore{},
		FeedStore:     &fakeFeedStore{createErr: storage.ErrDuplicateFeedURL},
		SSRFGuard:     testSSRFGuard(t),
	})
	h := s.wrapAPI(http.HandlerFunc(s.handleCreateFeed))

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds", bytes.NewBufferString(`{"feed_url":"https://example.com/feed.xml"}`))
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestCreateFeedSuccessContract(t *testing.T) {
	s := New(Dependencies{
		AuthToken:     "secret",
		CategoryStore: &fakeCategoryStore{},
		FeedStore:     &fakeFeedStore{},
		SSRFGuard:     testSSRFGuard(t),
	})
	h := s.wrapAPI(http.HandlerFunc(s.handleCreateFeed))

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds", bytes.NewBufferString(`{"feed_url":"https://example.com/feed.xml","interval_minutes":15}`))
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	var resp struct {
		Data struct {
			FeedURL string `json:"feed_url"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Total != 1 || resp.Data.FeedURL == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCreateFeedIntervalValidation(t *testing.T) {
	s := New(Dependencies{
		AuthToken:     "secret",
		CategoryStore: &fakeCategoryStore{},
		FeedStore:     &fakeFeedStore{},
		SSRFGuard:     testSSRFGuard(t),
	})
	h := s.wrapAPI(http.HandlerFunc(s.handleCreateFeed))

	t.Run("accepts minimum interval", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/feeds", bytes.NewBufferString(`{"feed_url":"https://example.com/feed.xml","interval_minutes":1}`))
		req.Header.Set("X-Auth-Token", "secret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("rejects below minimum", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/feeds", bytes.NewBufferString(`{"feed_url":"https://example.com/feed.xml","interval_minutes":-1}`))
		req.Header.Set("X-Auth-Token", "secret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}
