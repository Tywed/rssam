package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rssam/internal/audit"
	"rssam/internal/auth"
	"rssam/internal/storage"
)

type adminActionsEnv struct {
	mux      http.Handler
	h        *Handler
	sid      string
	token    string
	sessions *uiMemSessions
	feeds    *uiMemFeeds
	audit    *memAuditStore
}

func newAdminActionsEnv(t *testing.T, admin bool) *adminActionsEnv {
	t.Helper()
	sessions := &uiMemSessions{sessions: map[string]storage.Session{}}
	feeds := &uiMemFeeds{feeds: []storage.Feed{{ID: 1, Title: "News", FeedType: "rss"}}}
	auditStore := &memAuditStore{}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: roleFor(admin),
		}},
		Sessions:    sessions,
		Entries:     uiMemEntries{},
		Feeds:       feeds,
		Categories:  &uiMemCategories{},
		AdminFeeds:  &uiMemAdminFeeds{},
		CSRFSecret:  "csrf-test",
		EnvFilePath: filepath.Join(t.TempDir(), ".env"),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Audit = &audit.Recorder{Store: auditStore}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	return &adminActionsEnv{
		mux: mux, h: h, sid: sid, token: auth.CSRFToken("csrf-test", sid),
		sessions: sessions, feeds: feeds, audit: auditStore,
	}
}

func (e *adminActionsEnv) do(method, path string, form url.Values, withCSRF bool) *httptest.ResponseRecorder {
	if form == nil {
		form = url.Values{}
	}
	if withCSRF {
		form.Set("csrf_token", e.token)
	}
	body := strings.NewReader(form.Encode())
	req := httptest.NewRequest(method, path, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: e.sid})
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *adminActionsEnv) actions() []string {
	var out []string
	e.audit.mu.Lock()
	defer e.audit.mu.Unlock()
	for _, ev := range e.audit.rows {
		out = append(out, ev.Action)
	}
	return out
}

func TestAdminUsersPage(t *testing.T) {
	e := newAdminActionsEnv(t, true)
	rec := e.do(http.MethodGet, "/ui/admin/users", nil, false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alice") {
		t.Fatalf("admin users: status=%d body has user=%v", rec.Code, strings.Contains(rec.Body.String(), "alice"))
	}
	reader := newAdminActionsEnv(t, false)
	if rec := reader.do(http.MethodGet, "/ui/admin/users", nil, false); rec.Code == http.StatusOK {
		t.Fatalf("reader must not see the users page: %d", rec.Code)
	}
}

func TestAdminRefreshAll(t *testing.T) {
	e := newAdminActionsEnv(t, true)
	if rec := e.do(http.MethodPost, "/ui/admin/feeds/refresh-all", nil, false); rec.Code != http.StatusForbidden {
		t.Fatalf("without csrf: %d", rec.Code)
	}
	// No refresher configured: the request is still answered with the
	// redirect and nothing is audited.
	if rec := e.do(http.MethodPost, "/ui/admin/feeds/refresh-all", nil, true); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/admin/users" {
		t.Fatalf("no refresher: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if len(e.actions()) != 0 {
		t.Fatalf("unexpected audit: %v", e.actions())
	}
	var gotUser int64
	e.h.cfg.RefreshAllFeeds = func(_ *http.Request, userID int64) error { gotUser = userID; return nil }
	if rec := e.do(http.MethodPost, "/ui/admin/feeds/refresh-all", nil, true); rec.Code != http.StatusFound {
		t.Fatalf("refresh all: %d", rec.Code)
	}
	if gotUser != 1 || strings.Join(e.actions(), ",") != storage.AuditFeedsRefreshAll {
		t.Fatalf("user=%d audit=%v", gotUser, e.actions())
	}
}

