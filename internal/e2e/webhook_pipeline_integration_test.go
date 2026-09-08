//go:build integration

// Package e2e drives the real poll → filter → webhook pipeline end to end:
// a worker.Runner with a service.FeedRefresher against PostgreSQL, an
// httptest RSS server as the feed source and an httptest receiver as the
// webhook target. Nothing is mocked below the HTTP boundary.
//
// Run (requires PostgreSQL; use a dedicated test database):
//
//	DATABASE_URL='postgres://rssam:rssam@localhost:5432/rssam_test?sslmode=disable' \
//	  go test ./internal/e2e/... -tags=integration -count=1 -v
package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"rssam/internal/auth"
	"rssam/internal/filter"
	"rssam/internal/migrations"
	"rssam/internal/reader"
	"rssam/internal/service"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
	"rssam/internal/worker"
)

const (
	e2eWait = 30 * time.Second
	e2ePoll = 100 * time.Millisecond
)

// receivedHook is one request captured by the webhook receiver.
type receivedHook struct {
	Path   string
	Header http.Header
	Body   []byte
}

// hookReceiver records every webhook request and lets a test flip the
// /fail endpoint between 500 and 200 to simulate a recovering target.
type hookReceiver struct {
	mu      sync.Mutex
	got     []receivedHook
	failing atomic.Bool
}

func (h *hookReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	h.got = append(h.got, receivedHook{Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
	h.mu.Unlock()
	if r.URL.Path == "/fail" && h.failing.Load() {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *hookReceiver) byPath(path string) []receivedHook {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []receivedHook
	for _, g := range h.got {
		if g.Path == path {
			out = append(out, g)
		}
	}
	return out
}

func rssDocument(title string, items []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>`)
	b.WriteString(title)
	b.WriteString(`</title>`)
	for i, it := range items {
		fmt.Fprintf(&b, `<item><title>%s</title><link>https://example.org/%s/%d</link><description>body %d</description></item>`, it, strings.ToLower(strings.ReplaceAll(title, " ", "-")), i, i)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(e2eWait)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(e2ePoll)
	}
	t.Fatalf("timeout waiting for %s", what)
}

type e2eEnv struct {
	pool     *pgxpool.Pool
	store    *storage.PostgresStore
	runner   *worker.Runner
	receiver *hookReceiver
	status   *feedStatusSink
	hooks    *httptest.Server
	feeds    *httptest.Server
	user     storage.User
}

// feedStatusSink records feed_status_changed publications (what the WS hub
// would fan out) so tests can assert on the persisted state the event carries.
type feedStatusSink struct {
	mu     sync.Mutex
	events []feedStatusEvent
}

type feedStatusEvent struct {
	Feed storage.Feed
	Err  error
}

func (s *feedStatusSink) PublishNewEntries(context.Context, storage.Feed, []storage.Entry) {}
func (s *feedStatusSink) PublishFeedStatusChanged(feed storage.Feed, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, feedStatusEvent{Feed: feed, Err: err})
}
func (s *feedStatusSink) forFeed(feedID int64) []feedStatusEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []feedStatusEvent
	for _, e := range s.events {
		if e.Feed.ID == feedID {
			out = append(out, e)
		}
	}
	return out
}

