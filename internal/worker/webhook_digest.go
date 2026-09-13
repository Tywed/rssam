package worker

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"rssam/internal/metrics"
	"rssam/internal/storage"
)

// webhookDigestMaxItems bounds one digest message; the rest goes out with the
// next window.
const webhookDigestMaxItems = 200

type webhookDigestStore interface {
	ClaimWebhookDigestPeers(ctx context.Context, webhookID int64, excludeIDs []int64, limit int) ([]storage.WebhookLog, error)
	LoadWebhookDigestContext(ctx context.Context, webhookID int64, logIDs []int64) (storage.Webhook, []storage.WebhookDigestItem, error)
	MarkWebhookLogsSent(ctx context.Context, logIDs []int64, statusCode int, responseSnippet string) error
	MarkWebhookLogsFailed(ctx context.Context, logIDs []int64, statusCode *int, errMsg string, responseSnippet string, nextRetryAt *time.Time, dead bool, countAttempt bool) error
}

func (r *Runner) webhookDigest() webhookDigestStore {
	if r.WebhookDigest != nil {
		return r.WebhookDigest
	}
	return r.Store
}

// groupDigestLogs collapses the claimed rows of each digest webhook into one
// carrier row (peer ids in Peers) and pulls in the webhook's other due rows so
// the whole window leaves in a single message.
func (r *Runner) groupDigestLogs(ctx context.Context, logs []storage.WebhookLog) []storage.WebhookLog {
	out := make([]storage.WebhookLog, 0, len(logs))
	carriers := map[int64]int{}
	for _, l := range logs {
		if l.DigestMinutes <= 0 {
			out = append(out, l)
			continue
		}
		if i, ok := carriers[l.WebhookID]; ok {
			out[i].Peers = append(out[i].Peers, l.ID)
			continue
		}
		l.Peers = []int64{l.ID}
		carriers[l.WebhookID] = len(out)
		out = append(out, l)
	}
	if len(carriers) == 0 {
		return out
	}
	store := r.webhookDigest()
	for _, i := range carriers {
		c := &out[i]
		more, err := store.ClaimWebhookDigestPeers(ctx, c.WebhookID, c.Peers, webhookDigestMaxItems-len(c.Peers))
		if err != nil {
			r.Log.Warn("webhook digest: claim peers failed", "webhook_id", c.WebhookID, "err", err)
			continue
		}
		for _, m := range more {
			c.Peers = append(c.Peers, m.ID)
		}
	}
	return out
}

func (r *Runner) processWebhookDigest(ctx context.Context, l storage.WebhookLog) {
	start := time.Now()
	store := r.webhookDigest()
	ids := l.Peers
	if len(ids) == 0 {
		ids = []int64{l.ID}
	}
	attempt := l.Attempt + 1

	wh, items, err := store.LoadWebhookDigestContext(ctx, l.WebhookID, ids)
	if err != nil {
		r.digestFailure(ctx, store, ids, attempt, nil, err.Error(), "", true)
		return
	}
	if !wh.Enabled {
		next := time.Now().UTC().Add(webhookDisabledPark)
		_ = store.MarkWebhookLogsFailed(ctx, ids, nil, "webhook is disabled", "", &next, false, false)
		return
	}
	if len(items) == 0 {
		_ = store.MarkWebhookLogsFailed(ctx, ids, nil, "entries not found", "", nil, true, true)
		metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
		return
	}

	out, err := storage.BuildWebhookDigestOutbound(wh, items, time.Now().UTC())
	if err != nil {
		_ = store.MarkWebhookLogsFailed(ctx, ids, nil, err.Error(), "", nil, true, true)
		metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
		return
	}
	if r.SSRFGuard != nil {
		if err := r.SSRFGuard.ValidateURL(out.URL); err != nil {
			_ = store.MarkWebhookLogsFailed(ctx, ids, nil, err.Error(), "", nil, true, true)
			metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
			return
		}
	}

	timeout := r.Cfg.WebhookTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, out.Method, out.URL, bytes.NewReader(out.Body))
	if err != nil {
		r.digestFailure(ctx, store, ids, attempt, nil, err.Error(), "", true)
		return
	}
	if out.Kind == storage.WebhookKindHTTP {
		setWebhookHeaders(req, wh.Headers)
		if strings.TrimSpace(wh.Secret) != "" {
			req.Header.Set("X-RSSAM-Signature", "sha256="+signWebhookPayload(wh.Secret, out.Body))
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.httpClient().Do(req)
	metrics.WebhookDeliveryDuration.Observe(time.Since(start).Seconds())
	if err != nil {
		r.digestFailure(ctx, store, ids, attempt, nil, err.Error(), "", true)
		return
	}
	defer resp.Body.Close()

	snippet := readSnippet(resp.Body, webhookResponseSnippetMaxBytes)
	ok, failMsg := storage.ProviderDeliveryOK(out.Kind, resp.StatusCode, snippet)
	if ok {
		_ = store.MarkWebhookLogsSent(ctx, ids, resp.StatusCode, snippet)
		r.applyDigestOnSuccess(ctx, wh, items)
		metrics.WebhookDeliveriesTotal.WithLabelValues("sent").Inc()
		return
	}
	code := resp.StatusCode
	retryable := webhookHTTPRetryable(code) && (code < 200 || code > 299)
	r.digestFailure(ctx, store, ids, attempt, &code, failMsg, snippet, retryable)
}

func (r *Runner) digestFailure(ctx context.Context, store webhookDigestStore, ids []int64, attempt int, statusCode *int, errMsg, snippet string, retryable bool) {
	var next *time.Time
	dead := true
	if retryable {
		next, dead = r.webhookRetrySchedule(attempt)
	}
	_ = store.MarkWebhookLogsFailed(ctx, ids, statusCode, errMsg, snippet, next, dead, true)
	metrics.WebhookDeliveriesTotal.WithLabelValues(statusLabel(dead)).Inc()
	if !dead {
		metrics.WebhookRetriesTotal.Inc()
	}
}

// applyDigestOnSuccess runs the per-entry on_success handling for every
// entry of the batch, the same way a single delivery does.
func (r *Runner) applyDigestOnSuccess(ctx context.Context, wh storage.Webhook, items []storage.WebhookDigestItem) {
	delivery := r.webhookDelivery()
	delCtx := storage.WebhookDeliveryContext{Webhook: wh}
	for _, it := range items {
		r.applyOnSuccessEntry(ctx, delivery, storage.WebhookLog{ID: it.LogID, WebhookID: wh.ID, EntryID: it.Entry.ID}, delCtx)
	}
}
