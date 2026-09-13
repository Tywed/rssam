package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rssam/internal/storage"
)

type memAlertStore struct {
	mu       sync.Mutex
	hooks    []storage.Webhook
	paused   []storage.SystemAlertFeed
	silent   []storage.SystemAlertFeed
	failing  []storage.SystemAlertWebhook
	audit    []storage.AuditEvent
	settings map[string]json.RawMessage
	sets     int
}

func (m *memAlertStore) ListSystemAlertWebhooks(context.Context) ([]storage.Webhook, error) {
	return m.hooks, nil
}
func (m *memAlertStore) ListPausedFeeds(context.Context) ([]storage.SystemAlertFeed, error) {
	return m.paused, nil
}
func (m *memAlertStore) ListSilentFeeds(context.Context, time.Duration) ([]storage.SystemAlertFeed, error) {
	return m.silent, nil
}
func (m *memAlertStore) ListFailingWebhooks(context.Context) ([]storage.SystemAlertWebhook, error) {
	return m.failing, nil
}
func (m *memAlertStore) RecordAudit(_ context.Context, ev storage.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audit = append(m.audit, ev)
	return nil
}
func (m *memAlertStore) GetAppSetting(_ context.Context, key string) (json.RawMessage, error) {
	if v, ok := m.settings[key]; ok {
		return v, nil
	}
	return nil, storage.ErrNotFound
}
func (m *memAlertStore) SetAppSetting(_ context.Context, key string, v json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings == nil {
		m.settings = map[string]json.RawMessage{}
	}
	m.settings[key] = v
	m.sets++
	return nil
}

type alertSink struct {
	mu     sync.Mutex
	bodies []string
}

