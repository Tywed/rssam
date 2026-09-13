package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestE2E_HealthzAndSystemInfo(t *testing.T) {
	s := New(Dependencies{AuthToken: "secret"})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/system/info", s.handleSystemInfo)
	mux.Handle("/v1/", s.wrapAPI(api))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		// db nil → 503 is expected without DB
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz: got %d", rec.Code)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/system/info", nil)
	req.Header.Set("X-Auth-Token", "secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("system/info: got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestE2E_CategoryCRUD(t *testing.T) {
	s := New(Dependencies{
		AuthToken:     "secret",
		CategoryStore: &fakeCategoryStore{},
	})
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/categories", s.handleCreateCategory)
	api.HandleFunc("GET /v1/categories", s.handleListCategories)
	h := s.wrapAPI(api)

	body := bytes.NewBufferString(`{"title":"News"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/categories", body)
	req.Header.Set("X-Auth-Token", "secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create category: got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/categories", nil)
	req.Header.Set("X-Auth-Token", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list categories: got %d", rec.Code)
	}
	var resp listResponse[[]categoryDTO]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestE2E_OpenAPIDocs(t *testing.T) {
	s := New(Dependencies{})
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", s.handleOpenAPISpec)

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("openapi: got %d", rec.Code)
	}
	if len(rec.Body.Bytes()) < 32 {
		t.Fatalf("openapi spec too short")
	}
}

type memCategoryPollHours struct {
	fakeCategoryStore
	set map[int64]string
}

func (m *memCategoryPollHours) GetCategoryPollHours(_ context.Context, id int64) (string, error) {
	return m.set[id], nil
}

func (m *memCategoryPollHours) SetCategoryPollHours(_ context.Context, _ int64, id int64, v string) error {
	if m.set == nil {
		m.set = map[int64]string{}
	}
	m.set[id] = v
	return nil
}

func TestE2E_CategoryPollHours(t *testing.T) {
	ph := &memCategoryPollHours{}
	s := New(Dependencies{
		AuthToken:         "secret",
		CategoryStore:     ph,
		CategoryPollHours: ph,
	})
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/categories", s.handleCreateCategory)
	api.HandleFunc("PUT /v1/categories/{id}", s.handleUpdateCategory)
	h := s.wrapAPI(api)

	do := func(method, path, body string) (int, categoryDTO) {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("X-Auth-Token", "secret")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var resp listResponse[categoryDTO]
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec.Code, resp.Data
	}
	if code, c := do(http.MethodPost, "/v1/categories", `{"title":"Night","poll_hours":"22:00-6:00"}`); code != http.StatusCreated || c.PollHours != "22:00-06:00" {
		t.Fatalf("create: %d %+v", code, c)
	}
	if ph.set[1] != "22:00-06:00" {
		t.Fatalf("stored %q", ph.set[1])
	}
	if code, _ := do(http.MethodPost, "/v1/categories", `{"title":"Bad","poll_hours":"10:00-10:00"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid create: %d", code)
	}
	if code, c := do(http.MethodPut, "/v1/categories/1", `{"title":"Night"}`); code != http.StatusOK || c.PollHours != "" || ph.set[1] != "22:00-06:00" {
		t.Fatalf("omitted field must keep the window: %d %+v stored=%q", code, c, ph.set[1])
	}
	if code, c := do(http.MethodPut, "/v1/categories/1", `{"title":"Night","poll_hours":""}`); code != http.StatusOK || c.PollHours != "" || ph.set[1] != "" {
		t.Fatalf("empty string must clear: %d %+v stored=%q", code, c, ph.set[1])
	}
}