// newEnv wires the production object graph (store, refresher, runner) with an
// SSRF guard that allows loopback so the httptest servers are reachable, and
// starts the worker. Everything is torn down in t.Cleanup.
func newEnv(t *testing.T, feedDocs map[string]string) *e2eEnv {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping e2e test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := storage.NewPostgresPool(ctx, dsn, 0)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Apply(ctx, pool, slog.Default()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	store := storage.NewPostgresStore(pool)

	statusSink := &feedStatusSink{}
	receiver := &hookReceiver{}
	receiver.failing.Store(true)
	hooks := httptest.NewServer(receiver)
	t.Cleanup(hooks.Close)
	feeds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, ok := feedDocs[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = io.WriteString(w, doc)
	}))
	t.Cleanup(feeds.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	fetchClient := guard.HTTPClient(10 * time.Second)
	registry := reader.NewHandlerRegistry(reader.NewRSSHandler(reader.NewRSSFetcher(fetchClient, "rssam-e2e", guard, "")))

	logOut := io.Discard
	if testing.Verbose() {
		logOut = os.Stderr
	}
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelDebug}))

	refresher := &service.FeedRefresher{
		Feeds:       store,
		Entries:     store,
		Dedup:       store,
		Registry:    registry,
		Filters:     store,
		Matches:     store,
		Labels:      store,
		Engine:      filter.New(filter.Config{}),
		Webhooks:    store,
		WebhookLogs: store,
		PollLog:     store,
		Realtime:    statusSink,
		Log:         log,
	}
	runner := &worker.Runner{
		Log:               log,
		Store:             store,
		Refresher:         refresher,
		WebhookHTTPClient: guard.HTTPClient(5 * time.Second),
		SSRFGuard:         guard,
		Cfg: worker.Config{
			PoolSize:        2,
			WebhookPoolSize: 2,
			SchedulerTick:   200 * time.Millisecond,
			FetchTimeout:    10 * time.Second,
			InstanceID:      fmt.Sprintf("e2e-%d", time.Now().UnixNano()),

			// Two attempts with a ~1 s backoff: 500 → failed(retry) → dead.
			WebhookMaxAttempts: 2,
			WebhookTimeout:     5 * time.Second,
			WebhookRetryBase:   time.Second,
			WebhookRetryMax:    2 * time.Second,

			RemovedRetentionDays:    30,
			WebhookLogRetentionDays: 1,
		},
	}

	hash, err := auth.HashPassword("e2e-secret")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser(context.Background(), storage.CreateUserParams{
		Username:     fmt.Sprintf("e2e_%d", time.Now().UnixNano()),
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), user.ID) })

	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runner.Run(runCtx)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})

	return &e2eEnv{pool: pool, store: store, runner: runner, receiver: receiver, status: statusSink, hooks: hooks, feeds: feeds, user: user}
}

func (e *e2eEnv) createWebhook(t *testing.T, p storage.CreateWebhookParams) storage.Webhook {
	t.Helper()
	p.UserID = e.user.ID
	if p.Method == "" {
		p.Method = http.MethodPost
	}
	if p.Kind == "" {
		p.Kind = storage.WebhookKindHTTP
	}
	p.Enabled = true
	wh, err := e.store.CreateWebhook(context.Background(), p)
	if err != nil {
		t.Fatalf("create webhook %s: %v", p.Name, err)
	}
	return wh
}

func (e *e2eEnv) createFeed(t *testing.T, path string, webhookID *int64) storage.Feed {
	t.Helper()
	feed, err := e.store.CreateFeed(context.Background(), e.user.ID, storage.CreateFeedParams{
		FeedURL:         e.feeds.URL + path,
		FeedType:        reader.FeedTypeRSS,
		Title:           "e2e " + path,
		IntervalMinutes: 60,
		WebhookID:       webhookID,
	})
	if err != nil {
		t.Fatalf("create feed %s: %v", path, err)
	}
	return feed
}

