package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func TestParseRetentionForm(t *testing.T) {
	ok := map[string]string{
		"removed_retention_days":       "30",
		"webhook_log_retention_days":   "90",
		"filter_match_retention_days":  "0",
		"feed_poll_log_retention_days": "14",
		"audit_log_retention_days":     "180",
		"cleanup_interval":             "12h30m",
	}
	f, err := parseRetentionForm(func(k string) string { return ok[k] })
	if err != nil {
		t.Fatalf("valid form: %v", err)
	}
	if f.RemovedRetentionDays != 30 || f.WebhookLogRetentionDays != 90 || f.FilterMatchRetentionDays != 0 || f.FeedPollLogRetentionDays != 14 || f.AuditLogRetentionDays != 180 || f.CleanupInterval != 12*time.Hour+30*time.Minute {
		t.Fatalf("parsed = %+v", f)
	}

	bad := []struct {
		name  string
		key   string
		value string
	}{
		{"removed zero", "removed_retention_days", "0"},
		{"removed too big", "removed_retention_days", "3651"},
		{"removed not int", "removed_retention_days", "abc"},
		{"webhook zero", "webhook_log_retention_days", "0"},
		{"filter negative", "filter_match_retention_days", "-1"},
		{"poll log negative", "feed_poll_log_retention_days", "-1"},
		{"poll log too big", "feed_poll_log_retention_days", "3651"},
		{"interval too short", "cleanup_interval", "30s"},
		{"interval too long", "cleanup_interval", "169h"},
		{"interval garbage", "cleanup_interval", "soon"},
		{"interval empty", "cleanup_interval", ""},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			vals := map[string]string{}
			for k, v := range ok {
				vals[k] = v
			}
			vals[tc.key] = tc.value
			if _, err := parseRetentionForm(func(k string) string { return vals[k] }); err == nil {
				t.Fatalf("expected error for %s=%q", tc.key, tc.value)
			}
		})
	}

	// A bare number for the interval means hours.
	vals := map[string]string{}
	for k, v := range ok {
		vals[k] = v
	}
	vals["cleanup_interval"] = "6"
	f, err = parseRetentionForm(func(k string) string { return vals[k] })
	if err != nil || f.CleanupInterval != 6*time.Hour {
		t.Fatalf("bare hours: %+v err=%v", f, err)
	}
}

func TestFormatCleanupInterval(t *testing.T) {
	cases := map[time.Duration]string{
		24 * time.Hour:             "24h",
		90 * time.Minute:           "1h30m",
		45 * time.Minute:           "45m",
		0:                          "",
		168 * time.Hour:            "168h",
		time.Hour + 20*time.Second: "1h", // rounded to minutes
		time.Hour + 30*time.Minute + 40*time.Second: "1h31m",
	}
	for d, want := range cases {
		if got := formatCleanupInterval(d); got != want {
			t.Fatalf("format(%s) = %q, want %q", d, got, want)
		}
	}
	// Roundtrip through the parser.
	for _, d := range []time.Duration{24 * time.Hour, 90 * time.Minute, 7 * 24 * time.Hour} {
		got, err := parseCleanupInterval(formatCleanupInterval(d))
		if err != nil || got != d {
			t.Fatalf("roundtrip %s: got %s err=%v", d, got, err)
		}
	}
}

