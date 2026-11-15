package worker

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/storage"
)

func TestRunLoop_RestartsAfterPanicAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var passes atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(ctx, slog.Default(), "test", func() {
			if passes.Add(1) == 1 {
				panic("first pass")
			}
			<-ctx.Done()
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for passes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if passes.Load() < 2 {
		t.Fatalf("loop not restarted after panic: passes=%d", passes.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runLoop did not return after cancel")
	}
}

type panickingDelivery struct{ *memWebhookDelivery }

func (p panickingDelivery) LoadWebhookDeliveryContext(context.Context, int64) (storage.WebhookDeliveryContext, error) {
	panic("template bug")
}

// A panic inside a delivery becomes a retryable failure of that row and
// the worker reports the status as stored.
func TestProcessWebhookLog_PanicBecomesFailure(t *testing.T) {
	log := storage.WebhookLog{ID: 1, WebhookID: 5, EntryID: 42, Status: "pending"}
	mem := newMemWebhookDelivery(log, storage.WebhookDeliveryContext{})
	r := processTestRunner(mem, 0)
	r.WebhookDelivery = panickingDelivery{mem}
	r.Log = slog.Default()
	if !r.processWebhookLog(context.Background(), log) {
		t.Fatal("status must be stored")
	}
	got := mem.log(1)
	if got.Status != "failed" || got.Attempt != 1 || got.NextRetryAt == nil || got.LastError == nil || *got.LastError != "panic: template bug" {
		t.Fatalf("row=%+v", got)
	}
}

// When the drain budget runs out in the middle of a request, the request
// is aborted, but the row still gets its failed status and retry time: the
// write runs on a short grace context.
func TestProcessWebhookLog_StatusWrittenAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cancel()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	log := storage.WebhookLog{ID: 1, WebhookID: 5, EntryID: 42, Status: "pending"}
	del := storage.WebhookDeliveryContext{
		Webhook: storage.Webhook{ID: 5, Enabled: true, URL: srv.URL, Method: http.MethodPost},
		Entry:   storage.Entry{ID: 42}, Feed: storage.WebhookFeed{ID: 7},
	}
	mem := newMemWebhookDelivery(log, del)
	mem.failOnCancelled = true
	r := processTestRunner(mem, 0)
	if !r.processWebhookLog(ctx, log) {
		t.Fatal("status must be stored")
	}
	if got := mem.log(1); got.Status != "failed" || got.NextRetryAt == nil || got.Attempt != 1 {
		t.Fatalf("row=%+v", got)
	}
}
