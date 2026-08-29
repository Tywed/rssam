package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

func TestUI_LoginRateLimit(t *testing.T) {
	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	limiter := middleware.NewLimiter(true, 0.001, 1)
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID:           1,
			Username:     "alice",
			PasswordHash: hash,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		CSRFSecret: "csrf-test",
		RateLimit:  middleware.PerIPRateLimit(limiter),
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h.Register(mux)

	do := func() int {
		token := auth.CSRFToken("csrf-test", "login")
		form := url.Values{"username": {"alice"}, "password": {"wrong"}, "csrf_token": {token}}
		req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.9:12345"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := do(); code != http.StatusOK {
		t.Fatalf("first request: status=%d", code)
	}

	token := auth.CSRFToken("csrf-test", "login")
	form := url.Values{"username": {"alice"}, "password": {"wrong"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.9:12345"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status=%d, want 429", rec.Code)
	}
	var body struct {
		ErrorMessage string `json:"error_message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorMessage == "" {
		t.Fatal("expected error_message in 429 body")
	}
}
