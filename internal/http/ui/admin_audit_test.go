package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"rssam/internal/audit"
	"rssam/internal/auth"
	"rssam/internal/storage"
)

type memAuditStore struct {
	mu   sync.Mutex
	rows []storage.AuditEntry
}

func (m *memAuditStore) RecordAudit(_ context.Context, ev storage.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := storage.AuditEntry{
		ID: int64(len(m.rows) + 1), At: time.Now(), ActorName: ev.ActorName, IP: ev.IP,
		Action: ev.Action, TargetType: ev.TargetType, Details: ev.Details,
	}
	if ev.ActorID > 0 {
		e.ActorID = &ev.ActorID
	}
	if ev.TargetID > 0 {
		e.TargetID = &ev.TargetID
	}
	m.rows = append(m.rows, e)
	return nil
}

func (m *memAuditStore) ListAuditLog(_ context.Context, p storage.AuditListParams) ([]storage.AuditEntry, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.AuditEntry
	for i := len(m.rows) - 1; i >= 0; i-- {
		e := m.rows[i]
		if (p.Actor == "" || e.ActorName == p.Actor) && (p.Action == "" || e.Action == p.Action) {
			out = append(out, e)
		}
	}
	return out, len(out), nil
}

func (m *memAuditStore) CountAuditLog(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows), nil
}

// Admin actions in the UI leave audit rows with actor, ip, action and
// target; the audit page lists and filters them; non-admins get 404.
func TestUI_AuditRecordsAdminActions(t *testing.T) {
	store := &memAuditStore{}
	h, err := NewHandler(Config{
		Users:      &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		CSRFSecret: "csrf-test",
		Audit:      &audit.Recorder{Store: store},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	token := auth.CSRFToken("csrf-test", sid)

	if rec := postForm(t, mux, sid, "/ui/admin/users", url.Values{"csrf_token": {token}, "username": {"bob"}, "password": {"longenough1"}}); rec.Code != http.StatusFound {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postForm(t, mux, sid, "/ui/admin/users/7/delete", url.Values{"csrf_token": {token}}); rec.Code != http.StatusFound {
		t.Fatalf("delete user: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/settings/api-keys", url.Values{"csrf_token": {token}, "name": {"cli"}, "scope": {"read"}}); rec.Code != http.StatusFound {
		t.Fatalf("create key: %d %s", rec.Code, rec.Body.String())
	}

	got := make([]string, 0, len(store.rows))
	for _, e := range store.rows {
		got = append(got, e.Action)
		if e.ActorName != "alice" || e.ActorID == nil || *e.ActorID != 1 || e.IP == "" {
			t.Fatalf("actor/ip not recorded: %+v", e)
		}
	}
	if strings.Join(got, ",") != "user.create,user.delete,api_key.create" {
		t.Fatalf("actions = %v", got)
	}
	if d := store.rows[0].Details; d["username"] != "bob" {
		t.Fatalf("create details = %v", d)
	}
	if tid := store.rows[1].TargetID; tid == nil || *tid != 7 || store.rows[1].TargetType != "user" {
		t.Fatalf("delete target = %+v", store.rows[1])
	}
	if d := store.rows[2].Details; d["scope"] != "read" {
		t.Fatalf("key details = %v", d)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	rec := get("/ui/admin/audit")
	if rec.Code != http.StatusOK {
		t.Fatalf("audit page: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"user.create", "user.delete", "api_key.create", "alice", "Всего записей: 3", `href="/ui/admin/audit?actor=alice"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("audit page lacks %q", want)
		}
	}
	body = get("/ui/admin/audit?action=user.delete&actor=alice").Body.String()
	if strings.Contains(body, "api_key.create</code>") || !strings.Contains(body, "Всего записей: 1") {
		t.Fatalf("filter not applied:\n%s", body)
	}
	if body := get("/ui/admin/system").Body.String(); !strings.Contains(body, "3 записей") {
		t.Fatal("system page must show the audit row count")
	}
}

func TestUI_AuditPageHiddenWithoutStoreAndForNonAdmin(t *testing.T) {
	h := newTestUIHandler(t, true)
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	req := httptest.NewRequest(http.MethodGet, "/ui/admin/audit", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no store: %d", rec.Code)
	}

	h = newTestUIHandler(t, false)
	h.cfg.Audit = &audit.Recorder{Store: &memAuditStore{}}
	mux = http.NewServeMux()
	h.Register(mux)
	sid = uiSessionCookie(t, h, mux)
	req = httptest.NewRequest(http.MethodGet, "/ui/admin/audit", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("non-admin must not see the audit page: %d", rec.Code)
	}
}
