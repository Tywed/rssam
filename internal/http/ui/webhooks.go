package ui

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

func (h *Handler) loadAdminWebhooksDashboard(r *http.Request, userID int64) (pageData, error) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "webhooks"
	data.Title = "Вебхуки"

	ctx := r.Context()

	if h.cfg.AdminWebhooks == nil {
		whs, _, err := h.cfg.Webhooks.ListWebhooks(ctx, userID, 1000, 0)
		if err != nil {
			return data, err
		}
		views := make([]adminWebhookRowView, 0, len(whs))
		for _, wh := range whs {
			row := storage.AdminWebhookRow{Webhook: wh}
			views = append(views, adminWebhookRowView{
				AdminWebhookRow: row,
				Status:          classifyAdminWebhookStatus(row),
			})
		}
		data.AdminWebhookRows = views
		data.AdminWebhookSummary = storage.AdminWebhookSummary{TotalWebhooks: len(whs)}
		return data, nil
	}

	summary, err := h.cfg.AdminWebhooks.AdminWebhookSummary(ctx, userID)
	if err != nil {
		return data, err
	}
	data.AdminWebhookSummary = summary

	rows, err := h.cfg.AdminWebhooks.ListAdminWebhooks(ctx, userID)
	if err != nil {
		return data, err
	}

	views := make([]adminWebhookRowView, 0, len(rows))
	for _, row := range rows {
		views = append(views, adminWebhookRowView{
			AdminWebhookRow: row,
			Status:          classifyAdminWebhookStatus(row),
		})
	}
	data.AdminWebhookStatusCounts = countAdminWebhookStatuses(views)

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "ok", "queue", "retrying", "dead", "disabled", "idle":
		data.AdminWebhooksFilter = status
	default:
		data.AdminWebhooksFilter = "all"
		status = "all"
	}
	data.AdminWebhookRows = filterAdminWebhookRows(views, status)
	data.WebhookMaxAttempts = h.cfg.WebhookMaxAttempts
	return data, nil
}

func (h *Handler) handleWebhooksList(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	data, err := h.loadAdminWebhooksDashboard(r, p.UserID)
	if err != nil {
		http.Error(w, "list webhooks failed", http.StatusInternalServerError)
		return
	}
	h.render(w, r, "webhooks_list", data)
}

func (h *Handler) handleWebhookNew(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "webhooks"
	data.Webhook.Method = "POST"
	data.Webhook.Enabled = true
	data.Webhook.Kind = storage.WebhookKindHTTP
	data.WebhookMaxAttempts = h.cfg.WebhookMaxAttempts
	data.Title = "Новый webhook"
	h.render(w, r, "webhooks_form", data)
}

func (h *Handler) loadWebhookDetail(r *http.Request, userID, id int64) (pageData, error) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "webhooks"
	data.WebhookMaxAttempts = h.cfg.WebhookMaxAttempts

	if h.cfg.AdminWebhooks != nil {
		row, err := h.cfg.AdminWebhooks.GetAdminWebhook(r.Context(), userID, id)
		if err != nil {
			return data, err
		}
		data.Webhook = row.Webhook
		fillWebhookProviderFields(&data, row.Webhook)
		data.AdminWebhookDetail = adminWebhookRowView{
			AdminWebhookRow: row,
			Status:          classifyAdminWebhookStatus(row),
		}
		feeds, err := h.cfg.AdminWebhooks.ListWebhookBindingFeeds(r.Context(), userID, id)
		if err != nil {
			return data, err
		}
		data.WebhookBindingFeeds = feeds
		filters, err := h.cfg.AdminWebhooks.ListWebhookBindingFilters(r.Context(), userID, id)
		if err != nil {
			return data, err
		}
		data.WebhookBindingFilters = filters
		return data, nil
	}

	wh, err := h.cfg.Webhooks.GetWebhook(r.Context(), userID, id)
	if err != nil {
		return data, err
	}
	data.Webhook = wh
	fillWebhookProviderFields(&data, wh)
	return data, nil
}

func (h *Handler) handleWebhookEdit(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := h.loadWebhookDetail(r, p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data.Title = "Webhook"
	h.render(w, r, "webhooks_form", data)
}

func (h *Handler) handleWebhookCreate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	params, err := webhookParamsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params.UserID = p.UserID
	if _, err := h.cfg.Webhooks.CreateWebhook(r.Context(), params); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.invalidateWebhookCache(p.UserID)
	http.Redirect(w, r, "/ui/webhooks", http.StatusFound)
}

func (h *Handler) invalidateWebhookCache(userID int64) {
	if h.cfg.Refresher != nil {
		h.cfg.Refresher.InvalidateWebhookCache(userID)
	}
}