func (e *e2eEnv) feedEntries(t *testing.T, feedID int64) []storage.Entry {
	t.Helper()
	entries, _, err := e.store.ListFeedEntries(context.Background(), e.user.ID, feedID, storage.ListEntriesFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	return entries
}

func (e *e2eEnv) webhookLogs(t *testing.T, webhookID int64) []storage.WebhookLog {
	t.Helper()
	logs, _, err := e.store.ListWebhookLogs(context.Background(), webhookID, 100, 0)
	if err != nil {
		t.Fatalf("list webhook logs: %v", err)
	}
	return logs
}

func (e *e2eEnv) adminRow(t *testing.T, webhookID int64) storage.AdminWebhookRow {
	t.Helper()
	row, err := e.store.GetAdminWebhook(context.Background(), e.user.ID, webhookID)
	if err != nil {
		t.Fatalf("admin webhook row: %v", err)
	}
	return row
}

func signHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestE2E_FeedWebhook_TemplateHMACOnSuccess: a feed bound to a webhook with a
// body_template, custom headers, a secret and on_success_entry=mark_read.
// After one poll every new entry is delivered exactly once with a rendered
// body, a valid HMAC signature and the custom header; the entries become
// read and the persistent counters reflect the deliveries.
func TestE2E_FeedWebhook_TemplateHMACOnSuccess(t *testing.T) {
	env := newEnv(t, map[string]string{
		"/ok.xml": rssDocument("OK feed", []string{"first post", "second post"}),
	})
	const secret = "e2e-hmac-secret"
	wh := env.createWebhook(t, storage.CreateWebhookParams{
		Name:           "templated",
		URL:            env.hooks.URL + "/templated",
		Headers:        []byte(`{"X-Token":"abc","X-RSSAM-Signature":"forged"}`),
		BodyTemplate:   `{"title":{{printf "%q" .entry.Title}},"feed":{{printf "%q" .feed.Title}}}`,
		Secret:         secret,
		OnSuccessEntry: storage.WebhookOnSuccessMarkRead,
	})
	feed := env.createFeed(t, "/ok.xml", &wh.ID)

	waitFor(t, "2 templated deliveries", func() bool { return len(env.receiver.byPath("/templated")) == 2 })
	waitFor(t, "both logs sent", func() bool {
		logs := env.webhookLogs(t, wh.ID)
		if len(logs) != 2 {
			return false
		}
		return logs[0].Status == "sent" && logs[1].Status == "sent"
	})

	got := env.receiver.byPath("/templated")
	titles := map[string]bool{}
	for _, g := range got {
		if sig := g.Header.Get("X-RSSAM-Signature"); sig != signHMAC(secret, g.Body) {
			t.Fatalf("signature %q does not match body %s", sig, g.Body)
		}
		if g.Header.Get("X-Token") != "abc" {
			t.Fatalf("custom header missing: %v", g.Header)
		}
		if ct := g.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content-type = %q", ct)
		}
		var body struct {
			Title string `json:"title"`
			Feed  string `json:"feed"`
		}
		if err := json.Unmarshal(g.Body, &body); err != nil {
			t.Fatalf("body is not the rendered template: %s (%v)", g.Body, err)
		}
		if body.Feed != feed.Title {
			t.Fatalf("feed title in body = %q, want %q", body.Feed, feed.Title)
		}
		titles[body.Title] = true
	}
	if !titles["first post"] || !titles["second post"] {
		t.Fatalf("delivered titles: %v", titles)
	}

	// on_success_entry=mark_read flipped both entries.
	waitFor(t, "entries marked read", func() bool {
		for _, e := range env.feedEntries(t, feed.ID) {
			if e.Status != storage.EntryStatusRead {
				return false
			}
		}
		return len(env.feedEntries(t, feed.ID)) == 2
	})

	// Persistent counters.
	row := env.adminRow(t, wh.ID)
	if row.SentCount != 2 || row.FailedCount != 0 || row.LastSentAt == nil || row.LastFailedAt != nil {
		t.Fatalf("counters: sent=%d failed=%d last_sent=%v last_failed=%v", row.SentCount, row.FailedCount, row.LastSentAt, row.LastFailedAt)
	}

	// The poll itself was recorded as successful and rescheduled.
	f, err := env.store.GetFeedByID(context.Background(), feed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.LastCheckedAt == nil || f.LastError != "" || f.NextCheckAt == nil || !f.NextCheckAt.After(time.Now()) {
		t.Fatalf("feed meta after poll: checked=%v err=%q next=%v", f.LastCheckedAt, f.LastError, f.NextCheckAt)
	}

	// A second poll of the same document must not deliver anything again.
	if err := env.store.SetFeedNextCheckAt(context.Background(), feed.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second poll", func() bool {
		f, err := env.store.GetFeedByID(context.Background(), feed.ID)
		return err == nil && f.NextCheckAt != nil && f.NextCheckAt.After(time.Now())
	})
	if n := len(env.receiver.byPath("/templated")); n != 2 {
		t.Fatalf("re-poll re-delivered: %d requests", n)
	}
}

