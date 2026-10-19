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
	"math/rand/v2"
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

// webhookDispatchLoop claims due deliveries, then sleeps until the earliest
// next_retry_at, a webhooks-due notification, or SchedulerTick at most.
func (r *Runner) webhookDispatchLoop(ctx context.Context, out chan<- storage.WebhookLog) {
	for ctx.Err() == nil {
		if r.Paused() {
			idleWait(ctx, r.wake.webhooks, time.Time{}, false, r.Cfg.SchedulerTick)
			continue
		}
		exclude := r.inflightHooks.exclude(time.Now())
		logs, err := r.Store.ClaimDueWebhookLogs(ctx, r.Cfg.WebhookPoolSize, exclude)
		if err != nil {
			r.Log.Error("webhook dispatcher: claim due logs failed", "err", err)
			idleWait(ctx, r.wake.webhooks, time.Time{}, false, r.Cfg.SchedulerTick)
			continue
		}
		if len(logs) > 0 {
			claimedAt := time.Now()
			for _, l := range r.groupDigestLogs(ctx, logs, exclude) {
				r.inflightHooks.start(l.ID, claimedAt)
				for _, id := range l.Peers {
					r.inflightHooks.start(id, claimedAt)
				}
				select {
				case out <- l:
				case <-ctx.Done():
					return
				}
			}
			continue
		}
		next, known, err := r.Store.NextWebhookRetryAt(ctx, exclude)
		if err != nil {
			r.Log.Error("webhook dispatcher: next retry failed", "err", err)
		}
		idleWait(ctx, r.wake.webhooks, next, known, r.Cfg.SchedulerTick)
	}
}

func (r *Runner) webhookWorkerLoop(ctx context.Context, in <-chan storage.WebhookLog) {
	for {
		select {
		case <-ctx.Done():
			return
		case l := <-in:
			var stored bool
			if l.DigestMinutes > 0 {
				stored = r.processWebhookDigest(ctx, l)
			} else {
				stored = r.processWebhookLog(ctx, l)
			}
			// A row whose status is written is free at once (its retry time
			// is in the row); one whose write failed stays excluded for the
			// grace period so the endpoint is not hammered meanwhile.
			ids := append([]int64{l.ID}, l.Peers...)
			now := time.Now()
			for _, id := range ids {
				if stored {
					r.inflightHooks.forget(id)
				} else {
					r.inflightHooks.done(id, now)
				}
			}
		}
	}
}

type webhookDeliveryStore interface {
	LoadWebhookDeliveryContext(ctx context.Context, logID int64) (storage.WebhookDeliveryContext, error)
	MarkWebhookLogSent(ctx context.Context, logID int64, attempt int, statusCode int, responseSnippet string) error
	MarkWebhookLogFailed(ctx context.Context, logID int64, statusCode *int, errMsg string, responseSnippet string, attempt int, nextRetryAt *time.Time, dead bool) error
	StripEntryPayloadAfterWebhook(ctx context.Context, entryID int64) error
	EntryWebhookOutcome(ctx context.Context, entryID, excludeLogID int64) (incomplete int, action string, err error)
	DeleteRelayEntryAfterWebhook(ctx context.Context, entryID, excludeLogID int64) (bool, error)
	MarkEntryRemovedKeepPayload(ctx context.Context, userID, entryID int64) error
	MarkEntryReadIfActive(ctx context.Context, userID, entryID int64) error
}

func (r *Runner) webhookDelivery() webhookDeliveryStore {
	if r.WebhookDelivery != nil {
		return r.WebhookDelivery
	}
	return r.Store
}

// processWebhookLog delivers one row and reports whether its final status
// reached the database.
func (r *Runner) processWebhookLog(ctx context.Context, l storage.WebhookLog) bool {
	store := &markedDelivery{webhookDeliveryStore: r.webhookDelivery()}
	r.deliverWebhookLog(ctx, store, l)
	return store.stored
}

// markedDelivery remembers whether the last status write succeeded.
type markedDelivery struct {
	webhookDeliveryStore
	stored bool
}

func (m *markedDelivery) MarkWebhookLogSent(ctx context.Context, logID int64, attempt int, statusCode int, responseSnippet string) error {
	err := m.webhookDeliveryStore.MarkWebhookLogSent(ctx, logID, attempt, statusCode, responseSnippet)
	m.stored = err == nil
	return err
}

