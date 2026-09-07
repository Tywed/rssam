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

// uiMemAdminWebhooks is an in-memory storage.AdminWebhookStore backed by a
// uiMemWebhooks for the webhook rows themselves.
type uiMemAdminWebhooks struct {
	base   *uiMemWebhooks
	stats  map[int64]storage.AdminWebhookRow
	resets []int64
}

func (m *uiMemAdminWebhooks) row(w storage.Webhook) storage.AdminWebhookRow {
	r, ok := m.stats[w.ID]
	if !ok {
		r = storage.AdminWebhookRow{}
	}
	r.Webhook = w
	return r
}

func (m *uiMemAdminWebhooks) ListAdminWebhooks(_ context.Context, userID int64) ([]storage.AdminWebhookRow, error) {
	out := make([]storage.AdminWebhookRow, 0, len(m.base.items))
	for _, w := range m.base.items {
		if w.UserID == userID {
			out = append(out, m.row(w))
		}
	}
	return out, nil
}

func (m *uiMemAdminWebhooks) AdminWebhookSummary(_ context.Context, userID int64) (storage.AdminWebhookSummary, error) {
	var s storage.AdminWebhookSummary
	for _, w := range m.base.items {
		if w.UserID != userID {
			continue
		}
		s.TotalWebhooks++
		r := m.row(w)
		s.FailedTotal += r.FailedCount
		s.Sent24h += r.Sent24h
	}
	return s, nil
}

func (m *uiMemAdminWebhooks) GetAdminWebhook(_ context.Context, userID, webhookID int64) (storage.AdminWebhookRow, error) {
	w, ok := m.base.items[webhookID]
	if !ok || w.UserID != userID {
		return storage.AdminWebhookRow{}, storage.ErrNotFound
	}
	return m.row(w), nil
}

func (m *uiMemAdminWebhooks) ListWebhookBindingFeeds(context.Context, int64, int64) ([]storage.WebhookBindingFeed, error) {
	return nil, nil
}

func (m *uiMemAdminWebhooks) ListWebhookBindingFilters(context.Context, int64, int64) ([]storage.WebhookBindingFilter, error) {
	return nil, nil
}

func (m *uiMemAdminWebhooks) ListWebhookLogRows(context.Context, int64, string, *time.Time, int, int) ([]storage.WebhookLogRow, int, error) {
	return nil, 0, nil
}

func (m *uiMemAdminWebhooks) RetryAllWebhookLogs(context.Context, int64) (int64, error) {
	return 0, nil
}

func (m *uiMemAdminWebhooks) ResetWebhookStats(_ context.Context, userID, webhookID int64) error {
	w, ok := m.base.items[webhookID]
	if !ok || w.UserID != userID {
		return storage.ErrNotFound
	}
	m.resets = append(m.resets, webhookID)
	m.stats[webhookID] = storage.AdminWebhookRow{StatsResetAt: ptrTime(time.Now())}
	return nil
}

func ptrTime(t time.Time) *time.Time { return &t }

func newWebhookStatsTestHandler(t *testing.T) (http.Handler, *uiMemAdminWebhooks) {
	t.Helper()
	now := time.Now()
	whs := &uiMemWebhooks{items: map[int64]storage.Webhook{
		1: {ID: 1, UserID: 1, Name: "healthy", URL: "https://example.com/ok", Enabled: true},
		2: {ID: 2, UserID: 1, Name: "broken", URL: "https://example.com/err", Enabled: true},
		3: {ID: 3, UserID: 1, Name: "recovered", URL: "https://example.com/rec", Enabled: true},
		4: {ID: 4, UserID: 7, Name: "foreign", URL: "https://example.com/other", Enabled: true},
	}}
	admin := &uiMemAdminWebhooks{base: whs, stats: map[int64]storage.AdminWebhookRow{
		1: {SentCount: 12, Sent24h: 3, LastSentAt: ptrTime(now.Add(-time.Hour))},
		2: {SentCount: 4, FailedCount: 2, LastSentAt: ptrTime(now.Add(-3 * time.Hour)), LastFailedAt: ptrTime(now.Add(-time.Hour)), LastError: "non-2xx: 500"},
		3: {SentCount: 9, FailedCount: 1, LastSentAt: ptrTime(now.Add(-time.Hour)), LastFailedAt: ptrTime(now.Add(-2 * time.Hour)), LastError: "non-2xx: 503"},
	}}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:      &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:       uiMemEntries{},
		Feeds:         &uiMemFeeds{},
		Categories:    &uiMemCategories{},
		Webhooks:      whs,
		AdminWebhooks: admin,
		CSRFSecret:    "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, admin
}