func (s *alertSink) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf strings.Builder
		b := make([]byte, 1<<16)
		n, _ := r.Body.Read(b)
		buf.Write(b[:n])
		s.mu.Lock()
		s.bodies = append(s.bodies, buf.String())
		s.mu.Unlock()
		if strings.Contains(r.URL.Path, "/bot") {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *alertSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func newAlerter(store *memAlertStore, cfg SystemAlertsConfig) *systemAlerter {
	r := &Runner{Log: discardLogger(), Cfg: Config{WebhookTimeout: 2 * time.Second}}
	return &systemAlerter{r: r, store: store, cfg: cfg}
}

func TestSystemAlerts_FirstPassSeedsThenAlertsOnlyNewProblems(t *testing.T) {
	sink := &alertSink{}
	srv := sink.server(t)
	store := &memAlertStore{
		hooks:  []storage.Webhook{{ID: 1, Enabled: true, SystemAlerts: true, Kind: storage.WebhookKindHTTP, URL: srv.URL}},
		paused: []storage.SystemAlertFeed{{ID: 10, Title: "Old broken", Error: "dial tcp"}},
	}
	a := newAlerter(store, SystemAlertsConfig{SilentAfter: 7 * 24 * time.Hour})
	a.load(context.Background())
	ctx := context.Background()

	// Fresh start: existing problems are recorded, not announced.
	a.pass(ctx)
	if sink.count() != 0 || len(store.audit) != 0 || store.sets != 1 {
		t.Fatalf("first pass must only seed: sent=%d audit=%d sets=%d", sink.count(), len(store.audit), store.sets)
	}
	// Nothing changed: no write.
	a.pass(ctx)
	if store.sets != 1 {
		t.Fatalf("unchanged state must not be rewritten: sets=%d", store.sets)
	}

	// One more feed pauses, one goes silent, a webhook starts failing.
	store.paused = append(store.paused, storage.SystemAlertFeed{ID: 11, Title: "New broken", Error: "HTTP 500"})
	store.silent = []storage.SystemAlertFeed{{ID: 12, Title: "Quiet"}}
	store.failing = []storage.SystemAlertWebhook{{ID: 2, Name: "tg", LastError: "chat not found"}}
	a.pass(ctx)
	if sink.count() != 3 || len(store.audit) != 3 {
		t.Fatalf("sent=%d audit=%d", sink.count(), len(store.audit))
	}
	joined := strings.Join(sink.bodies, "\n")
	if strings.Contains(joined, "Old broken") || !strings.Contains(joined, "New broken") || !strings.Contains(joined, "HTTP 500") {
		t.Fatalf("only the new feed must be reported:\n%s", joined)
	}
	if !strings.Contains(joined, "Quiet") || !strings.Contains(joined, "chat not found") || !strings.Contains(joined, `"event_type":"system_alert"`) {
		t.Fatalf("payload:\n%s", joined)
	}
	if store.audit[0].Action != storage.AuditSystemAlert || store.audit[0].ActorName != "worker" || store.audit[0].TargetType != "feeds_paused" {
		t.Fatalf("audit %+v", store.audit[0])
	}

	// Same state again: silence.
	a.pass(ctx)
	if sink.count() != 3 {
		t.Fatalf("repeat alert for unchanged problems: %d", sink.count())
	}

	// Feed recovers and breaks again: announced again.
	store.paused = store.paused[:1]
	a.pass(ctx)
	store.paused = append(store.paused, storage.SystemAlertFeed{ID: 11, Title: "New broken", Error: "HTTP 500"})
	a.pass(ctx)
	if sink.count() != 4 {
		t.Fatalf("re-broken feed must be re-announced: %d", sink.count())
	}

	// State survives a restart through app_settings.
	b := newAlerter(store, SystemAlertsConfig{SilentAfter: 7 * 24 * time.Hour})
	b.load(ctx)
	if !b.seeded || !b.state.Paused[11] || !b.state.Silent[12] || !b.state.Webhooks[2] {
		t.Fatalf("state not restored: %+v", b.state)
	}
	b.pass(ctx)
	if sink.count() != 4 {
		t.Fatalf("restart must not re-alert: %d", sink.count())
	}
}

func TestSystemAlerts_UpdateAndBackupTelegram(t *testing.T) {
	sink := &alertSink{}
	srv := sink.server(t)
	cfg, _ := json.Marshal(storage.TelegramProviderConfig{APIBase: srv.URL, BotToken: "t", ChatID: "7"})
	store := &memAlertStore{
		hooks: []storage.Webhook{{ID: 1, Enabled: true, SystemAlerts: true, Kind: storage.WebhookKindTelegram, ProviderConfig: cfg}},
	}
	dir := t.TempDir()
	old := filepath.Join(dir, "rssam-20260101_000000.dump")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(old, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))
	latest := "v0.1.13"
	a := newAlerter(store, SystemAlertsConfig{
		BackupDir: dir, BackupMaxAge: 36 * time.Hour,
		CurrentVersion: "0.1.12",
		Compare:        func(a, b string) int { return strings.Compare(strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")) },
		LatestRelease:  func() string { return latest },
	})
	// Saved state from a previous run: nothing was broken.
	store.settings = map[string]json.RawMessage{AppSettingSystemAlertState: json.RawMessage(`{}`)}
	a.load(context.Background())
	a.pass(context.Background())
	if sink.count() != 2 {
		t.Fatalf("want update + backup alerts, got %d: %v", sink.count(), sink.bodies)
	}
	joined := strings.Join(sink.bodies, "\n")
	if !strings.Contains(joined, "v0.1.13") || !strings.Contains(joined, "Бэкап устарел") || !strings.Contains(joined, `"parse_mode":"HTML"`) {
		t.Fatalf("bodies:\n%s", joined)
	}

	// Fresh backup clears the flag; the same release is not repeated; a newer one is.
	fresh := filepath.Join(dir, "rssam-20260913_000000.dump")
	if err := os.WriteFile(fresh, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.pass(context.Background())
	if sink.count() != 2 || a.state.Backup {
		t.Fatalf("count=%d backup=%v", sink.count(), a.state.Backup)
	}
	latest = "v0.1.14"
	a.pass(context.Background())
	if sink.count() != 3 || a.state.Release != "v0.1.14" {
		t.Fatalf("count=%d release=%q", sink.count(), a.state.Release)
	}

	// Backup dir gone: no judgement, no alert.
	a.cfg.BackupDir = filepath.Join(dir, "missing")
	a.state.Backup = false
	a.pass(context.Background())
	if sink.count() != 3 {
		t.Fatalf("missing dir must not alert: %d", sink.count())
	}
}

func TestSystemAlerts_NoSubscribersResetsBaseline(t *testing.T) {
	sink := &alertSink{}
	srv := sink.server(t)
	store := &memAlertStore{paused: []storage.SystemAlertFeed{{ID: 1, Title: "x"}}}
	a := newAlerter(store, SystemAlertsConfig{})
	a.load(context.Background())
	a.pass(context.Background())
	if a.seeded {
		t.Fatal("no subscribers: nothing to seed")
	}
	store.hooks = []storage.Webhook{{ID: 1, Enabled: true, SystemAlerts: true, Kind: storage.WebhookKindHTTP, URL: srv.URL}}
	a.pass(context.Background())
	if sink.count() != 0 || !a.seeded || !a.state.Paused[1] {
		t.Fatalf("first pass with a new subscriber seeds silently: sent=%d state=%+v", sink.count(), a.state)
	}
}

func TestFeedsAlert_ListCapAndEscaping(t *testing.T) {
	feeds := make([]storage.SystemAlertFeed, 0, 25)
	for i := 25; i >= 1; i-- {
		feeds = append(feeds, storage.SystemAlertFeed{ID: int64(i), Title: "f<b>", Error: strings.Repeat("e", 200)})
	}
	m := feedsAlert("feeds_paused", "T", feeds, true)
	if !strings.Contains(m.HTML, "f&lt;b&gt;") || strings.Contains(m.HTML, "f<b>") {
		t.Fatalf("html not escaped: %s", m.HTML)
	}
	if !strings.Contains(m.Plain, "… ещё 5") || strings.Count(m.Plain, "\n• ") != alertListMax {
		t.Fatalf("plain:\n%s", m.Plain)
	}
	if !strings.HasPrefix(m.Plain, "T\n• #1 ") {
		t.Fatalf("must be sorted by id: %s", m.Plain[:40])
	}
	if ids := m.Details["feed_ids"].([]int64); len(ids) != 25 {
		t.Fatalf("details ids=%d", len(ids))
	}
	if !strings.Contains(m.Plain, strings.Repeat("e", 120)+"…") {
		t.Fatal("error must be truncated to 120 runes")
	}
}
