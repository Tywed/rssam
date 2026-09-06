package httpserver

import (
	"bytes"
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