func postForm(t *testing.T, mux http.Handler, sid, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func getPage(t *testing.T, mux http.Handler, sid, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestWebhooksDashboardStatusesAndErrorFilter(t *testing.T) {
	mux, _ := newWebhookStatsTestHandler(t)
	sid := uiSessionCookie(t, nil, mux)

	rec := getPage(t, mux, sid, "/ui/webhooks")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "Мёртв") || strings.Contains(body, "status=dead") {
		t.Fatal("legacy \"dead\" wording must be gone from the dashboard")
	}
	for _, want := range []string{"Ошибки", "Недоставлено", "Недоставлено всего: 3", `href="/ui/webhooks?status=error"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard lacks %q", want)
		}
	}
	// healthy → ok, broken → error, recovered (success after failure) → ok.
	if got := strings.Count(body, ">В норме</span>"); got != 2 {
		t.Fatalf("ok badges = %d, want 2\n%s", got, body)
	}
	if got := strings.Count(body, ">Ошибки</span>"); got != 1 {
		t.Fatalf("error badges = %d, want 1", got)
	}

	rec = getPage(t, mux, sid, "/ui/webhooks?status=error")
	body = rec.Body.String()
	if !strings.Contains(body, "broken") || strings.Contains(body, "recovered") || strings.Contains(body, "healthy") {
		t.Fatalf("error filter must show only the broken webhook:\n%s", body)
	}

	// The old filter value is not accepted and falls back to "all".
	rec = getPage(t, mux, sid, "/ui/webhooks?status=dead")
	if !strings.Contains(rec.Body.String(), "recovered") || !strings.Contains(rec.Body.String(), "broken") {
		t.Fatal("unknown filter must fall back to all rows")
	}
}

func TestWebhookDetailShowsCountersAndReset(t *testing.T) {
	mux, admin := newWebhookStatsTestHandler(t)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	rec := getPage(t, mux, sid, "/ui/webhooks/3")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Отправлено всего", "<td>9</td>", "Недоставлено всего", "90%", ">В норме</span>", "/ui/webhooks/3/reset-stats", "Сбросить счётчики", "non-2xx: 503"} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail lacks %q\n%s", want, body)
		}
	}

	// Reset: CSRF required, tenant-scoped, redirects with a flash.
	rec = postForm(t, mux, sid, "/ui/webhooks/3/reset-stats", url.Values{})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reset without csrf: %d", rec.Code)
	}
	rec = postForm(t, mux, sid, "/ui/webhooks/4/reset-stats", url.Values{"csrf_token": {token}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign reset: %d", rec.Code)
	}
	rec = postForm(t, mux, sid, "/ui/webhooks/3/reset-stats", url.Values{"csrf_token": {token}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/webhooks/3?stats_reset=1" {
		t.Fatalf("reset: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if len(admin.resets) != 1 || admin.resets[0] != 3 {
		t.Fatalf("resets = %v", admin.resets)
	}

	rec = getPage(t, mux, sid, "/ui/webhooks/3?stats_reset=1")
	body = rec.Body.String()
	if !strings.Contains(body, "Счётчики доставки сброшены") || !strings.Contains(body, "Счётчики сброшены") {
		t.Fatalf("after reset: flash / reset time missing\n%s", body)
	}
	if !strings.Contains(body, ">Без отправок</span>") {
		t.Fatal("after reset the webhook must be idle")
	}
	if strings.Contains(body, "/ui/webhooks/3/reset-stats") {
		t.Fatal("reset button must be hidden when there is nothing to reset")
	}
}
