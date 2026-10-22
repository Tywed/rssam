package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestRetryAfterOf(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	resp := func(h string) *http.Response {
		r := &http.Response{Header: http.Header{}}
		if h != "" {
			r.Header.Set("Retry-After", h)
		}
		return r
	}
	cases := []struct {
		header, body string
		want         time.Duration
	}{
		{"", `{"ok":false,"error_code":429,"parameters":{"retry_after":35}}`, 35 * time.Second},
		{"7", `{"ok":false,"error_code":429,"parameters":{"retry_after":35}}`, 35 * time.Second},
		{"7", ``, 7 * time.Second},
		{"0.5", ``, time.Second},
		{now.Add(90 * time.Second).Format(http.TimeFormat), ``, 90 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), ``, webhookFloodDefaultWait},
		{"garbage", `not json`, webhookFloodDefaultWait},
		{"", ``, webhookFloodDefaultWait},
		{"99999", ``, webhookFloodMaxWait},
		{"", `{"parameters":{"retry_after":0}}`, webhookFloodDefaultWait},
	}
	for _, c := range cases {
		if got := retryAfterOf(resp(c.header), c.body, now); got != c.want {
			t.Errorf("header=%q body=%q: got %v want %v", c.header, c.body, got, c.want)
		}
	}
}

func TestWebhookPauses(t *testing.T) {
	var p webhookPauses
	now := time.Now()
	if ids, _, ok := p.active(now); ids != nil || ok {
		t.Fatal("empty set must be inactive")
	}
	p.pause(1, now.Add(10*time.Second))
	p.pause(2, now.Add(3*time.Second))
	p.pause(1, now.Add(5*time.Second)) // a shorter pause never cuts a longer one
	ids, resume, ok := p.active(now)
	if len(ids) != 2 || !ok || !resume.Equal(now.Add(3*time.Second)) {
		t.Fatalf("ids=%v resume=%v ok=%v", ids, resume, ok)
	}
	ids, resume, ok = p.active(now.Add(4 * time.Second))
	if len(ids) != 1 || ids[0] != 1 || !resume.Equal(now.Add(10*time.Second)) || !ok {
		t.Fatalf("after 4s: ids=%v resume=%v", ids, resume)
	}
	if ids, _, ok := p.active(now.Add(time.Minute)); len(ids) != 0 || ok || len(p.until) != 0 {
		t.Fatalf("expired pauses must be forgotten: %v %v", ids, p.until)
	}
}

func TestProcessWebhookLog_429PausesWebhookWithoutAttempt(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 35","parameters":{"retry_after":35}}`))
	}))
	defer srv.Close()

	cfg, _ := json.Marshal(storage.TelegramProviderConfig{APIBase: srv.URL, BotToken: "t", ChatID: "7"})
	log := storage.WebhookLog{ID: 1, WebhookID: 77, EntryID: 42, Status: "failed", Attempt: 3}
	del := storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{ID: 77, Enabled: true, Kind: storage.WebhookKindTelegram, ProviderConfig: cfg},
		Entry:   storage.Entry{ID: 42, Title: "Hello"},
		Feed:    storage.WebhookFeed{ID: 7, Title: "Feed"},
	}
	store := newMemWebhookDelivery(log, del)
	r := processTestRunner(store, 0)
	before := time.Now()
	if !r.processWebhookLog(context.Background(), log) {
		t.Fatal("status must be stored")
	}
	got := store.log(1)
	if got.Status != "failed" || got.Attempt != 3 {
		t.Fatalf("a 429 is not an attempt: status=%s attempt=%d", got.Status, got.Attempt)
	}
	if got.LastStatusCode == nil || *got.LastStatusCode != 429 || got.NextRetryAt == nil {
		t.Fatalf("row=%+v", got)
	}
	if d := got.NextRetryAt.Sub(before); d < 34*time.Second || d > 36*time.Second {
		t.Fatalf("next retry in %v, want 35s", d)
	}
	ids, resume, ok := r.pausedHooks.active(time.Now())
	if !ok || len(ids) != 1 || ids[0] != 77 || !resume.Equal(*got.NextRetryAt) {
		t.Fatalf("webhook must be paused until the retry: ids=%v resume=%v row=%v", ids, resume, got.NextRetryAt)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d", hits.Load())
	}
}

func TestProcessWebhookLog_429HTTPRetryAfterHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	log := storage.WebhookLog{ID: 1, WebhookID: 5, EntryID: 42, Status: "pending"}
	del := storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{ID: 5, Enabled: true, URL: srv.URL, Method: http.MethodPost},
		Entry:   storage.Entry{ID: 42}, Feed: storage.WebhookFeed{ID: 7},
	}
	store := newMemWebhookDelivery(log, del)
	r := processTestRunner(store, 0)
	r.processWebhookLog(context.Background(), log)
	got := store.log(1)
	if got.Attempt != 0 || got.Status != "failed" || got.NextRetryAt == nil {
		t.Fatalf("row=%+v", got)
	}
	if d := time.Until(*got.NextRetryAt); d < time.Second || d > 2*time.Second {
		t.Fatalf("retry in %v, want ≈2s", d)
	}
	if ids, _, ok := r.pausedHooks.active(time.Now()); !ok || len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("paused=%v", ids)
	}
	if ids, _, ok := r.pausedHooks.active(time.Now().Add(3 * time.Second)); ok || len(ids) != 0 {
		t.Fatalf("pause must end with the retry: %v", ids)
	}
}