// TestE2E_FailingWebhook_BackoffDeadThenRecovers: the receiver answers 500.
// The first attempt schedules a retry (transient, failed_total untouched),
// the second attempt exhausts WebhookMaxAttempts → dead, failed_total=1.
// Once the receiver is healthy again, a manual retry delivers the log and
// last_sent_at moves past last_failed_at (the webhook is "ok" again).
func TestE2E_FailingWebhook_BackoffDeadThenRecovers(t *testing.T) {
	env := newEnv(t, map[string]string{
		"/fail.xml": rssDocument("Fail feed", []string{"only post"}),
	})
	wh := env.createWebhook(t, storage.CreateWebhookParams{Name: "failing", URL: env.hooks.URL + "/fail"})
	env.createFeed(t, "/fail.xml", &wh.ID)

	// Attempt 1: 500 → failed with a retry scheduled.
	waitFor(t, "first failed attempt", func() bool {
		logs := env.webhookLogs(t, wh.ID)
		return len(logs) == 1 && logs[0].Attempt >= 1 && logs[0].Status != "pending"
	})
	logs := env.webhookLogs(t, wh.ID)
	if logs[0].Attempt == 1 {
		if logs[0].Status != "failed" || logs[0].NextRetryAt == nil {
			t.Fatalf("after attempt 1: status=%s next=%v", logs[0].Status, logs[0].NextRetryAt)
		}
		if logs[0].LastStatusCode == nil || *logs[0].LastStatusCode != http.StatusInternalServerError {
			t.Fatalf("last_status_code=%v", logs[0].LastStatusCode)
		}
		row := env.adminRow(t, wh.ID)
		if row.FailedCount != 0 || row.LastError == "" {
			t.Fatalf("transient failure must not count as failed: failed=%d last_error=%q", row.FailedCount, row.LastError)
		}
	}

	// Attempt 2 (after ~1 s backoff): dead.
	waitFor(t, "dead after max attempts", func() bool {
		logs := env.webhookLogs(t, wh.ID)
		return len(logs) == 1 && logs[0].Status == "dead"
	})
	logs = env.webhookLogs(t, wh.ID)
	if logs[0].Attempt != 2 || logs[0].NextRetryAt != nil {
		t.Fatalf("dead log: attempt=%d next=%v", logs[0].Attempt, logs[0].NextRetryAt)
	}
	if n := len(env.receiver.byPath("/fail")); n != 2 {
		t.Fatalf("receiver saw %d attempts, want 2", n)
	}
	row := env.adminRow(t, wh.ID)
	if row.FailedCount != 1 || row.LastFailedAt == nil || row.SentCount != 0 || !strings.Contains(row.LastError, "500") {
		t.Fatalf("counters after dead: sent=%d failed=%d last_failed=%v last_error=%q", row.SentCount, row.FailedCount, row.LastFailedAt, row.LastError)
	}
	// Dead logs are not picked up again on their own.
	time.Sleep(1500 * time.Millisecond)
	if n := len(env.receiver.byPath("/fail")); n != 2 {
		t.Fatalf("dead log was retried automatically: %d requests", n)
	}

	// Receiver recovers; the user retries the log from the UI.
	env.receiver.failing.Store(false)
	if err := env.store.RetryWebhookLogNow(context.Background(), env.user.ID, logs[0].ID); err != nil {
		t.Fatalf("retry now: %v", err)
	}
	waitFor(t, "recovered delivery", func() bool {
		logs := env.webhookLogs(t, wh.ID)
		return len(logs) == 1 && logs[0].Status == "sent"
	})
	row = env.adminRow(t, wh.ID)
	if row.SentCount != 1 || row.FailedCount != 1 || row.LastSentAt == nil || row.LastFailedAt == nil {
		t.Fatalf("counters after recovery: %+v", row)
	}
	if !row.LastSentAt.After(*row.LastFailedAt) {
		t.Fatalf("last_sent_at %v must be after last_failed_at %v so the webhook is 'ok' again", row.LastSentAt, row.LastFailedAt)
	}
}

