package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
	"rssam/internal/ws"

	"golang.org/x/net/websocket"
)

func TestSameOriginRequest(t *testing.T) {
	mk := func(h map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://rss.example.com/v1/feeds", nil)
		r.RemoteAddr = "203.0.113.5:4444" // not a trusted proxy
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		h    map[string]string
		want bool
	}{
		{"no headers at all (curl with cookie)", nil, false},
		{"sec-fetch-site same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"sec-fetch-site none (bookmark)", map[string]string{"Sec-Fetch-Site": "none"}, true},
		{"sec-fetch-site cross-site", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "http://rss.example.com"}, false},
		{"sec-fetch-site same-site (subdomain)", map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"origin matches host", map[string]string{"Origin": "http://rss.example.com"}, true},
		{"origin matches host case-insensitive", map[string]string{"Origin": "HTTP://RSS.Example.COM"}, true},
		{"origin foreign", map[string]string{"Origin": "https://evil.example"}, false},
		{"origin null", map[string]string{"Origin": "null"}, false},
		{"origin with different port", map[string]string{"Origin": "http://rss.example.com:8443"}, false},
		{"referer matches", map[string]string{"Referer": "http://rss.example.com/ui/feeds"}, true},
		{"referer foreign", map[string]string{"Referer": "https://evil.example/attack.html"}, false},
		{"origin wins over referer", map[string]string{"Origin": "https://evil.example", "Referer": "http://rss.example.com/"}, false},
		{"xfh ignored from untrusted peer", map[string]string{"Origin": "https://evil.example", "X-Forwarded-Host": "evil.example"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameOriginRequest(mk(tc.h)); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSameOriginRequest_BehindTrustedProxy(t *testing.T) {
	// nginx proxy_pass without "proxy_set_header Host $host" rewrites Host to
	// the upstream address; X-Forwarded-Host from a trusted proxy restores it.
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/v1/feeds", nil)
	r.RemoteAddr = "127.0.0.1:5555" // loopback is in DefaultTrustedProxies
	r.Header.Set("Origin", "https://rss.example.com")
	r.Header.Set("X-Forwarded-Host", "rss.example.com")
	if !sameOriginRequest(r) {
		t.Fatal("expected same-origin via X-Forwarded-Host from trusted proxy")
	}
	r.Header.Set("X-Forwarded-Host", "other.example.com, rss.example.com")
	if sameOriginRequest(r) {
		t.Fatal("first X-Forwarded-Host value must be used")
	}
	if got := middleware.RequestHost(r); got != "other.example.com" {
		t.Fatalf("RequestHost=%q", got)
	}
}

func newCSRFTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	sessions := &memSessionStore{s: storage.Session{UserID: 1, SessionID: "sess1", ExpiresAt: time.Now().Add(time.Hour)}}
	s := New(Dependencies{
		AuthToken:     "tok",
		UserStore:     &uiMemUsersWS{user: storage.User{ID: 1, IsAdmin: true}},
		SessionStore:  sessions,
		CategoryStore: &fakeCategoryStore{},
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /v1/categories", s.handleListCategories)
	api.HandleFunc("POST /v1/categories", s.handleCreateCategory)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))
	return s, mux
}

// Cookie-authenticated state-changing /v1 calls require same-origin proof;
// token-authenticated calls do not.
func TestWrapAPI_CookieAuthRequiresSameOriginForMutations(t *testing.T) {
	_, mux := newCSRFTestServer(t)
	body := `{"title":"x"}`
	do := func(method string, headers map[string]string, cookie, token bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://rss.example.com/v1/categories", bytes.NewBufferString(body))
		req.RemoteAddr = "203.0.113.5:4444"
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if cookie {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess1"})
		}
		if token {
			req.Header.Set("X-Auth-Token", "tok")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	// Cross-site POST with cookie: the actual attack. Must be 403.
	if rec := do(http.MethodPost, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, true, false); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site cookie POST: got %d want 403 (%s)", rec.Code, rec.Body.String())
	}
	// Cookie POST with no origin evidence at all (curl -b): rejected, use a token.
	if rec := do(http.MethodPost, nil, true, false); rec.Code != http.StatusForbidden {
		t.Fatalf("cookie POST without origin: got %d want 403", rec.Code)
	}
	// Same-origin browser POST with cookie: allowed.
	if rec := do(http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, true, false); rec.Code != http.StatusCreated {
		t.Fatalf("same-origin cookie POST: got %d want 201 (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, map[string]string{"Origin": "http://rss.example.com"}, true, false); rec.Code != http.StatusCreated {
		t.Fatalf("origin-matching cookie POST: got %d want 201", rec.Code)
	}
	// Cookie GET from anywhere: reads are protected by SameSite, not by us.
	if rec := do(http.MethodGet, map[string]string{"Origin": "https://evil.example"}, true, false); rec.Code != http.StatusOK {
		t.Fatalf("cookie GET: got %d want 200", rec.Code)
	}
	// Token auth is never subject to the origin check, even with a foreign Origin.
	if rec := do(http.MethodPost, map[string]string{"Origin": "https://evil.example"}, false, true); rec.Code != http.StatusCreated {
		t.Fatalf("token POST: got %d want 201", rec.Code)
	}
	// Token + stale cookie: token wins, no origin check.
	if rec := do(http.MethodPost, nil, true, true); rec.Code != http.StatusCreated {
		t.Fatalf("token+cookie POST: got %d want 201", rec.Code)
	}
	// Bad token + valid cookie + same origin: falls back to cookie.
	req := httptest.NewRequest(http.MethodPost, "http://rss.example.com/v1/categories", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", "wrong")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess1"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bad token + cookie same-origin: got %d want 201", rec.Code)
	}
	// No credentials at all: still 401, not 403.
	if rec := do(http.MethodPost, nil, false, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST: got %d want 401", rec.Code)
	}
}

// Cross-site WebSocket hijacking: a cookie-authenticated upgrade from a
// foreign Origin must be refused; token auth and same-origin keep working.
func TestHandleWS_CookieAuthRequiresSameOrigin(t *testing.T) {
	sessions := &memSessionStore{s: storage.Session{UserID: 1, SessionID: "sess1", ExpiresAt: time.Now().Add(time.Hour)}}
	hub := ws.NewHub(10, time.Second)
	s := New(Dependencies{
		AuthToken:      "tok",
		UserStore:      &uiMemUsersWS{user: storage.User{ID: 1, IsAdmin: true}},
		SessionStore:   sessions,
		WSEnabled:      true,
		WSHub:          hub,
		WSClientBuffer: 10,
		WSPingInterval: time.Second,
		Logger:         slog.Default(),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/v1", s.handleWS)
	ts := httptest.NewServer(s.wrapMiddleware(mux))
	t.Cleanup(ts.Close)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/v1"
	sameOrigin := ts.URL + "/"

	dial := func(origin string, cookie, token bool) error {
		cfg, err := websocket.NewConfig(wsURL, origin)
		if err != nil {
			t.Fatal(err)
		}
		if cookie {
			cfg.Header.Set("Cookie", auth.SessionCookieName+"=sess1")
		}
		if token {
			cfg.Header.Set("X-Auth-Token", "tok")
		}
		conn, err := websocket.DialConfig(cfg)
		if err == nil {
			conn.Close()
		}
		return err
	}
	// Raw handshake so the exact status code is visible (x/net's dialer only
	// reports "bad status").
	rawStatus := func(origin string) int {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/ws/v1", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "sess1"})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := rawStatus("https://evil.example"); code != http.StatusForbidden {
		t.Fatalf("cross-origin cookie ws: got %d want 403", code)
	}
	if code := rawStatus(""); code != http.StatusForbidden {
		t.Fatalf("cookie ws without Origin: got %d want 403", code)
	}
	if err := dial(sameOrigin, true, false); err != nil {
		t.Fatalf("same-origin cookie ws: %v", err)
	}
	if err := dial("https://evil.example/", false, true); err != nil {
		t.Fatalf("token ws from foreign origin must work: %v", err)
	}
	if err := dial("https://evil.example/", true, false); err == nil {
		t.Fatal("cross-origin cookie ws dial must fail")
	}
}