func TestAdminWorkersPauseResume(t *testing.T) {
	e := newAdminActionsEnv(t, true)
	decode := func(rec *httptest.ResponseRecorder) map[string]any {
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("json: %v (%s)", err, rec.Body.String())
		}
		return m
	}
	if rec := e.do(http.MethodPost, "/ui/admin/system/workers/pause", nil, false); rec.Code != http.StatusForbidden {
		t.Fatalf("without csrf: %d", rec.Code)
	}
	// Workers run in another process: the page gets a readable refusal.
	rec := e.do(http.MethodPost, "/ui/admin/system/workers/pause", nil, true)
	if rec.Code != http.StatusBadRequest || decode(rec)["ok"] != false {
		t.Fatalf("pause without workers: %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/ui/admin/system/workers/resume", nil, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("resume without workers: %d", rec.Code)
	}

	var paused, resumed int
	e.h.cfg.PauseWorkers = func() { paused++ }
	e.h.cfg.ResumeWorkers = func() { resumed++ }
	rec = e.do(http.MethodPost, "/ui/admin/system/workers/pause", nil, true)
	if rec.Code != http.StatusOK || decode(rec)["paused"] != true || paused != 1 {
		t.Fatalf("pause: %d %s paused=%d", rec.Code, rec.Body.String(), paused)
	}
	rec = e.do(http.MethodPost, "/ui/admin/system/workers/resume", nil, true)
	if rec.Code != http.StatusOK || decode(rec)["paused"] != false || resumed != 1 {
		t.Fatalf("resume: %d %s resumed=%d", rec.Code, rec.Body.String(), resumed)
	}
	if got := strings.Join(e.actions(), ","); got != storage.AuditWorkersPause+","+storage.AuditWorkersResume {
		t.Fatalf("audit: %s", got)
	}
}

func TestAdminWorkersSave(t *testing.T) {
	e := newAdminActionsEnv(t, true)
	for _, bad := range []url.Values{
		{"worker_pool_size": {"0"}, "webhook_worker_pool_size": {"4"}},
		{"worker_pool_size": {"4"}, "webhook_worker_pool_size": {"1001"}},
		{"worker_pool_size": {"x"}, "webhook_worker_pool_size": {"4"}},
	} {
		if rec := e.do(http.MethodPost, "/ui/admin/system/workers", bad, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("%v: status=%d", bad, rec.Code)
		}
	}
	if _, err := os.Stat(e.h.cfg.EnvFilePath); !os.IsNotExist(err) {
		t.Fatalf("rejected values must not touch the env file: %v", err)
	}
	rec := e.do(http.MethodPost, "/ui/admin/system/workers", url.Values{"worker_pool_size": {"12"}, "webhook_worker_pool_size": {"3"}}, true)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/admin/system?saved=1" {
		t.Fatalf("save: status=%d location=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	env, err := os.ReadFile(e.h.cfg.EnvFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "WORKER_POOL_SIZE=12\n") || !strings.Contains(string(env), "WEBHOOK_WORKER_POOL_SIZE=3\n") {
		t.Fatalf("env file:\n%s", env)
	}
	if got := strings.Join(e.actions(), ","); got != storage.AuditEnvUpdate {
		t.Fatalf("audit: %s", got)
	}
}

func TestAdminFeedRefreshRoute(t *testing.T) {
	e := newAdminActionsEnv(t, true)
	if rec := e.do(http.MethodPost, "/ui/admin/feeds/1/refresh", nil, false); rec.Code != http.StatusForbidden {
		t.Fatalf("without csrf: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/ui/admin/feeds/999/refresh", nil, true); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown feed: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/ui/admin/feeds/abc/refresh", nil, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", rec.Code)
	}
	rec := e.do(http.MethodPost, "/ui/admin/feeds/1/refresh?page=3", nil, true)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/ui/admin/feeds") {
		t.Fatalf("refresh: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLogoutAndRoot(t *testing.T) {
	e := newAdminActionsEnv(t, false)
	if rec := e.do(http.MethodGet, "/ui/", nil, false); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/unread" {
		t.Fatalf("root: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := e.do(http.MethodGet, "/ui/no-such-page", nil, false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown ui path: %d", rec.Code)
	}

	// Logout without a CSRF token clears the cookie but keeps the session
	// row (a cross-site form must not be able to end the session).
	rec := e.do(http.MethodPost, "/ui/logout", nil, false)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/login" {
		t.Fatalf("logout no csrf: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if _, ok := e.sessions.sessions[e.sid]; !ok {
		t.Fatal("session deleted without csrf")
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("session cookie not cleared")
	}

	rec = e.do(http.MethodPost, "/ui/logout", nil, true)
	if rec.Code != http.StatusFound {
		t.Fatalf("logout: %d", rec.Code)
	}
	if _, ok := e.sessions.sessions[e.sid]; ok {
		t.Fatal("session row survived logout")
	}
	if rec := e.do(http.MethodGet, "/ui/unread", nil, false); rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "/ui/login") {
		t.Fatalf("after logout: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