// TestE2E_FilterPipeline_LabelWebhookDelete: two filters on one feed. The
// first labels matching entries and fires a webhook (event_type
// entry_matched, filter in payload, no signature without a secret); the
// second removes matching entries. Non-matching entries are left alone and
// every match is recorded in filter_matches.
func TestE2E_FilterPipeline_LabelWebhookDelete(t *testing.T) {
	env := newEnv(t, map[string]string{
		"/mixed.xml": rssDocument("Mixed feed", []string{"ALERT disk full", "SPAM buy now", "plain news"}),
	})
	ctx := context.Background()
	label, err := env.store.CreateLabel(ctx, storage.CreateLabelParams{UserID: env.user.ID, Caption: fmt.Sprintf("alerts-%d", time.Now().UnixNano())})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.store.DeleteLabel(context.Background(), env.user.ID, label.ID) })
	wh := env.createWebhook(t, storage.CreateWebhookParams{Name: "alerts", URL: env.hooks.URL + "/filter"})

	alerts, err := env.store.CreateFilter(ctx, storage.CreateFilterParams{
		UserID: env.user.ID, Name: "alerts", Enabled: true, FeedScope: storage.FilterFeedScopeAll,
		Rules: []storage.CreateFilterRuleParams{{Field: "title", Pattern: `^ALERT\b`}},
		Actions: []storage.CreateFilterActionParams{
			{ActionType: storage.FilterActionLabel, ActionParam: fmt.Sprint(label.ID)},
			{ActionType: storage.FilterActionWebhook, ActionParam: fmt.Sprint(wh.ID)},
		},
	})
	if err != nil {
		t.Fatalf("create alerts filter: %v", err)
	}
	spam, err := env.store.CreateFilter(ctx, storage.CreateFilterParams{
		UserID: env.user.ID, Name: "spam", Enabled: true, FeedScope: storage.FilterFeedScopeAll,
		Rules:   []storage.CreateFilterRuleParams{{Field: "title", Pattern: `(?i)\bspam\b`}},
		Actions: []storage.CreateFilterActionParams{{ActionType: storage.FilterActionDelete}},
	})
	if err != nil {
		t.Fatalf("create spam filter: %v", err)
	}
	feed := env.createFeed(t, "/mixed.xml", nil)

	waitFor(t, "3 entries stored", func() bool { return len(env.feedEntries(t, feed.ID)) == 3 })
	waitFor(t, "alert webhook delivered", func() bool {
		logs := env.webhookLogs(t, wh.ID)
		return len(logs) == 1 && logs[0].Status == "sent"
	})

	byTitle := map[string]storage.Entry{}
	for _, e := range env.feedEntries(t, feed.ID) {
		byTitle[e.Title] = e
	}
	if byTitle["SPAM buy now"].Status != storage.EntryStatusRemoved {
		t.Fatalf("spam entry status = %q, want removed", byTitle["SPAM buy now"].Status)
	}
	if byTitle["plain news"].Status != storage.EntryStatusUnread || byTitle["ALERT disk full"].Status != storage.EntryStatusUnread {
		t.Fatalf("non-delete entries changed: plain=%q alert=%q", byTitle["plain news"].Status, byTitle["ALERT disk full"].Status)
	}

	labelled, _, err := env.store.ListEntries(ctx, env.user.ID, storage.ListEntriesFilter{LabelID: &label.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(labelled) != 1 || labelled[0].Title != "ALERT disk full" {
		t.Fatalf("labelled entries: %+v", labelled)
	}

	got := env.receiver.byPath("/filter")
	if len(got) != 1 {
		t.Fatalf("filter webhook requests = %d, want 1", len(got))
	}
	if got[0].Header.Get("X-RSSAM-Signature") != "" {
		t.Fatal("signature header present without a secret")
	}
	// Payload contract (event_version 1): snake_case envelope, Go field
	// names inside entry/feed/filter (storage structs without json tags).
	var payload struct {
		EventVersion int                    `json:"event_version"`
		EventType    string                 `json:"event_type"`
		Entry        struct{ Title string } `json:"entry"`
		Feed         struct{ Title string } `json:"feed"`
		Filter       *struct{ Name string } `json:"filter"`
		MatchDetails json.RawMessage        `json:"match_details"`
	}
	if err := json.Unmarshal(got[0].Body, &payload); err != nil {
		t.Fatalf("payload: %v\n%s", err, got[0].Body)
	}
	if payload.EventVersion != 1 || payload.EventType != "entry_matched" || payload.Entry.Title != "ALERT disk full" || payload.Feed.Title != feed.Title || payload.Filter == nil || payload.Filter.Name != "alerts" || len(payload.MatchDetails) == 0 {
		t.Fatalf("payload mismatch: %s", got[0].Body)
	}

	for _, f := range []storage.Filter{alerts, spam} {
		_, total, err := env.store.ListFilterMatches(ctx, f.ID, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if total != 1 {
			t.Fatalf("filter %s matches = %d, want 1", f.Name, total)
		}
	}

	// Log retention through the worker: age the log, run the same pass the
	// cleanup loop runs, the log is gone but sent_total is not.
	if _, err := env.pool.Exec(ctx, `UPDATE webhook_logs SET created_at = now() - interval '2 days' WHERE webhook_id = $1`, wh.ID); err != nil {
		t.Fatal(err)
	}
	res, err := env.runner.RetentionCleanupNow(ctx)
	if err != nil {
		t.Fatalf("retention cleanup: %v", err)
	}
	if res.WebhookLogs < 1 {
		t.Fatalf("retention deleted %d webhook logs, want >= 1", res.WebhookLogs)
	}
	if n := len(env.webhookLogs(t, wh.ID)); n != 0 {
		t.Fatalf("logs after retention = %d", n)
	}
	if row := env.adminRow(t, wh.ID); row.SentCount != 1 {
		t.Fatalf("sent_total after retention = %d, want 1", row.SentCount)
	}
}
