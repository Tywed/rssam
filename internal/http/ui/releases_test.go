package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/githubrel"
	"rssam/internal/storage"
)

// The UI must use the release client it is given (one per process) rather
// than creating its own: one fake GitHub, one request, both endpoints served
// from the same cache.
func TestUI_SharedReleaseClient(t *testing.T) {
	var hits atomic.Int32
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v99.0.0", "html_url": "https://example.test/r", "body": "notes"})
	}))
	defer gh.Close()
	rel := githubrel.New("x/y")
	rel.BaseURL = gh.URL
	rel.TTL = time.Hour

	hash, err := auth.HashPassword("secret")
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(Config{
		Users:      &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: hash, Role: auth.RoleAdmin}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		CSRFSecret: "csrf-test",
		Releases:   rel,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	if n := hits.Load(); n != 0 {
		t.Fatalf("handler construction must not call GitHub, got %d requests", n)
	}

	form := url.Values{"username": {"alice"}, "password": {"secret"}, "csrf_token": {auth.CSRFToken("csrf-test", "login")}}
	req := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var sid string
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			sid = c.Value
		}
	}
	if sid == "" {
		t.Fatal("login failed")
	}

	// Warm the shared client the way the worker's system-alert pass does.
	if _, err := rel.Latest(); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/ui/version", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/ui/version: %d %s", rec.Code, rec.Body.String())
	}
	var st struct {
		Latest      string `json:"latest"`
		UpdateAvail bool   `json:"update_available"`
		Checked     bool   `json:"checked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Checked || !st.UpdateAvail || st.Latest != "v99.0.0" {
		t.Fatalf("unexpected status %+v", st)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("expected the cached release to be reused, GitHub was called %d times", n)
	}
}