func newRetentionTestHandler(t *testing.T, envPath string, cleanup func(context.Context) (storage.RetentionCleanupResult, error)) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:    &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:     uiMemEntries{},
		Feeds:       &uiMemFeeds{},
		Categories:  &uiMemCategories{},
		CSRFSecret:  "csrf-test",
		EnvFilePath: envPath,
		Retention: RetentionSettings{
			RemovedRetentionDays:     30,
			WebhookLogRetentionDays:  90,
			FilterMatchRetentionDays: 90,
			FeedPollLogRetentionDays: 14,
			CleanupInterval:          24 * time.Hour,
		},
		RunRetentionCleanup: cleanup,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestAdminRetentionSaveWritesEnv(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte("LISTEN_ADDR=:8080\nWEBHOOK_LOG_RETENTION_DAYS=90\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := newRetentionTestHandler(t, envPath, nil)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	// The system page shows the current values and the form.
	req := httptest.NewRequest(http.MethodGet, "/ui/admin/system", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("system page: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`name="webhook_log_retention_days"`, `value="90"`, `name="cleanup_interval"`, `value="24h"`, `name="feed_poll_log_retention_days"`, `value="14"`, `name="audit_log_retention_days"`, "Хранение журналов"} {
		if !strings.Contains(body, want) {
			t.Fatalf("system page lacks %q", want)
		}
	}
	if strings.Contains(body, "/ui/admin/system/retention/cleanup") {
		t.Fatal("cleanup-now button must be hidden when the hook is nil")
	}

	rec = postForm(t, mux, sid, "/ui/admin/system/retention", url.Values{
		"csrf_token":                   {token},
		"removed_retention_days":       {"14"},
		"webhook_log_retention_days":   {"45"},
		"filter_match_retention_days":  {"0"},
		"feed_poll_log_retention_days": {"7"},
		"audit_log_retention_days":     {"90"},
		"cleanup_interval":             {"6h"},
	})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/admin/system?saved=1" {
		t.Fatalf("save: status=%d loc=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	env := string(raw)
	for _, want := range []string{"LISTEN_ADDR=:8080", "REMOVED_RETENTION_DAYS=14", "WEBHOOK_LOG_RETENTION_DAYS=45", "FILTER_MATCH_RETENTION_DAYS=0", "FEED_POLL_LOG_RETENTION_DAYS=7", "AUDIT_LOG_RETENTION_DAYS=90", "CLEANUP_INTERVAL=6h"} {
		if !strings.Contains(env, want) {
			t.Fatalf(".env lacks %q:\n%s", want, env)
		}
	}
	if strings.Count(env, "WEBHOOK_LOG_RETENTION_DAYS=") != 1 {
		t.Fatalf("key must be replaced, not duplicated:\n%s", env)
	}

	// Invalid values are rejected and nothing is written.
	rec = postForm(t, mux, sid, "/ui/admin/system/retention", url.Values{
		"csrf_token":                   {token},
		"removed_retention_days":       {"0"},
		"webhook_log_retention_days":   {"45"},
		"filter_match_retention_days":  {"0"},
		"feed_poll_log_retention_days": {"7"},
		"audit_log_retention_days":     {"90"},
		"cleanup_interval":             {"6h"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid save: status=%d", rec.Code)
	}
	raw2, _ := os.ReadFile(envPath)
	if string(raw2) != env {
		t.Fatal(".env must not change on validation error")
	}

	// CSRF is enforced.
	rec = postForm(t, mux, sid, "/ui/admin/system/retention", url.Values{
		"removed_retention_days":       {"14"},
		"webhook_log_retention_days":   {"45"},
		"filter_match_retention_days":  {"0"},
		"feed_poll_log_retention_days": {"7"},
		"audit_log_retention_days":     {"90"},
		"cleanup_interval":             {"6h"},
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no csrf: status=%d", rec.Code)
	}
}

func TestAdminRetentionCleanupNow(t *testing.T) {
	calls := 0
	mux := newRetentionTestHandler(t, filepath.Join(t.TempDir(), ".env"), func(context.Context) (storage.RetentionCleanupResult, error) {
		calls++
		return storage.RetentionCleanupResult{WebhookLogs: 5, ExpiredSessions: 2, FeedPollLog: 3}, nil
	})
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	req := httptest.NewRequest(http.MethodGet, "/ui/admin/system", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "/ui/admin/system/retention/cleanup") {
		t.Fatal("cleanup-now button must be shown when the hook is set")
	}

	rec = postForm(t, mux, sid, "/ui/admin/system/retention/cleanup", url.Values{"csrf_token": {token}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/admin/system?cleaned=10" {
		t.Fatalf("cleanup: status=%d loc=%q body=%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("cleanup hook calls = %d", calls)
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/admin/system?cleaned=10", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "удалено строк — 10") {
		t.Fatalf("flash missing: %s", rec.Body.String())
	}

	failing := newRetentionTestHandler(t, filepath.Join(t.TempDir(), ".env"), func(context.Context) (storage.RetentionCleanupResult, error) {
		return storage.RetentionCleanupResult{}, errors.New("db down")
	})
	sid2 := uiSessionCookie(t, nil, failing)
	rec = postForm(t, failing, sid2, "/ui/admin/system/retention/cleanup", url.Values{"csrf_token": {auth.CSRFToken("csrf-test", sid2)}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failing cleanup: status=%d", rec.Code)
	}
}

func TestAdminRetentionRequiresAdmin(t *testing.T) {
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 2, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: false,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		CSRFSecret: "csrf-test",
		Retention:  RetentionSettings{RemovedRetentionDays: 30, WebhookLogRetentionDays: 90, CleanupInterval: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, nil, mux)
	rec := postForm(t, mux, sid, "/ui/admin/system/retention", url.Values{"csrf_token": {auth.CSRFToken("csrf-test", sid)}})
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin: status=%d", rec.Code)
	}
}
