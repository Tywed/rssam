package worker

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"rssam/internal/storage"
)

type memWebhookDelivery struct {
	mu sync.Mutex

	loadErr    error
	delCtx     storage.WebhookDeliveryContext
	incomplete int

	logs     map[int64]storage.WebhookLog
	stripped []int64
	removed  []int64
	read     []int64
}

func newMemWebhookDelivery(log storage.WebhookLog, ctx storage.WebhookDeliveryContext) *memWebhookDelivery {
	return &memWebhookDelivery{
		delCtx: ctx,
		logs:   map[int64]storage.WebhookLog{log.ID: log},
	}
}

func (m *memWebhookDelivery) LoadWebhookDeliveryContext(context.Context, int64) (storage.WebhookDeliveryContext, error) {
	if m.loadErr != nil {
		return storage.WebhookDeliveryContext{}, m.loadErr
	}
	return m.delCtx, nil
}

func (m *memWebhookDelivery) MarkWebhookLogSent(_ context.Context, logID int64, attempt int, statusCode int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.logs[logID]
	l.Status = "sent"
	l.Attempt = attempt
	code := statusCode
	l.LastStatusCode = &code
	l.NextRetryAt = nil
	m.logs[logID] = l
	return nil
}

func (m *memWebhookDelivery) MarkWebhookLogFailed(_ context.Context, logID int64, statusCode *int, errMsg string, _ string, attempt int, nextRetryAt *time.Time, dead bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.logs[logID]
	if dead {
		l.Status = "dead"
	} else {
		l.Status = "failed"
	}
	l.Attempt = attempt
	l.LastStatusCode = statusCode
	msg := errMsg
	l.LastError = &msg
	l.NextRetryAt = nextRetryAt
	m.logs[logID] = l
	return nil
}

func (m *memWebhookDelivery) StripEntryPayloadAfterWebhook(_ context.Context, entryID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stripped = append(m.stripped, entryID)
	return nil
}

func (m *memWebhookDelivery) CountIncompleteWebhookLogs(context.Context, int64, int64) (int, error) {
	return m.incomplete, nil
}

func (m *memWebhookDelivery) MarkEntryRemovedKeepPayload(_ context.Context, entryID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, entryID)
	return nil
}

func (m *memWebhookDelivery) MarkEntryReadIfActive(_ context.Context, entryID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.read = append(m.read, entryID)
	return nil
}

func (m *memWebhookDelivery) log(id int64) storage.WebhookLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.logs[id]
}

func processTestRunner(store *memWebhookDelivery, timeout time.Duration) *Runner {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Runner{
		WebhookDelivery: store,
		Cfg: Config{
			WebhookMaxAttempts: 10,
			WebhookTimeout:     timeout,
			WebhookRetryBase:   time.Second,
			WebhookRetryMax:    time.Hour,
		},
		Rand: rand.New(rand.NewSource(1)),
	}
}

func TestWebhookHTTPRetryable(t *testing.T) {
	if webhookHTTPRetryable(400) || webhookHTTPRetryable(404) {
		t.Fatal("4xx should not retry")
	}
	if !webhookHTTPRetryable(408) || !webhookHTTPRetryable(429) || !webhookHTTPRetryable(500) {
		t.Fatal("408/429/5xx should retry")
	}
}

func TestProcessWebhookLog_SentAppliesOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	log := storage.WebhookLog{ID: 1, EntryID: 42, Status: "pending", Attempt: 0}
	del := storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{
			Enabled:        true,
			URL:            srv.URL,
			Method:         http.MethodPost,
			OnSuccessEntry: storage.WebhookOnSuccessMarkRead,
		},
		Entry: storage.Entry{ID: 42, Title: "Hello"},
		Feed:  storage.WebhookFeed{ID: 7, Title: "Feed"},
	}
	store := newMemWebhookDelivery(log, del)
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)

	got := store.log(1)
	if got.Status != "sent" {
		t.Fatalf("status=%s", got.Status)
	}
	if got.Attempt != 1 {
		t.Fatalf("attempt=%d", got.Attempt)
	}
	if len(store.read) != 1 || store.read[0] != 42 {
		t.Fatalf("mark_read not applied: %v", store.read)
	}
}

func TestProcessWebhookLog_400DeadNoRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	log := storage.WebhookLog{ID: 2, EntryID: 9, Status: "pending", Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{Enabled: true, URL: srv.URL, Method: http.MethodPost},
		Entry:   storage.Entry{ID: 9},
	})
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)

	got := store.log(2)
	if got.Status != "dead" {
		t.Fatalf("status=%s", got.Status)
	}
	if got.NextRetryAt != nil {
		t.Fatalf("expected no retry, got %v", got.NextRetryAt)
	}
}

func TestProcessWebhookLog_429And500Retry(t *testing.T) {
	t.Parallel()
	for _, code := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()
			log := storage.WebhookLog{ID: int64(code), EntryID: 1, Status: "pending", Attempt: 0}
			store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
				Webhook: storage.Webhook{Enabled: true, URL: srv.URL, Method: http.MethodPost},
				Entry:   storage.Entry{ID: 1},
			})
			processTestRunner(store, 0).processWebhookLog(context.Background(), log)
			got := store.log(int64(code))
			if got.Status != "failed" {
				t.Fatalf("status=%s", got.Status)
			}
			if got.NextRetryAt == nil {
				t.Fatal("expected next_retry_at")
			}
			if got.Attempt != 1 {
				t.Fatalf("attempt=%d", got.Attempt)
			}
		})
	}
}

