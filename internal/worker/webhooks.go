package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"text/template"
	"time"

	"rssam/internal/metrics"
	"rssam/internal/storage"
)

const webhookResponseSnippetMaxBytes = 2048
const webhookDisabledPark = 60 * time.Second

const (
	webhookEventEntryMatched = "entry_matched"
	webhookEventNewEntry     = "new_entry"
)

type webhookEventPayload struct {
	EventVersion int                 `json:"event_version"`
	EventType    string              `json:"event_type"`
	Entry        storage.Entry       `json:"entry"`
	Feed         storage.WebhookFeed `json:"feed"`
	Filter       *storage.Filter     `json:"filter,omitempty"`
	MatchDetails json.RawMessage     `json:"match_details,omitempty"`
	SentAt       time.Time           `json:"sent_at"`
}

func (r *Runner) webhookDispatchLoop(ctx context.Context, out chan<- storage.WebhookLog) {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			logs, err := r.Store.ClaimDueWebhookLogs(ctx, r.Cfg.WebhookPoolSize)
			if err != nil {
				r.Log.Error("webhook dispatcher: claim due logs failed", "err", err)
				continue
			}
			if len(logs) == 0 {
				continue
			}
			for _, l := range logs {
				select {
				case out <- l:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (r *Runner) webhookWorkerLoop(ctx context.Context, in <-chan storage.WebhookLog) {
	for {
		select {
		case <-ctx.Done():
			return
		case l := <-in:
			r.processWebhookLog(ctx, l)
		}
	}
}

type webhookDeliveryStore interface {
	LoadWebhookDeliveryContext(ctx context.Context, logID int64) (storage.WebhookDeliveryContext, error)
	MarkWebhookLogSent(ctx context.Context, logID int64, attempt int, statusCode int, responseSnippet string) error
	MarkWebhookLogFailed(ctx context.Context, logID int64, statusCode *int, errMsg string, responseSnippet string, attempt int, nextRetryAt *time.Time, dead bool) error
	StripEntryPayloadAfterWebhook(ctx context.Context, entryID int64) error
	CountIncompleteWebhookLogs(ctx context.Context, entryID, excludeLogID int64) (int, error)
	MarkEntryRemovedKeepPayload(ctx context.Context, entryID int64) error
	MarkEntryReadIfActive(ctx context.Context, entryID int64) error
}

func (r *Runner) webhookDelivery() webhookDeliveryStore {
	if r.WebhookDelivery != nil {
		return r.WebhookDelivery
	}
	return r.Store
}

func (r *Runner) processWebhookLog(ctx context.Context, l storage.WebhookLog) {
	start := time.Now()
	attempt := l.Attempt + 1
	store := r.webhookDelivery()

	delCtx, err := store.LoadWebhookDeliveryContext(ctx, l.ID)
	if err != nil {
		if r.Log != nil {
			r.Log.Warn("webhook delivery: load context failed", "log_id", l.ID, "err", err)
		}
		if errors.Is(err, storage.ErrNotFound) {
			_ = store.MarkWebhookLogFailed(ctx, l.ID, nil, "delivery context not found", "", attempt, nil, true)
			metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
			return
		}
		r.recordWebhookFailure(ctx, store, l.ID, attempt, nil, err.Error(), "", true)
		return
	}
	if !delCtx.Webhook.Enabled {
		next := time.Now().UTC().Add(webhookDisabledPark)
		_ = store.MarkWebhookLogFailed(ctx, l.ID, nil, "webhook is disabled", "", l.Attempt, &next, false)
		return
	}

	payloadBytes, err := buildWebhookPayloadBytes(delCtx, time.Now().UTC())
	if err != nil {
		_ = store.MarkWebhookLogFailed(ctx, l.ID, nil, err.Error(), "", attempt, nil, true)
		metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
		return
	}

	kind, _ := storage.NormalizeWebhookKind(delCtx.Webhook.Kind)
	method := delCtx.Webhook.Method
	reqURL := delCtx.Webhook.URL
	bodyBytes := payloadBytes
	signHMAC := true

	if kind == storage.WebhookKindTelegram || kind == storage.WebhookKindMax {
		out, oerr := storage.BuildWebhookOutbound(delCtx.Webhook, delCtx.Feed, delCtx.Entry, delCtx.Filter)
		if oerr != nil {
			_ = store.MarkWebhookLogFailed(ctx, l.ID, nil, oerr.Error(), "", attempt, nil, true)
			metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
			return
		}
		method = out.Method
		reqURL = out.URL
		bodyBytes = out.Body
		signHMAC = false
	} else if strings.TrimSpace(delCtx.Webhook.BodyTemplate) != "" {
		b, tmplErr := renderBodyTemplate(delCtx.Webhook.BodyTemplate, delCtx, payloadBytes)
		if tmplErr != nil {
			_ = store.MarkWebhookLogFailed(ctx, l.ID, nil, tmplErr.Error(), "", attempt, nil, true)
			metrics.WebhookDeliveriesTotal.WithLabelValues("dead").Inc()
			return
		}
		bodyBytes = b
	}

	if r.SSRFGuard != nil {
		if err := r.SSRFGuard.ValidateURL(reqURL); err != nil {
			_ = store.MarkWebhookLogFailed(ctx, l.ID, nil, err.Error(), "", attempt, nil, true)
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

	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(reqCtx, method, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		r.recordWebhookFailure(ctx, store, l.ID, attempt, nil, err.Error(), "", true)
		return
	}

	if kind == storage.WebhookKindHTTP {
		setWebhookHeaders(req, delCtx.Webhook.Headers)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if signHMAC && strings.TrimSpace(delCtx.Webhook.Secret) != "" {
		req.Header.Set("X-RSSAM-Signature", "sha256="+signWebhookPayload(delCtx.Webhook.Secret, bodyBytes))
	}

	client := r.httpClient()
	resp, err := client.Do(req)
	dur := time.Since(start)
	metrics.WebhookDeliveryDuration.Observe(dur.Seconds())

	if err != nil {
		r.recordWebhookFailure(ctx, store, l.ID, attempt, nil, err.Error(), "", true)
		return
	}
	defer resp.Body.Close()

	snippet := readSnippet(resp.Body, webhookResponseSnippetMaxBytes)
	ok, failMsg := storage.ProviderDeliveryOK(kind, resp.StatusCode, snippet)
	if ok {
		_ = store.MarkWebhookLogSent(ctx, l.ID, attempt, resp.StatusCode, snippet)
		r.applyOnSuccessEntry(ctx, store, l, delCtx)
		metrics.WebhookDeliveriesTotal.WithLabelValues("sent").Inc()
		return
	}

	code := resp.StatusCode
	retryable := webhookHTTPRetryable(resp.StatusCode)
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		retryable = false
	}
	r.recordWebhookFailure(ctx, store, l.ID, attempt, &code, failMsg, snippet, retryable)
}

func (r *Runner) recordWebhookFailure(ctx context.Context, store webhookDeliveryStore, logID int64, attempt int, statusCode *int, errMsg, snippet string, retryable bool) {
	var next *time.Time
	dead := true
	if retryable {
		next, dead = r.webhookRetrySchedule(attempt)
	}
	_ = store.MarkWebhookLogFailed(ctx, logID, statusCode, errMsg, snippet, attempt, next, dead)
	metrics.WebhookDeliveriesTotal.WithLabelValues(statusLabel(dead)).Inc()
	if !dead {
		metrics.WebhookRetriesTotal.Inc()
	}
}

func webhookHTTPRetryable(statusCode int) bool {
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests {
		return true
	}
	return statusCode >= 500
}

func (r *Runner) applyOnSuccessEntry(ctx context.Context, store webhookDeliveryStore, l storage.WebhookLog, delCtx storage.WebhookDeliveryContext) {
	action, err := storage.NormalizeWebhookOnSuccess(delCtx.Webhook.OnSuccessEntry)
	if err != nil {
		action = storage.WebhookOnSuccessNone
	}
	switch action {
	case storage.WebhookOnSuccessHash, storage.WebhookOnSuccessDelete:
		n, cerr := store.CountIncompleteWebhookLogs(ctx, l.EntryID, l.ID)
		if cerr != nil {
			if r.Log != nil {
				r.Log.Error("webhook on_success: count incomplete logs", "entry_id", l.EntryID, "err", cerr)
			}
			break
		}
		if n > 0 {
			break
		}
		if action == storage.WebhookOnSuccessHash {
			if err := store.StripEntryPayloadAfterWebhook(ctx, l.EntryID); err != nil && r.Log != nil {
				r.Log.Error("webhook on_success: hash entry", "entry_id", l.EntryID, "err", err)
			}
		} else if err := store.MarkEntryRemovedKeepPayload(ctx, l.EntryID); err != nil && r.Log != nil {
			r.Log.Error("webhook on_success: delete entry", "entry_id", l.EntryID, "err", err)
		}
	case storage.WebhookOnSuccessMarkRead:
		if err := store.MarkEntryReadIfActive(ctx, l.EntryID); err != nil && r.Log != nil {
			r.Log.Error("webhook on_success: mark read", "entry_id", l.EntryID, "err", err)
		}
	}
	if r.Cfg.DedupOnlyStorage {
		if err := store.StripEntryPayloadAfterWebhook(ctx, l.EntryID); err != nil && r.Log != nil {
			r.Log.Error("webhook on_success: dedup strip", "entry_id", l.EntryID, "err", err)
		}
	}
}

func (r *Runner) httpClient() *http.Client {
	if r.WebhookHTTPClient != nil {
		return r.WebhookHTTPClient
	}
	timeout := r.Cfg.WebhookTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func statusLabel(dead bool) string {
	if dead {
		return "dead"
	}
	return "failed"
}

func buildWebhookPayloadBytes(ctx storage.WebhookDeliveryContext, sentAt time.Time) ([]byte, error) {
	var details json.RawMessage
	if len(ctx.MatchDetails) > 0 {
		details = ctx.MatchDetails
	}
	p := webhookEventPayload{
		EventVersion: 1,
		EventType:    webhookEventType(ctx),
		Entry:        ctx.Entry,
		Feed:         ctx.Feed,
		Filter:       ctx.Filter,
		MatchDetails: details,
		SentAt:       sentAt,
	}
	return json.Marshal(p)
}

func webhookEventType(ctx storage.WebhookDeliveryContext) string {
	if ctx.Filter != nil {
		return webhookEventEntryMatched
	}
	return webhookEventNewEntry
}

func setWebhookHeaders(req *http.Request, raw []byte) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	for k, v := range m {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		req.Header.Set(k, v)
	}
}

func renderBodyTemplate(bodyTemplate string, ctx storage.WebhookDeliveryContext, payloadJSON []byte) ([]byte, error) {
	tmpl, err := template.New("webhook_body").Option("missingkey=error").Parse(bodyTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid body_template: %w", err)
	}
	data := map[string]any{
		"payload": string(payloadJSON),
		"entry":   ctx.Entry,
		"feed":    ctx.Feed,
		"filter":  ctx.Filter,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render body_template: %w", err)
	}
	return buf.Bytes(), nil
}

func signWebhookPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func readSnippet(r io.Reader, limit int) string {
	if limit <= 0 {
		limit = webhookResponseSnippetMaxBytes
	}
	b, _ := io.ReadAll(io.LimitReader(r, int64(limit)))
	return string(b)
}

func (r *Runner) webhookRetrySchedule(attempt int) (*time.Time, bool) {
	maxAttempts := r.Cfg.WebhookMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	if attempt >= maxAttempts {
		return nil, true
	}

	base := r.Cfg.WebhookRetryBase
	if base <= 0 {
		base = 5 * time.Second
	}
	max := r.Cfg.WebhookRetryMax
	if max <= 0 {
		max = time.Hour
	}
	delay := webhookBackoffWithJitter(attempt, base, max, r.rand())
	next := time.Now().UTC().Add(delay)
	return &next, false
}

func webhookBackoffWithJitter(attempt int, base, max time.Duration, rnd *rand.Rand) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}
	if base <= 0 {
		base = time.Second
	}
	d := base
	for i := 1; i < attempt; i++ {
		if d > max/2 && max > 0 {
			d = max
			break
		}
		d *= 2
	}
	if max > 0 && d > max {
		d = max
	}
	if rnd == nil {
		rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	// Jitter in [0, d/2).
	jitter := time.Duration(rnd.Int63n(int64(d / 2)))
	return d + jitter
}

func (r *Runner) rand() *rand.Rand {
	if r.Rand != nil {
		return r.Rand
	}
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

var _ webhookDeliveryStore = (*storage.PostgresStore)(nil)
