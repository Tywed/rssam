package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

// The Secure flag on the session cookie must follow the real transport: a
// forged X-Forwarded-Proto from a direct (untrusted) client must not set it —
// browsers drop Secure cookies received over plain HTTP, which surfaces as an
// endless login loop — while a trusted reverse proxy terminating TLS must.
func TestUI_LoginCookieSecureFollowsTrustedForwardedProto(t *testing.T) {
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

	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"),
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	login := func(remote string) *http.Cookie {
		t.Helper()
		token := auth.CSRFToken("csrf-test", "login")
		body := "username=alice&password=secret&csrf_token=" + token
		req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("login status=%d body=%s", rec.Code, rec.Body.String())
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookieName {
				return c
			}
		}
		t.Fatal("no session cookie")
		return nil
	}

	if c := login("203.0.113.9:4444"); c.Secure {
		t.Fatal("forged X-Forwarded-Proto from an untrusted peer must not make the cookie Secure")
	}
	if c := login("127.0.0.1:5555"); !c.Secure {
		t.Fatal("X-Forwarded-Proto: https from a trusted proxy must make the cookie Secure")
	}
}
