package httpserver

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
	"net/http"
	"strings"
	"time"

	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type webhookDTO struct {
	ID             int64                           `json:"id"`
	UserID         int64                           `json:"user_id"`
	FilterID       *int64                          `json:"filter_id,omitempty"`
	Name           string                          `json:"name"`
	Kind           string                          `json:"kind"`
	URL            string                          `json:"url"`
	Method         string                          `json:"method"`
	Headers        json.RawMessage                 `json:"headers"`
	BodyTemplate   string                          `json:"body_template"`
	Enabled        bool                            `json:"enabled"`
	OnSuccessEntry string                          `json:"on_success_entry"`
	Telegram       *storage.TelegramProviderConfig `json:"telegram,omitempty"`
	Max            *storage.MaxProviderConfig      `json:"max,omitempty"`
	CreatedAt      time.Time                       `json:"created_at"`
	UpdatedAt      time.Time                       `json:"updated_at"`
}

type webhookWriteRequest struct {
	FilterID       *int64                          `json:"filter_id"`
	Name           *string                         `json:"name"`
	Kind           *string                         `json:"kind"`
	URL            string                          `json:"url"`
	Method         *string                         `json:"method"`
	Headers        json.RawMessage                 `json:"headers"`
	BodyTemplate   *string                         `json:"body_template"`
	Secret         *string                         `json:"secret"`
	Enabled        *bool                           `json:"enabled"`
	OnSuccessEntry *string                         `json:"on_success_entry"`
	Telegram       *storage.TelegramProviderConfig `json:"telegram"`
	Max            *storage.MaxProviderConfig      `json:"max"`
}

func toWebhookDTO(w storage.Webhook) webhookDTO {
	headers := w.Headers
	if len(headers) == 0 {
		headers = []byte(`{}`)
	}
	kind, _ := storage.NormalizeWebhookKind(w.Kind)
	dto := webhookDTO{
		ID:             w.ID,
		UserID:         w.UserID,
		FilterID:       w.FilterID,
		Name:           w.Name,
		Kind:           kind,
		URL:            w.URL,
		Method:         w.Method,
		Headers:        headers,
		BodyTemplate:   w.BodyTemplate,
		Enabled:        w.Enabled,
		OnSuccessEntry: w.OnSuccessEntry,
		CreatedAt:      w.CreatedAt,
		UpdatedAt:      w.UpdatedAt,
	}
	switch kind {
	case storage.WebhookKindTelegram:
		var c storage.TelegramProviderConfig
		_ = json.Unmarshal(storage.MaskTelegramConfig(w.ProviderConfig), &c)
		dto.Telegram = &c
	case storage.WebhookKindMax:
		c, err := storage.ParseMaxProviderConfig(w.ProviderConfig)
		if err == nil {
			dto.Max = &c
		}
	}
	return dto
}

func providerConfigFromWrite(req webhookWriteRequest) (kind string, cfg []byte, err error) {
	if req.Kind != nil {
		kind = *req.Kind
	}
	kind, err = storage.NormalizeWebhookKind(kind)
	if err != nil {
		return "", nil, err
	}
	switch kind {
	case storage.WebhookKindTelegram:
		if req.Telegram == nil {
			return "", nil, errors.New("telegram config is required")
		}
		b, err := json.Marshal(req.Telegram)
		return kind, b, err
	case storage.WebhookKindMax:
		if req.Max == nil {
			return "", nil, errors.New("max config is required")
		}
		b, err := json.Marshal(req.Max)
		return kind, b, err
	default:
		return kind, []byte(`{}`), nil
	}
}

