package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
	"rssam/web"
)

// The UI CSP has no 'unsafe-inline', so any style="" or on*="" attribute in
// a template is silently dropped by the browser. Colours go through the
// nonce'd <style> block in the layout, confirms through data-confirm.
func TestUI_TemplatesHaveNoInlineStyleOrHandlers(t *testing.T) {
	inline := regexp.MustCompile(`(?i)\s(style|on[a-z]+)\s*=`)
	err := fs.WalkDir(web.FS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(web.FS, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if inline.MatchString(line) {
				t.Errorf("%s:%d: inline attribute blocked by CSP: %s", path, i+1, strings.TrimSpace(line))
			}
			if strings.Contains(line, `<label><input type="checkbox"`) {
				t.Errorf("%s:%d: checkbox label needs class=\"checkbox-label\" (block label stretches the box)", path, i+1)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUI_ColoursViaNoncedStyle(t *testing.T) {
	catID := int64(7)
	h, err := NewHandler(Config{
		Users:    &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds:    &uiMemFeeds{feeds: []storage.Feed{{ID: 1, Title: "F", CategoryID: &catID}}},
		Categories: &uiMemCategories{cats: []storage.Category{
			{ID: catID, Title: "Cat", Color: "#ff0000"},
			{ID: 8, Title: "Bad", Color: "red;background:url(https://x/)"},
		}},
		Labels: &uiMemLabels{labels: []storage.Label{
			{ID: 3, UserID: 1, Caption: "Росгвардия", BgColor: "#00ff00", FgColor: "#000"},
		}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	srv := middleware.SecurityHeaders(middleware.SecurityConfig{})(mux)
	sid := uiSessionCookie(t, h, mux)

	get := func(path string) (*httptest.ResponseRecorder, string) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec, rec.Body.String()
	}

	rec, body := get("/ui/labels/3")
	if rec.Code != http.StatusOK {
		t.Fatalf("label page: %d", rec.Code)
	}
	nonce := regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`).FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	if nonce == nil {
		t.Fatalf("no nonce in CSP: %q", rec.Header().Get("Content-Security-Policy"))
	}
	for _, want := range []string{
		`<style nonce="` + nonce[1] + `">`,
		`<script nonce="` + nonce[1] + `">`,
		`[data-cat-color="7"]{--cat-color:#ff0000}`,
		`[data-cat-color="8"]{--cat-color:#c0392b}`,
		`[data-label-color="3"]{--label-bg:#00ff00;--label-fg:#000}`,
		`class="tree-row tree-special active" data-label-color="3"`,
		`<span class="label-dot" aria-hidden="true"></span>`,
		`<h1 class="headlines-title" data-label-color="3"><span class="label-badge">Росгвардия</span></h1>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("label page missing %q", want)
		}
	}
	if strings.Contains(body, "url(https://x/)") {
		t.Fatal("unsafe category colour leaked into the page")
	}

	rec, _ = get("/ui/login")
	if rec.Code != http.StatusFound {
		t.Fatalf("login while authenticated: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	body = rec.Body.String()
	nonce = regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`).FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	if nonce == nil || !strings.Contains(body, `<script nonce="`+nonce[1]+`">`) {
		t.Fatal("login page must carry the CSP nonce on its inline script")
	}
	if strings.Contains(body, "<style") {
		t.Fatal("login page has no colours to emit")
	}

}