func (h *Handler) handleWebhookUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params, err := webhookParamsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, err = h.cfg.Webhooks.UpdateWebhook(r.Context(), storage.UpdateWebhookParams{
		ID:             id,
		UserID:         p.UserID,
		FilterID:       params.FilterID,
		Name:           &params.Name,
		URL:            params.URL,
		Method:         params.Method,
		BodyTemplate:   params.BodyTemplate,
		Secret:         &params.Secret,
		Enabled:        params.Enabled,
		OnSuccessEntry: params.OnSuccessEntry,
		Kind:           params.Kind,
		ProviderConfig: params.ProviderConfig,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.invalidateWebhookCache(p.UserID)
	http.Redirect(w, r, "/ui/webhooks/"+strconv.FormatInt(id, 10), http.StatusFound)
}

func (h *Handler) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.cfg.Webhooks.DeleteWebhook(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	h.invalidateWebhookCache(p.UserID)
	http.Redirect(w, r, "/ui/webhooks", http.StatusFound)
}

func webhooksListRedirect(r *http.Request) string {
	status := strings.TrimSpace(r.FormValue("status"))
	switch status {
	case "ok", "queue", "retrying", "dead", "disabled", "idle":
		return "/ui/webhooks?status=" + status
	case "all":
		return "/ui/webhooks"
	}
	return refererOr(r, "/ui/webhooks")
}

func (h *Handler) handleWebhookPause(w http.ResponseWriter, r *http.Request) {
	h.setWebhookEnabled(w, r, false)
}

func (h *Handler) handleWebhookUnpause(w http.ResponseWriter, r *http.Request) {
	h.setWebhookEnabled(w, r, true)
}

func (h *Handler) setWebhookEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Webhooks.GetWebhook(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.cfg.Webhooks.SetWebhookEnabled(r.Context(), p.UserID, id, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.invalidateWebhookCache(p.UserID)
	http.Redirect(w, r, webhooksListRedirect(r), http.StatusFound)
}

func (h *Handler) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := h.loadWebhookDetail(r, p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data.Title = "Webhook"
	if h.cfg.TestWebhook == nil {
		data.FlashErr = "Тест вебхука недоступен"
		h.render(w, r, "webhooks_form", data)
		return
	}
	res, terr := h.cfg.TestWebhook(r, p.UserID, id)
	if terr != nil {
		data.FlashErr = terr.Error()
	} else if res.OK {
		data.FlashMsg = res.Message
	} else {
		data.FlashErr = res.Message
	}
	h.render(w, r, "webhooks_form", data)
}

func (h *Handler) handleWebhookRetryAll(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Webhooks.GetWebhook(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if h.cfg.AdminWebhooks != nil {
		_, _ = h.cfg.AdminWebhooks.RetryAllWebhookLogs(r.Context(), id)
	}
	http.Redirect(w, r, "/ui/webhooks/"+strconv.FormatInt(id, 10), http.StatusFound)
}

func (h *Handler) handleWebhookLogs(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) {
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Webhooks.GetWebhook(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}

	status, period := parseWebhookLogsFilter(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "webhooks"
	data.WebhookID = id
	data.WebhookLogsFilter = status
	data.WebhookLogsPeriod = period
	data.WebhookMaxAttempts = h.cfg.WebhookMaxAttempts
	data.Title = "Логи webhook"

	if h.cfg.AdminWebhooks != nil {
		logs, _, err := h.cfg.AdminWebhooks.ListWebhookLogRows(r.Context(), id, status, webhookLogsSince(period), 200, 0)
		if err != nil {
			http.Error(w, "list logs failed", http.StatusInternalServerError)
			return
		}
		data.WebhookLogRows = logs
	} else {
		logs, _, err := h.cfg.WebhookLogs.ListWebhookLogs(r.Context(), id, 200, 0)
		if err != nil {
			http.Error(w, "list logs failed", http.StatusInternalServerError)
			return
		}
		data.Logs = logs
	}
	h.render(w, r, "webhooks_logs", data)
}

func (h *Handler) handleWebhookLogRetry(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdminPrincipal(w, r) || !h.validateCSRF(r) {
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, _ := principal(r)
	_ = h.cfg.WebhookLogs.RetryWebhookLogNow(r.Context(), p.UserID, id)
	http.Redirect(w, r, refererOr(r, "/ui/webhooks"), http.StatusFound)
}

func fillWebhookProviderFields(data *pageData, w storage.Webhook) {
	var tg storage.TelegramProviderConfig
	var mx storage.MaxProviderConfig
	_ = json.Unmarshal(w.ProviderConfig, &tg)
	_ = json.Unmarshal(w.ProviderConfig, &mx)
	data.WebhookTelegramTokenSet = strings.TrimSpace(tg.BotToken) != ""
	tg.BotToken = ""
	data.WebhookTelegram = tg
	data.WebhookMax = mx
}

func webhookParamsFromForm(r *http.Request) (storage.CreateWebhookParams, error) {
	_ = r.ParseForm()
	kind := strings.TrimSpace(r.FormValue("kind"))
	method := strings.TrimSpace(r.FormValue("method"))
	if method == "" {
		method = "POST"
	}
	var filterID *int64
	if v := strings.TrimSpace(r.FormValue("filter_id")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			filterID = &n
		}
	}
	params := storage.CreateWebhookParams{
		FilterID:       filterID,
		Name:           r.FormValue("name"),
		URL:            strings.TrimSpace(r.FormValue("url")),
		Method:         method,
		BodyTemplate:   r.FormValue("body_template"),
		Secret:         r.FormValue("secret"),
		Enabled:        r.FormValue("enabled") == "1",
		OnSuccessEntry: r.FormValue("on_success_entry"),
		Kind:           kind,
	}
	kindNorm, err := storage.NormalizeWebhookKind(kind)
	if err != nil {
		return storage.CreateWebhookParams{}, err
	}
	switch kindNorm {
	case storage.WebhookKindTelegram:
		cfg := storage.TelegramProviderConfig{
			APIBase:  r.FormValue("telegram_api_base"),
			BotToken: r.FormValue("telegram_bot_token"),
			ChatID:   r.FormValue("telegram_chat_id"),
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			return storage.CreateWebhookParams{}, err
		}
		params.ProviderConfig = b
	case storage.WebhookKindMax:
		cfg := storage.MaxProviderConfig{
			APIBase:  r.FormValue("max_api_base"),
			Target:   r.FormValue("max_target"),
			TargetID: r.FormValue("max_target_id"),
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			return storage.CreateWebhookParams{}, err
		}
		params.ProviderConfig = b
	default:
		if params.URL == "" {
			return storage.CreateWebhookParams{}, errRequired("url")
		}
	}
	return params, nil
}
