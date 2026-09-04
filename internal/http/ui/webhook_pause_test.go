package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemWebhooks struct {
	items map[int64]storage.Webhook
}

func (m *uiMemWebhooks) ListWebhooks(context.Context, int64, int, int) ([]storage.Webhook, int, error) {
	out := make([]storage.Webhook, 0, len(m.items))
	for _, w := range m.items {
		out = append(out, w)
	}
	return out, len(out), nil
}
func (m *uiMemWebhooks) ListEnabledWebhooks(context.Context, int64, int) ([]storage.Webhook, error) {
	out := make([]storage.Webhook, 0)
	for _, w := range m.items {
		if w.Enabled {
			out = append(out, w)
		}
	}
	return out, nil
}
func (m *uiMemWebhooks) CreateWebhook(context.Context, storage.CreateWebhookParams) (storage.Webhook, error) {
	return storage.Webhook{}, nil
}
func (m *uiMemWebhooks) GetWebhook(_ context.Context, _ int64, id int64) (storage.Webhook, error) {
	w, ok := m.items[id]
	if !ok {
		return storage.Webhook{}, storage.ErrNotFound
	}
	return w, nil
}
func (m *uiMemWebhooks) UpdateWebhook(context.Context, storage.UpdateWebhookParams) (storage.Webhook, error) {
	return storage.Webhook{}, nil
}
func (m *uiMemWebhooks) SetWebhookEnabled(_ context.Context, _ int64, id int64, enabled bool) error {
	w, ok := m.items[id]
	if !ok {
		return storage.ErrNotFound
	}
	w.Enabled = enabled
	w.UpdatedAt = time.Now().UTC()
	m.items[id] = w
	return nil
}
func (m *uiMemWebhooks) DeleteWebhook(_ context.Context, _ int64, id int64) error {
	if _, ok := m.items[id]; !ok {
		return storage.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

func TestWebhookPauseUnpause(t *testing.T) {
	whs := &uiMemWebhooks{items: map[int64]storage.Webhook{
		1: {ID: 1, UserID: 1, Name: "alerts", URL: "https://example.com/hook", Enabled: true},
		2: {ID: 2, UserID: 1, Name: "paused", URL: "https://example.com/p", Enabled: false},
	}}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		Webhooks:   whs,
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	post := func(path, status string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {token}, "status": {status}}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := post("/ui/webhooks/1/pause", "ok")
	if rec.Code != http.StatusFound {
		t.Fatalf("pause: status=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/webhooks?status=ok" {
		t.Fatalf("pause redirect: %q", loc)
	}
	if whs.items[1].Enabled {
		t.Fatal("expected webhook 1 paused")
	}

	rec = post("/ui/webhooks/2/unpause", "disabled")
	if rec.Code != http.StatusFound {
		t.Fatalf("unpause: status=%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/webhooks?status=disabled" {
		t.Fatalf("unpause redirect: %q", loc)
	}
	if !whs.items[2].Enabled {
		t.Fatal("expected webhook 2 enabled")
	}

	rec = post("/ui/webhooks/99/pause", "all")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing webhook: status=%d", rec.Code)
	}
}

func TestAdminWebhookStatusLabelPaused(t *testing.T) {
	if got := adminWebhookStatusLabel("disabled"); got != "На паузе" {
		t.Fatalf("got %q", got)
	}
}
