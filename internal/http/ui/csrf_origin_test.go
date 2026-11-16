package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
)

func TestUI_StateChangeRequiresSameOrigin(t *testing.T) {
	h := newTestUIHandler(t, true)
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	post := func(hdr map[string]string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {auth.CSRFToken("csrf-test", sid)}}
		req := httptest.NewRequest(http.MethodPost, "http://rss.example.com/ui/entries/1/read", strings.NewReader(form.Encode()))
		req.RemoteAddr = "203.0.113.5:4444"
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	for name, tc := range map[string]struct {
		hdr  map[string]string
		deny bool
	}{
		"valid token, Origin evil + cross-site": {map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, true},
		"valid token, Origin evil only":         {map[string]string{"Origin": "https://evil.example"}, true},
		"valid token, no browser headers":       {nil, true},
		"same-origin":                           {map[string]string{"Sec-Fetch-Site": "same-origin"}, false},
		"Origin matches Host":                   {map[string]string{"Origin": "http://rss.example.com"}, false},
		"Referer matches Host":                  {map[string]string{"Referer": "http://rss.example.com/ui/unread"}, false},
	} {
		rec := post(tc.hdr)
		if tc.deny && rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status=%d, want 403", name, rec.Code)
		}
		if !tc.deny && rec.Code == http.StatusForbidden {
			t.Fatalf("%s: unexpected 403: %s", name, rec.Body.String())
		}
	}
	// Reads are not affected.
	req := httptest.NewRequest(http.MethodGet, "http://rss.example.com/ui/unread", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET without browser headers: %d", rec.Code)
	}
}

func TestUI_LoginCSRFTokenPerBrowser(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)
	visit := func() (token string, nonce *http.Cookie) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/login", nil))
		body := rec.Body.String()
		i := strings.Index(body, `name="csrf_token" value="`)
		if i < 0 {
			t.Fatal("no csrf token in login page")
		}
		token = body[i+len(`name="csrf_token" value="`):]
		token = token[:strings.Index(token, `"`)]
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.LoginNonceCookieName {
				nonce = c
			}
		}
		if nonce == nil || nonce.Value == "" || !nonce.HttpOnly || nonce.Path != "/ui/login" {
			t.Fatalf("login nonce cookie missing or weak: %+v", nonce)
		}
		return token, nonce
	}
	t1, c1 := visit()
	t2, c2 := visit()
	if t1 == t2 || c1.Value == c2.Value {
		t.Fatal("two anonymous visitors must not share the login CSRF token")
	}
	login := func(token string, nonce *http.Cookie) *httptest.ResponseRecorder {
		form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {token}}
		req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if nonce != nil {
			req.AddCookie(nonce)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := login(t1, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "CSRF") {
		t.Fatalf("token without its nonce cookie must be rejected: %d", rec.Code)
	}
	if rec := login(t1, c2); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "CSRF") {
		t.Fatalf("token of another browser must be rejected: %d", rec.Code)
	}
	if rec := login(auth.CSRFToken("csrf-test", "login"), c1); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "CSRF") {
		t.Fatalf("legacy constant token must be rejected: %d", rec.Code)
	}
	rec := login(t1, c1)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/unread" {
		t.Fatalf("login: %d %q %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.LoginNonceCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("nonce cookie must be cleared after login")
	}
	// Cross-site login POST (login CSRF) is refused before the token check.
	form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {t1}}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(c1)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site login: %d", rec.Code)
	}
}
