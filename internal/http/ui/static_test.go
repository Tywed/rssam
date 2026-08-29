package ui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUI_StaticCacheControl(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/static/app.css?v=abc123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("static: status=%d", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control=%q, want public, max-age=31536000, immutable", got)
	}
}