func TestProcessWebhookLog_DisabledParksWithoutBurningAttempt(t *testing.T) {
	log := storage.WebhookLog{ID: 3, EntryID: 1, Status: "pending", Attempt: 2}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{Enabled: false, URL: "http://127.0.0.1/unused", Method: http.MethodPost},
	})
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	got := store.log(3)
	if got.Status != "failed" {
		t.Fatalf("status=%s want failed", got.Status)
	}
	if got.Attempt != 2 {
		t.Fatalf("attempt=%d want 2", got.Attempt)
	}
	if got.NextRetryAt == nil {
		t.Fatal("expected park next_retry_at")
	}
}

func TestProcessWebhookLog_LoadNotFoundDead(t *testing.T) {
	log := storage.WebhookLog{ID: 4, EntryID: 1, Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{})
	store.loadErr = storage.ErrNotFound
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	got := store.log(4)
	if got.Status != "dead" {
		t.Fatalf("status=%s", got.Status)
	}
	if got.NextRetryAt != nil {
		t.Fatalf("retry=%v", got.NextRetryAt)
	}
}

func TestProcessWebhookLog_LoadTransientFailed(t *testing.T) {
	log := storage.WebhookLog{ID: 5, EntryID: 1, Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{})
	store.loadErr = errors.New("db down")
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	got := store.log(5)
	if got.Status != "failed" {
		t.Fatalf("status=%s", got.Status)
	}
	if got.NextRetryAt == nil {
		t.Fatal("expected retry")
	}
	if got.Attempt != 1 {
		t.Fatalf("attempt=%d", got.Attempt)
	}
}

func TestProcessWebhookLog_HashSkippedWhenOtherLogsIncomplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	log := storage.WebhookLog{ID: 6, EntryID: 99, Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{
			Enabled:        true,
			URL:            srv.URL,
			Method:         http.MethodPost,
			OnSuccessEntry: storage.WebhookOnSuccessHash,
		},
		Entry: storage.Entry{ID: 99},
	})
	store.incomplete = 1
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	if store.log(6).Status != "sent" {
		t.Fatalf("status=%s", store.log(6).Status)
	}
	if len(store.stripped) != 0 {
		t.Fatalf("hash should wait, stripped=%v", store.stripped)
	}
}

func TestProcessWebhookLog_HashAppliedWhenAlone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	log := storage.WebhookLog{ID: 7, EntryID: 99, Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{
			Enabled:        true,
			URL:            srv.URL,
			Method:         http.MethodPost,
			OnSuccessEntry: storage.WebhookOnSuccessHash,
		},
		Entry: storage.Entry{ID: 99},
	})
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	if len(store.stripped) != 1 {
		t.Fatalf("expected strip, got %v", store.stripped)
	}
}

func TestProcessWebhookLog_TelegramOK(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	cfg, _ := json.Marshal(storage.TelegramProviderConfig{
		APIBase:  srv.URL,
		BotToken: "tok",
		ChatID:   "42",
	})
	log := storage.WebhookLog{ID: 8, EntryID: 1, Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{
			Enabled:        true,
			Kind:           storage.WebhookKindTelegram,
			ProviderConfig: cfg,
		},
		Entry: storage.Entry{ID: 1, Title: "Hello", URL: "https://example.com/a"},
		Feed:  storage.WebhookFeed{Title: "Feed"},
	})
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	if store.log(8).Status != "sent" {
		t.Fatalf("status=%s err=%v", store.log(8).Status, store.log(8).LastError)
	}
	if !strings.Contains(gotPath, "sendMessage") {
		t.Fatalf("path=%s", gotPath)
	}
}

func TestProcessWebhookLog_TelegramOKFalseDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	defer srv.Close()
	cfg, _ := json.Marshal(storage.TelegramProviderConfig{
		APIBase:  srv.URL,
		BotToken: "tok",
		ChatID:   "42",
	})
	log := storage.WebhookLog{ID: 9, EntryID: 1, Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{Enabled: true, Kind: storage.WebhookKindTelegram, ProviderConfig: cfg},
		Entry:   storage.Entry{ID: 1, Title: "Hello"},
	})
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	got := store.log(9)
	if got.Status != "dead" {
		t.Fatalf("status=%s", got.Status)
	}
	if got.NextRetryAt != nil {
		t.Fatal("expected no retry")
	}
}

func TestProcessWebhookLog_MaxSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/channel/id/99/messages" {
			t.Errorf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":true,"message":{"id":1,"text":"x","time":1,"type":"text"}}`))
	}))
	defer srv.Close()
	cfg, _ := json.Marshal(storage.MaxProviderConfig{
		APIBase:  srv.URL,
		Target:   storage.MaxTargetChatID,
		TargetID: "99",
	})
	log := storage.WebhookLog{ID: 10, EntryID: 1, Attempt: 0}
	store := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{Enabled: true, Kind: storage.WebhookKindMax, ProviderConfig: cfg},
		Entry:   storage.Entry{ID: 1, Title: "Hello"},
	})
	processTestRunner(store, 0).processWebhookLog(context.Background(), log)
	if store.log(10).Status != "sent" {
		t.Fatalf("status=%s err=%v", store.log(10).Status, store.log(10).LastError)
	}
}
