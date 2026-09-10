package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
)

func loginSID(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	token := auth.CSRFToken("csrf-test", "login")
	form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	return ""
}

func postPassword(mux *http.ServeMux, sid, current, next string) *httptest.ResponseRecorder {
	form := url.Values{"current_password": {current}, "password": {next}, "csrf_token": {auth.CSRFToken("csrf-test", sid)}}
	req := httptest.NewRequest(http.MethodPost, "/ui/settings/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// Changing the password requires the current one and revokes the account's
// other sessions.
func TestUI_PasswordChangeRequiresCurrentAndRevokesOtherSessions(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

	sidA := loginSID(t, mux)
	sidB := loginSID(t, mux)

	if rec := postPassword(mux, sidA, "wrong-current", "newpass123"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Текущий пароль неверный") {
		t.Fatalf("wrong current password: code=%d body=%.200s", rec.Code, rec.Body.String())
	}

	if rec := postPassword(mux, sidA, "secret", "newpass123"); rec.Code != http.StatusFound {
		t.Fatalf("valid change: code=%d body=%.200s", rec.Code, rec.Body.String())
	}

	// Session A (the one that changed the password) survives; B is revoked.
	for _, tc := range []struct {
		sid  string
		want int
	}{{sidA, http.StatusOK}, {sidB, http.StatusFound}} {
		req := httptest.NewRequest(http.MethodGet, "/ui/settings", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tc.sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("session %q: got %d want %d", tc.sid[:6], rec.Code, tc.want)
		}
	}
}

// A newly created API token must not appear in the redirect URL (browser
// history, proxy logs).
func TestUI_APIKeyTokenNotInRedirectURL(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)
	sid := loginSID(t, mux)

	form := url.Values{"name": {"cli"}, "csrf_token": {auth.CSRFToken("csrf-test", sid)}}
	req := httptest.NewRequest(http.MethodPost, "/ui/settings/api-keys", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("create key: %d %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "token=") {
		t.Fatalf("token leaked into redirect URL: %s", loc)
	}
	var tokenCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == newTokenCookie {
			tokenCookie = c
		}
	}
	if tokenCookie == nil || tokenCookie.Value == "" || !tokenCookie.HttpOnly {
		t.Fatalf("expected one-shot HttpOnly token cookie, got %+v", tokenCookie)
	}

	// Following GET shows it once and clears the cookie.
	req = httptest.NewRequest(http.MethodGet, "/ui/settings", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	req.AddCookie(tokenCookie)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tokenCookie.Value) {
		t.Fatalf("token not shown on settings page: %d", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == newTokenCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("one-shot cookie was not cleared")
	}
}

func TestUI_LogoutOtherSessions(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

	sidA := loginSID(t, mux)
	sidB := loginSID(t, mux)

	post := func(sid, csrf string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {csrf}}
		req := httptest.NewRequest(http.MethodPost, "/ui/settings/sessions/logout-others", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(sidA, "bogus"); rec.Code != http.StatusForbidden {
		t.Fatalf("bad csrf: code=%d", rec.Code)
	}
	if rec := post(sidA, auth.CSRFToken("csrf-test", sidA)); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/settings?pw=sessions" {
		t.Fatalf("logout others: code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	for _, tc := range []struct {
		sid  string
		want int
	}{{sidA, http.StatusOK}, {sidB, http.StatusFound}} {
		req := httptest.NewRequest(http.MethodGet, "/ui/settings?pw=sessions", nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: tc.sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("session %q: got %d want %d", tc.sid[:6], rec.Code, tc.want)
		}
		if tc.want == http.StatusOK && !strings.Contains(rec.Body.String(), "Остальные сессии завершены") {
			t.Fatal("confirmation flash missing")
		}
	}
}

// SESSION_MAX_AGE drives both the cookie lifetime and the stored expiry.
func TestUI_SessionMaxAgeAppliesToCookieAndStore(t *testing.T) {
	h := newTestUIHandler(t, false)
	h.cfg.SessionMaxAge = 2 * time.Hour
	mux := http.NewServeMux()
	h.Register(mux)

	token := auth.CSRFToken("csrf-test", "login")
	form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var sid string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sid = c.Value
			if c.MaxAge != 7200 {
				t.Fatalf("cookie Max-Age=%d want 7200", c.MaxAge)
			}
		}
	}
	if sid == "" {
		t.Fatal("no session cookie")
	}
	sess, err := h.cfg.Sessions.LookupSession(req.Context(), sid)
	if err != nil {
		t.Fatal(err)
	}
	if until := time.Until(sess.ExpiresAt); until > 2*time.Hour || until < 2*time.Hour-time.Minute {
		t.Fatalf("stored expiry in %s, want ~2h", until)
	}
}