func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.webhooks == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "webhook storage is not configured")
		}
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, total, err := s.webhooks.ListWebhooks(r.Context(), p.UserID, limit, offset)
	if err != nil {
		s.log.Error("list webhooks failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]webhookDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toWebhookDTO(row))
	}
	writeJSON(w, http.StatusOK, listResponse[[]webhookDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.webhooks == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "webhook storage is not configured")
		}
		return
	}
	var req webhookWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind, cfg, err := providerConfigFromWrite(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	url := strings.TrimSpace(req.URL)
	if kind == storage.WebhookKindHTTP {
		url, err = validateWebhookURL(req.URL, s.ssrfGuard)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	method := "POST"
	if req.Method != nil && strings.TrimSpace(*req.Method) != "" {
		method = strings.TrimSpace(*req.Method)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	bodyTemplate := ""
	if req.BodyTemplate != nil {
		bodyTemplate = *req.BodyTemplate
	}
	onSuccess := ""
	if req.OnSuccessEntry != nil {
		onSuccess = *req.OnSuccessEntry
	}
	secret := ""
	if req.Secret != nil {
		secret = *req.Secret
	}
	name := ""
	if req.Name != nil {
		name = *req.Name
	}
	headers := req.Headers
	if len(bytes.TrimSpace(headers)) == 0 {
		headers = []byte(`{}`)
	}
	wh, err := s.webhooks.CreateWebhook(r.Context(), storage.CreateWebhookParams{
		UserID:         p.UserID,
		FilterID:       req.FilterID,
		Name:           name,
		URL:            url,
		Method:         method,
		Headers:        headers,
		BodyTemplate:   bodyTemplate,
		Secret:         secret,
		Enabled:        enabled,
		OnSuccessEntry: onSuccess,
		Kind:           kind,
		ProviderConfig: cfg,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[webhookDTO]{Data: toWebhookDTO(wh), Total: 1})
}

func (s *Server) handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.webhooks == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "webhook storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	wh, err := s.webhooks.GetWebhook(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		s.log.Error("get webhook failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[webhookDTO]{Data: toWebhookDTO(wh), Total: 1})
}

func (s *Server) handleUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.webhooks == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "webhook storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req webhookWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind, cfg, err := providerConfigFromWrite(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	url := strings.TrimSpace(req.URL)
	if kind == storage.WebhookKindHTTP {
		url, err = validateWebhookURL(req.URL, s.ssrfGuard)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	method := "POST"
	if req.Method != nil && strings.TrimSpace(*req.Method) != "" {
		method = strings.TrimSpace(*req.Method)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	bodyTemplate := ""
	if req.BodyTemplate != nil {
		bodyTemplate = *req.BodyTemplate
	}
	onSuccess := ""
	if req.OnSuccessEntry != nil {
		onSuccess = *req.OnSuccessEntry
	}
	headers := req.Headers
	if len(bytes.TrimSpace(headers)) == 0 {
		headers = []byte(`{}`)
	}
	wh, err := s.webhooks.UpdateWebhook(r.Context(), storage.UpdateWebhookParams{
		ID:             id,
		UserID:         p.UserID,
		FilterID:       req.FilterID,
		Name:           req.Name,
		URL:            url,
		Method:         method,
		Headers:        headers,
		BodyTemplate:   bodyTemplate,
		Secret:         req.Secret, // nil => keep
		Enabled:        enabled,
		OnSuccessEntry: onSuccess,
		Kind:           kind,
		ProviderConfig: cfg,
	})
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listResponse[webhookDTO]{Data: toWebhookDTO(wh), Total: 1})
}

func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.webhooks == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "webhook storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.webhooks.DeleteWebhook(r.Context(), p.UserID, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		s.log.Error("delete webhook failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

type webhookTestResult struct {
	OK              bool   `json:"ok"`
	StatusCode      int    `json:"status_code"`
	ResponseSnippet string `json:"response_snippet,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (s *Server) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.webhooks == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "webhook storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.runWebhookTest(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "webhook not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	code := http.StatusOK
	if !res.OK {
		code = http.StatusBadGateway
	}
	writeJSON(w, code, listResponse[webhookTestResult]{Data: res, Total: 1})
}

func (s *Server) runWebhookTest(ctx context.Context, userID, webhookID int64) (webhookTestResult, error) {
	if s.webhooks == nil {
		return webhookTestResult{}, errors.New("webhook storage is not configured")
	}
	wh, err := s.webhooks.GetWebhook(ctx, userID, webhookID)
	if err != nil {
		return webhookTestResult{}, err
	}

	var f *storage.Filter
	if wh.FilterID != nil && s.filters != nil {
		ff, ferr := s.filters.GetFilter(ctx, userID, *wh.FilterID)
		if ferr == nil {
			f = &ff
		}
	}
	feed := storage.WebhookFeed{Title: "test feed"}
	entry := storage.Entry{Title: "test entry", URL: "https://example.com/test"}
	kind, _ := storage.NormalizeWebhookKind(wh.Kind)

	out, err := storage.BuildWebhookOutbound(wh, feed, entry, f)
	if err != nil {
		return webhookTestResult{}, err
	}
	method := out.Method
	if method == "" {
		method = http.MethodPost
	}
	reqURL := out.URL
	body := out.Body

	if s.ssrfGuard != nil {
		if err := s.ssrfGuard.ValidateURL(reqURL); err != nil {
			return webhookTestResult{}, err
		}
	}

	client := s.webhookHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	timeout := client.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req2, err := http.NewRequestWithContext(timeoutCtx, method, reqURL, bytes.NewReader(body))
	if err != nil {
		return webhookTestResult{}, err
	}
	if kind == storage.WebhookKindHTTP {
		setHeadersFromJSON(req2, wh.Headers)
	}
	if req2.Header.Get("Content-Type") == "" {
		req2.Header.Set("Content-Type", "application/json")
	}
	if kind == storage.WebhookKindHTTP && strings.TrimSpace(wh.Secret) != "" {
		req2.Header.Set("X-RSSAM-Signature", "sha256="+signHMACSHA256(wh.Secret, body))
	}

	resp, err := client.Do(req2)
	if err != nil {
		return webhookTestResult{Error: err.Error()}, nil
	}
	defer resp.Body.Close()
	snippetB, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	snippet := string(snippetB)
	ok, failMsg := storage.ProviderDeliveryOK(kind, resp.StatusCode, snippet)
	res := webhookTestResult{
		OK:              ok,
		StatusCode:      resp.StatusCode,
		ResponseSnippet: snippet,
	}
	if !ok {
		res.Error = failMsg
	}
	return res, nil
}

func formatWebhookTestMessage(res webhookTestResult) string {
	if res.OK {
		return "Тестовое сообщение отправлено. Журнал доставки не пишется."
	}
	msg := strings.TrimSpace(res.Error)
	if msg == "" {
		if res.StatusCode > 0 {
			msg = fmt.Sprintf("HTTP %d", res.StatusCode)
		} else {
			msg = "тест не выполнен"
		}
	}
	snip := strings.TrimSpace(res.ResponseSnippet)
	if snip != "" && snip != msg {
		msg = msg + " — " + clipRunes(snip, 240)
	}
	return "Тест не прошёл: " + msg
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func setHeadersFromJSON(req *http.Request, raw []byte) {
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

func validateWebhookURL(raw string, guard *ssrf.Guard) (string, error) {
	url := strings.TrimSpace(raw)
	if url == "" {
		return "", errors.New("url is required")
	}
	if guard != nil {
		if err := guard.ValidateURL(url); err != nil {
			return "", err
		}
	}
	return url, nil
}

func signHMACSHA256(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

type webhookLogDTO struct {
	ID              int64      `json:"id"`
	WebhookID       int64      `json:"webhook_id"`
	EntryID         int64      `json:"entry_id"`
	Status          string     `json:"status"`
	Attempt         int        `json:"attempt"`
	NextRetryAt     *time.Time `json:"next_retry_at,omitempty"`
	LastStatusCode  *int       `json:"last_status_code,omitempty"`
	LastError       *string    `json:"last_error,omitempty"`
	ResponseSnippet *string    `json:"response_snippet,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (s *Server) handleListWebhookLogs(w http.ResponseWriter, r *http.Request) {
	if s.webhookLogs == nil {
		writeError(w, http.StatusServiceUnavailable, "webhook log storage is not configured")
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, total, err := s.webhookLogs.ListWebhookLogs(r.Context(), id, limit, offset)
	if err != nil {
		s.log.Error("list webhook logs failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]webhookLogDTO, 0, len(rows))
	for _, l := range rows {
		out = append(out, webhookLogDTO{
			ID:              l.ID,
			WebhookID:       l.WebhookID,
			EntryID:         l.EntryID,
			Status:          l.Status,
			Attempt:         l.Attempt,
			NextRetryAt:     l.NextRetryAt,
			LastStatusCode:  l.LastStatusCode,
			LastError:       l.LastError,
			ResponseSnippet: l.ResponseSnippet,
			CreatedAt:       l.CreatedAt,
			UpdatedAt:       l.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, listResponse[[]webhookLogDTO]{Data: out, Total: total})
}

func (s *Server) handleRetryWebhookLog(w http.ResponseWriter, r *http.Request) {
	if s.webhookLogs == nil {
		writeError(w, http.StatusServiceUnavailable, "webhook log storage is not configured")
		return
	}
	logID, err := parsePathInt64(r, "logID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.webhookLogs.RetryWebhookLogNow(r.Context(), logID); err != nil {
		s.log.Error("retry webhook log failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[map[string]bool]{Data: map[string]bool{"retried": true}, Total: 1})
}
