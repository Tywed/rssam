package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func TestSettingsBridgesForbiddenForNonAdmin(t *testing.T) {
	h := newTestUIHandler(t, false)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/settings/bridges", nil)
	req = withSessionCookie(t, h, req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestSettingsBridgesOverviewAdmin(t *testing.T) {
	h := newTestUIHandler(t, true)
	settings := BridgeSettings{
		Telegram: TelegramBridgeSettings{Enabled: true, MaxPages: 1},
		Max:      MaxBridgeSettings{Enabled: true, APIBaseURL: "http://192.168.0.112:8000"},
		Rutube:   RutubeBridgeSettings{Enabled: true, APIBaseURL: "https://rutube.ru/api"},
	}
	h.cfg.GetBridgeSettings = func() BridgeSettings { return settings }
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/settings/bridges", nil)
	req = withSessionCookie(t, h, req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Telegram") || !strings.Contains(body, "Rutube") {
		t.Fatalf("expected bridge cards in overview")
	}
}

func TestSettingsTelegramRedirect(t *testing.T) {
	h := newTestUIHandler(t, true)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/settings/telegram", nil)
	req = withSessionCookie(t, h, req)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/ui/settings/bridges/telegram" {
		t.Fatalf("location=%q", rec.Header().Get("Location"))
	}
}

func TestNormalizeBridgeFeedType(t *testing.T) {
	cases := map[string]string{
		"telegram":  "telegram",
		"vk_search": "vk_search",
		"vk":        "vk_search",
		"atom":      "rss",
		"":          "rss",
	}
	for in, want := range cases {
		if got := normalizeBridgeFeedType(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func withSessionCookie(t *testing.T, h *Handler, req *http.Request) *http.Request {
	t.Helper()
	user := h.cfg.Users.(*uiMemUsers).user
	sessions := h.cfg.Sessions.(*uiMemSessions)
	sid := "test-session"
	sessions.sessions[sid] = storage.Session{
		UserID:    user.ID,
		SessionID: sid,
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	return req
}
