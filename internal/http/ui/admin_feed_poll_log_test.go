package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemPollLog struct {
	rows     []storage.FeedPollLogEntry
	askedID  int64
	askedLim int
}

func (m *uiMemPollLog) RecordFeedPoll(context.Context, storage.RecordFeedPollParams) error {
	return nil
}
func (m *uiMemPollLog) ListFeedPollLog(_ context.Context, feedID int64, limit int) ([]storage.FeedPollLogEntry, error) {
	m.askedID, m.askedLim = feedID, limit
	return m.rows, nil
}

func newAdminFeedDetailHandler(t *testing.T, pollLog storage.FeedPollLogStore) (http.Handler, string) {
	t.Helper()
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:    &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:     uiMemEntries{},
		Feeds:       &uiMemFeeds{feeds: []storage.Feed{{ID: 7, UserID: 1, Title: "Detail feed", FeedURL: "https://example.com/f.xml", IntervalMinutes: 30}}},
		Categories:  &uiMemCategories{},
		AdminFeeds:  &uiMemAdminFeeds{},
		FeedPollLog: pollLog,
		CSRFSecret:  "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, uiSessionCookie(t, h, mux)
}

func TestUI_AdminFeedDetailShowsPollLog(t *testing.T) {
	at := time.Date(2026, 9, 8, 10, 30, 15, 0, time.Local)
	pl := &uiMemPollLog{rows: []storage.FeedPollLogEntry{
		{ID: 2, FeedID: 7, At: at, OK: false, Error: "fetch feed: 503 Service Unavailable", DurationMS: 1200},
		{ID: 1, FeedID: 7, At: at.Add(-time.Hour), OK: true, Inserted: 3, DurationMS: 240},
	}}
	mux, sid := newAdminFeedDetailHandler(t, pl)

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/feeds/7", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if pl.askedID != 7 || pl.askedLim != adminFeedPollLogLimit {
		t.Fatalf("asked feed=%d limit=%d", pl.askedID, pl.askedLim)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Журнал опросов", "08.09.2026 10:30:15", "503 Service Unavailable", "1200 мс",
		">ок<", ">ошибка<", "<td>3</td>", "240 мс",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail page missing %q", want)
		}
	}
	// Newest row first.
	if strings.Index(body, "503 Service Unavailable") > strings.Index(body, "240 мс") {
		t.Fatal("poll log must be rendered newest first")
	}
}

func TestUI_AdminFeedDetailPollLogEmptyAndHidden(t *testing.T) {
	mux, sid := newAdminFeedDetailHandler(t, &uiMemPollLog{})
	req := httptest.NewRequest(http.MethodGet, "/ui/admin/feeds/7", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Опросов ещё не было") {
		t.Fatalf("status=%d, expected empty-state text", rec.Code)
	}

	mux, sid = newAdminFeedDetailHandler(t, nil)
	req = httptest.NewRequest(http.MethodGet, "/ui/admin/feeds/7", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Журнал опросов") {
		t.Fatalf("status=%d, poll log card must be hidden when store is nil", rec.Code)
	}
}
