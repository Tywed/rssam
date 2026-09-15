package ui

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

func TestUI_LoginPageWarnsWhenHSTSWithoutTLS(t *testing.T) {
	prev := middleware.TrustedProxies()
	if err := middleware.SetTrustedProxies([]string{"127.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		strs := make([]string, 0, len(prev))
		for _, p := range prev {
			strs = append(strs, p.String())
		}
		_ = middleware.SetTrustedProxies(strs)
	})

	var logBuf bytes.Buffer
	h, err := NewHandler(Config{
		Users:       &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret")}},
		Sessions:    &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:     uiMemEntries{},
		Feeds:       &uiMemFeeds{},
		Categories:  &uiMemCategories{},
		CSRFSecret:  "csrf-test",
		HSTSEnabled: true,
		Logger:      slog.New(slog.NewTextHandler(&logBuf, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	get := func(remote, proto string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
		req.RemoteAddr = remote
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d", rec.Code)
		}
		return rec.Body.String()
	}
	const hint = "Включён HSTS, но страница открыта по HTTP"

	if body := get("203.0.113.9:4444", ""); !strings.Contains(body, hint) {
		t.Fatal("plain HTTP with HSTS=true must show the hint")
	}
	if body := get("203.0.113.9:4444", "https"); !strings.Contains(body, hint) {
		t.Fatal("X-Forwarded-Proto from an untrusted peer must not silence the hint")
	}
	if body := get("127.0.0.1:5555", "https"); strings.Contains(body, hint) {
		t.Fatal("a trusted proxy announcing https is the intended deployment: no hint")
	}
	get("203.0.113.9:4444", "")
	if n := strings.Count(logBuf.String(), "HSTS=true but the login page"); n != 1 {
		t.Fatalf("warning must be logged exactly once per process, got %d:\n%s", n, logBuf.String())
	}
}