func (m *markedDelivery) MarkWebhookLogFailed(ctx context.Context, logID int64, statusCode *int, errMsg string, responseSnippet string, attempt int, nextRetryAt *time.Time, dead bool) error {
	err := m.webhookDeliveryStore.MarkWebhookLogFailed(ctx, logID, statusCode, errMsg, responseSnippet, attempt, nextRetryAt, dead)
	m.stored = err == nil
	return err
}

func (r *Runner) deliverWebhookLog(ctx context.Context, store webhookDeliveryStore, l storage.WebhookLog) {
	start := time.Now()
	attempt := l.Attempt + 1

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
		// The body of a successful delivery (Telegram echoes the whole
		// message back) is not kept: the log row only needs it for diagnosis.
		_ = store.MarkWebhookLogSent(ctx, l.ID, attempt, resp.StatusCode, "")
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
	if r.Cfg.DedupOnlyStorage || delCtx.Feed.StoreMode == storage.StoreModeRelay {
		// A relay entry exists only to be delivered: nobody reads it, so
		// per-user actions do not apply and the row goes as soon as the
		// last delivery is done.
		if _, err := store.DeleteRelayEntryAfterWebhook(ctx, l.EntryID, l.ID); err != nil && r.Log != nil {
			r.Log.Error("webhook on_success: delete relay entry", "entry_id", l.EntryID, "err", err)
		}
		return
	}
	thisAction, err := storage.NormalizeWebhookOnSuccess(delCtx.Webhook.OnSuccessEntry)
	if err != nil {
		thisAction = storage.WebhookOnSuccessNone
	}
	if thisAction == storage.WebhookOnSuccessMarkRead {
		if err := store.MarkEntryReadIfActive(ctx, delCtx.Webhook.UserID, l.EntryID); err != nil && r.Log != nil {
			r.Log.Error("webhook on_success: mark read", "entry_id", l.EntryID, "err", err)
		}
	}

	incomplete, resolved, err := store.EntryWebhookOutcome(ctx, l.EntryID, l.ID)
	if err != nil {
		if r.Log != nil {
			r.Log.Error("webhook on_success: entry outcome", "entry_id", l.EntryID, "err", err)
		}
		return
	}
	if incomplete > 0 {
		return
	}

	switch storage.MergeOnSuccessActions([]string{thisAction, resolved}) {
	case storage.WebhookOnSuccessHash:
		if err := store.StripEntryPayloadAfterWebhook(ctx, l.EntryID); err != nil && r.Log != nil {
			r.Log.Error("webhook on_success: hash entry", "entry_id", l.EntryID, "err", err)
		}
	case storage.WebhookOnSuccessDelete:
		if err := store.MarkEntryRemovedKeepPayload(ctx, delCtx.Webhook.UserID, l.EntryID); err != nil && r.Log != nil {
			r.Log.Error("webhook on_success: delete entry", "entry_id", l.EntryID, "err", err)
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

// reservedWebhookHeaders cannot be overridden by user-supplied webhook
// headers. net/http already ignores Host/Content-Length/Transfer-Encoding
// from the header map, but honours Connection/Upgrade/Keep-Alive; and the
// signature header must only ever be produced by the worker itself.
var reservedWebhookHeaders = map[string]struct{}{
	"Host":              {},
	"Content-Length":    {},
	"Transfer-Encoding": {},
	"Connection":        {},
	"Keep-Alive":        {},
	"Upgrade":           {},
	"Te":                {},
	"Trailer":           {},
	"Proxy-Connection":  {},
	"X-Rssam-Signature": {},
}

// isReservedWebhookHeader reports whether a user-supplied header name may not
// be set on outgoing webhook requests. Names are canonicalised first.
func isReservedWebhookHeader(name string) bool {
	_, reserved := reservedWebhookHeaders[http.CanonicalHeaderKey(name)]
	return reserved
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
		if k == "" || isReservedWebhookHeader(k) {
			continue
		}
		// A CR/LF in a value would otherwise be rejected by net/http at send
		// time with an opaque error; skip the header instead of failing delivery.
		if strings.ContainsAny(v, "\r\n") {
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
	delay := webhookBackoffWithJitter(attempt, base, max)
	next := time.Now().UTC().Add(delay)
	return &next, false
}

func webhookBackoffWithJitter(attempt int, base, max time.Duration) time.Duration {
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
	half := int64(d / 2)
	if half <= 0 {
		return d
	}
	jitter := time.Duration(rand.Int64N(half))
	return d + jitter
}

var _ webhookDeliveryStore = (*storage.PostgresStore)(nil)
