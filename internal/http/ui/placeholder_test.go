package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

// A login with the example password must land on the password form, and the
// form must say why; a real password keeps the normal redirect.
func TestUI_LoginWithPlaceholderPasswordGoesToPasswordForm(t *testing.T) {
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "changeme"), Role: auth.RoleAdmin,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)

	form := url.Values{
		"username":   {"alice"},
		"password":   {"changeme"},
		"csrf_token": {auth.CSRFToken("csrf-test", "login")},
		"next":       {"/ui/feeds"},
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/settings?pw=placeholder" {
		t.Fatalf("placeholder login: %d loc=%q", rec.Code, rec.Header().Get("Location"))
	}
	var sid string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sid = c.Value
		}
	}
	if sid == "" {
		t.Fatal("the session must still be created")
	}
	page := getPage(t, mux, sid, "/ui/settings?pw=placeholder")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "паролем из примера") {
		t.Fatalf("settings page: %d, banner missing", page.Code)
	}
}

func TestUI_SystemPageLocaleWarning(t *testing.T) {
	newMux := func(l storage.DatabaseLocale) (http.Handler, string) {
		h, err := NewHandler(Config{
			Users: &uiMemUsers{user: storage.User{
				ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin,
			}},
			Sessions:       &uiMemSessions{sessions: map[string]storage.Session{}},
			Entries:        uiMemEntries{},
			Feeds:          &uiMemFeeds{},
			Categories:     &uiMemCategories{},
			CSRFSecret:     "csrf-test",
			DatabaseLocale: l,
		})
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		h.Register(mux)
		return mux, uiSessionCookie(t, h, mux)
	}
	const marker = "не сворачивает регистр кириллицы"

	mux, sid := newMux(storage.DatabaseLocale{Encoding: "UTF8", Collate: "C", Ctype: "C", LowerOK: false})
	body := getPage(t, mux, sid, "/ui/admin/system").Body.String()
	if !strings.Contains(body, marker) || !strings.Contains(body, "«C» (UTF8)") {
		t.Fatal("locale=C must produce the warning with the ctype named")
	}

	mux, sid = newMux(storage.DatabaseLocale{Encoding: "UTF8", Collate: "C.UTF-8", Ctype: "C.UTF-8", LowerOK: true})
	if strings.Contains(getPage(t, mux, sid, "/ui/admin/system").Body.String(), marker) {
		t.Fatal("a folding ctype must not warn")
	}

	mux, sid = newMux(storage.DatabaseLocale{})
	if strings.Contains(getPage(t, mux, sid, "/ui/admin/system").Body.String(), marker) {
		t.Fatal("an unprobed locale must not warn")
	}
}
