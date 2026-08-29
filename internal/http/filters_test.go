package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

type memFilterStore struct {
	nextFilterID int64
	nextRuleID   int64
	filters      map[int64]storage.Filter
}

func newMemFilterStore() *memFilterStore {
	return &memFilterStore{
		nextFilterID: 1,
		nextRuleID:   1,
		filters:      map[int64]storage.Filter{},
	}
}

func (m *memFilterStore) ListFilters(_ context.Context, _ int64, _, _ int) ([]storage.Filter, int, error) {
	out := make([]storage.Filter, 0, len(m.filters))
	for _, f := range m.filters {
		out = append(out, f)
	}
	return out, len(out), nil
}

func (m *memFilterStore) CreateFilter(_ context.Context, params storage.CreateFilterParams) (storage.Filter, error) {
	id := m.nextFilterID
	m.nextFilterID++
	now := time.Now().UTC()
	f := storage.Filter{
		ID:        id,
		UserID:    params.UserID,
		Name:      params.Name,
		Enabled:   params.Enabled,
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, rr := range params.Rules {
		rid := m.nextRuleID
		m.nextRuleID++
		f.Rules = append(f.Rules, storage.FilterRule{
			ID:        rid,
			FilterID:  id,
			Field:     rr.Field,
			Pattern:   rr.Pattern,
			Negate:    rr.Negate,
			Op:        rr.Op,
			Priority:  rr.Priority,
			CreatedAt: now,
		})
	}
	m.filters[id] = f
	return f, nil
}

func (m *memFilterStore) GetFilter(_ context.Context, _ int64, id int64) (storage.Filter, error) {
	f, ok := m.filters[id]
	if !ok {
		return storage.Filter{}, storage.ErrNotFound
	}
	return f, nil
}

func (m *memFilterStore) UpdateFilter(_ context.Context, params storage.UpdateFilterParams) (storage.Filter, error) {
	_, ok := m.filters[params.ID]
	if !ok {
		return storage.Filter{}, storage.ErrNotFound
	}
	now := time.Now().UTC()
	f := storage.Filter{
		ID:        params.ID,
		UserID:    params.UserID,
		Name:      params.Name,
		Enabled:   params.Enabled,
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, rr := range params.Rules {
		rid := m.nextRuleID
		m.nextRuleID++
		f.Rules = append(f.Rules, storage.FilterRule{
			ID:        rid,
			FilterID:  params.ID,
			Field:     rr.Field,
			Pattern:   rr.Pattern,
			Negate:    rr.Negate,
			Op:        rr.Op,
			Priority:  rr.Priority,
			CreatedAt: now,
		})
	}
	m.filters[params.ID] = f
	return f, nil
}

func (m *memFilterStore) DeleteFilter(_ context.Context, _ int64, id int64) error {
	if _, ok := m.filters[id]; !ok {
		return storage.ErrNotFound
	}
	delete(m.filters, id)
	return nil
}

func (m *memFilterStore) ListEnabledFilters(_ context.Context, _ int64, _ int) ([]storage.Filter, error) {
	out := make([]storage.Filter, 0, len(m.filters))
	for _, f := range m.filters {
		if f.Enabled {
			out = append(out, f)
		}
	}
	return out, nil
}

type noopMatchStore struct{}

func (n *noopMatchStore) CreateFilterMatches(context.Context, []storage.CreateFilterMatchParams) (int, error) {
	return 0, nil
}

func (n *noopMatchStore) IncrementFilterMatchCount(context.Context, int64, int64) error {
	return nil
}
func (n *noopMatchStore) ListFilterMatches(context.Context, int64, int, int) ([]storage.FilterMatchWithEntry, int, error) {
	return nil, 0, nil
}

func TestFiltersAPI_CreateAndTest_Smoke(t *testing.T) {
	fs := newMemFilterStore()
	eng := filter.New(filter.Config{MaxRulesPerFilter: 50, MaxRegexLength: 2048})
	s := New(Dependencies{
		AuthToken:        "secret",
		FilterStore:      fs,
		FilterMatchStore: &noopMatchStore{},
		FilterEngine:     eng,
	})

	api := http.NewServeMux()
	api.HandleFunc("POST /v1/filters", s.handleCreateFilter)
	api.HandleFunc("POST /v1/filters/{id}/test", s.handleTestFilter)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))

	createReq := httptest.NewRequest(http.MethodPost, "/v1/filters", bytes.NewBufferString(`{
	  "name":"Foo in title",
	  "enabled":true,
	  "rules":[{"field":"title","pattern":"foo","op":"and"}]
	}`))
	createReq.Header.Set("X-Auth-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, createReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if createResp.Data.ID == 0 {
		t.Fatalf("expected id in response")
	}

	testReq := httptest.NewRequest(http.MethodPost, "/v1/filters/"+itoa(createResp.Data.ID)+"/test", bytes.NewBufferString(`{"entry":{"title":"foo","content":"x"}}`))
	testReq.Header.Set("X-Auth-Token", "secret")
	testReq.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, testReq)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var testResp struct {
		Data struct {
			Match bool `json:"match"`
		} `json:"data"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&testResp); err != nil {
		t.Fatalf("decode test: %v", err)
	}
	if !testResp.Data.Match {
		t.Fatalf("expected match=true")
	}
}

func itoa(id int64) string {
	b := make([]byte, 0, 20)
	if id == 0 {
		return "0"
	}
	for id > 0 {
		d := byte(id % 10)
		b = append(b, '0'+d)
		id /= 10
	}
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}
