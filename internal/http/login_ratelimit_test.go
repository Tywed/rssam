package httpserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

// POST /ui/login is throttled by its own limiter (LOGIN_RATE_LIMIT_*), not by
// the general RATE_LIMIT_* budget: with the defaults (10 rps, burst 20) a
// single IP could try ~600 passwords a minute; the login budget is 10 then
// one every 5 s.
func TestLoginUsesDedicatedLimiter(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	s := New(Dependencies{
		UserStore:           &uiMemUsersWS{user: storage.User{ID: 1, Username: "admin", PasswordHash: hash}},
		SessionStore:        &memSessionStore{},
		UIEnabled:           true,
		CSRFSecret:          "csrf-test",
		RateLimitEnabled:    true,
		RateLimitRPS:        1000, // general limiter would never trip in this test
		RateLimitBurst:      1000,
		LoginRateLimitRPS:   0.001,
		LoginRateLimitBurst: 3,
	})
	mux := http.NewServeMux()
	s.registerUI(mux)
	h := s.wrapMiddleware(mux)

	attempt := func(ip string) int {
		form := url.Values{"username": {"admin"}, "password": {"wrong"}, "csrf_token": {auth.CSRFToken("csrf-test", "login")}}
		req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip + ":40000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < 3; i++ {
		if code := attempt("203.0.113.5"); code != http.StatusOK {
			t.Fatalf("attempt %d: status=%d, want 200 (login page with error)", i+1, code)
		}
	}
	if code := attempt("203.0.113.5"); code != http.StatusTooManyRequests {
		t.Fatalf("4th attempt: status=%d, want 429", code)
	}
	// Another client is unaffected.
	if code := attempt("203.0.113.6"); code != http.StatusOK {
		t.Fatalf("other IP: status=%d, want 200", code)
	}
	// The general limiter still governs the API path and is not exhausted.
	req := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	req.RemoteAddr = "203.0.113.5:40000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/login after 429: status=%d", rec.Code)
	}
}
