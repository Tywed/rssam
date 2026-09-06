package ui

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/reader"
	"rssam/internal/storage"
)

func (h *Handler) loadFeedsListPage(r *http.Request, data *pageData, userID int64, filter string) error {
	data.FeedsFilter = filter
	ctx := r.Context()
	if h.cfg.Feeds == nil {
		return nil
	}

	if counts, err := h.cfg.Feeds.FeedCountsByCategory(ctx, userID); err == nil {
		data.TotalFeedCount = counts.Total
		if filter == "" {
			data.ListCategoryFeedCounts = counts.ByCategory
			data.ListUncategorizedFeedCount = counts.Uncategorized
		}
	}
	if errCount, inactiveCount, err := h.cfg.Feeds.CountFeedStatuses(ctx, userID); err == nil {
		data.ErrorFeedCount = errCount
		data.InactiveFeedCount = inactiveCount
	}

	if filter == "errors" || filter == "inactive" {
		limit, offset, page := parseFeedsListPage(r)
		feeds, total, err := h.cfg.Feeds.ListFeedsByStatus(ctx, userID, filter, limit, offset)
		if err != nil {
			return err
		}
		data.ListFeeds = feeds
		data.Total = total
		data.Limit = limit
		data.Offset = offset
		data.FeedsListPage = page
		data.FeedsListPageCount = adminFeedsPageCount(total, limit)
		return nil
	}

	if data.TotalFeedCount > sidebarLazyFeedThreshold {
		data.FeedsTreeLazy = true
		return nil
	}

	feeds, total, err := h.cfg.Feeds.ListFeeds(ctx, userID, storage.NoLimit, 0)
	if err != nil {
		return err
	}
	if data.TotalFeedCount == 0 {
		data.TotalFeedCount = total
	}
	if data.ListCategoryFeedCounts == nil {
		data.ListCategoryFeedCounts, data.ListUncategorizedFeedCount = categoryFeedCounts(feeds)
	}
	if data.ErrorFeedCount == 0 && data.InactiveFeedCount == 0 {
		data.ErrorFeedCount, data.InactiveFeedCount = countFeedStatuses(feeds)
	}
	data.ListFeeds = feeds
	return nil
}

func (h *Handler) handleFeedsList(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "feeds"
	filter := strings.TrimSpace(r.URL.Query().Get("filter"))
	if filter != "errors" && filter != "inactive" {
		filter = ""
	}
	if err := h.loadFeedsListPage(r, &data, p.UserID, filter); err != nil {
		http.Error(w, "list feeds failed", http.StatusInternalServerError)
		return
	}
	if p.IsAdmin {
		h.loadFeedFormWebhooks(r, &data)
	}
	data.Title = "Ленты"
	h.render(w, r, "feeds_list", data)
}

func (h *Handler) handleFeedNew(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "feeds"
	data.Feed.IntervalMinutes = 60
	data.TgUseProxy = true
	h.loadFeedFormWebhooks(r, &data)
	data.Title = "Новая лента"
	h.render(w, r, "feeds_form", data)
}

func (h *Handler) handleFeedCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	params, err := feedParamsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := reader.ValidateFeedURL(params.FeedURL, h.cfg.SSRFGuard); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params.FeedType = reader.DetectFeedTypeFromURL(params.FeedURL)
	if err := validateFeedBeforeCreate(r.Context(), h, params.FeedURL, params.FeedType, params.TLSInsecure); err != nil {
		h.renderFeedFormError(w, r, params, err.Error())
		return
	}
	if params.FeedType == reader.FeedTypeTelegram {
		params.BridgeState = bridgeStateFromTelegramForm(r, nil)
	}
	if params.Title == "" && h.cfg.ResolveFeedTitle != nil {
		if t, err := h.cfg.ResolveFeedTitle(r.Context(), params.FeedURL, params.FeedType, params.TLSInsecure); err == nil && strings.TrimSpace(t) != "" {
			params.Title = strings.TrimSpace(t)
		}
	}
	if params.Title == "" {
		params.Title = params.FeedURL
	}
	_, err = h.cfg.Feeds.CreateFeed(r.Context(), p.UserID, params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/feeds", http.StatusFound)
}

func (h *Handler) handleFeedShow(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	feed, err := h.cfg.Feeds.GetFeed(r.Context(), p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "feeds"
	data.Feed = feed
	data.Title = feed.Title
	h.render(w, r, "feeds_show", data)
}

func (h *Handler) handleFeedEdit(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	feed, err := h.cfg.Feeds.GetFeed(r.Context(), p.UserID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := h.baseData(r, "settings")
	data.SettingsSection = "feeds"
	data.Feed = feed
	applyTelegramFormData(&data, feed)
	h.loadFeedFormWebhooks(r, &data)
	data.Title = feed.Title
	h.render(w, r, "feeds_form", data)
}

func (h *Handler) loadFeedFormWebhooks(r *http.Request, data *pageData) {
	p, _ := principal(r)
	if h.cfg.Webhooks != nil {
		whs, _, _ := h.cfg.Webhooks.ListWebhooks(r.Context(), p.UserID, 500, 0)
		data.Webhooks = whs
	}
}

func (h *Handler) handleFeedUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	params, err := feedParamsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	existing, _ := h.cfg.Feeds.GetFeed(r.Context(), p.UserID, id)
	bridgeState := existing.BridgeState
	if existing.FeedType == reader.FeedTypeTelegram || reader.DetectFeedTypeFromURL(params.FeedURL) == reader.FeedTypeTelegram {
		bridgeState = bridgeStateFromTelegramForm(r, existing.BridgeState)
	}
	_, err = h.cfg.Feeds.UpdateFeed(r.Context(), p.UserID, storage.UpdateFeedParams{
		ID:                 id,
		FeedURL:            params.FeedURL,
		Title:              params.Title,
		CategoryID:         params.CategoryID,
		IntervalMinutes:    params.IntervalMinutes,
		TLSInsecure:        params.TLSInsecure,
		StoreHashOnly:      params.StoreHashOnly,
		EntryRetentionDays: params.EntryRetentionDays,
		WebhookID:          params.WebhookID,
		BridgeState:        bridgeState,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.cfg.Refresher != nil {
		_ = h.cfg.Refresher.RescheduleFeed(r.Context(), id, params.IntervalMinutes)
	}
	http.Redirect(w, r, "/ui/feeds/"+strconv.FormatInt(id, 10), http.StatusFound)
}

func (h *Handler) handleFeedDelete(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.cfg.Feeds.DeleteFeed(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/feeds", http.StatusFound)
}

func (h *Handler) handleFeedRefresh(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.cfg.Feeds.GetFeed(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	if h.cfg.Refresher != nil {
		_, _ = h.cfg.Refresher.RefreshFeedManual(r.Context(), id)
	}
	http.Redirect(w, r, refererOr(r, "/ui/feeds/"+strconv.FormatInt(id, 10)), http.StatusFound)
}

func (h *Handler) handleFeedMarkRead(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	feedID, err := parsePathID(r, "feedID")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, _ = h.cfg.Entries.MarkAllFeedEntriesRead(r.Context(), p.UserID, feedID)
	h.invalidateUnread(p.UserID)
	http.Redirect(w, r, "/ui/unread?feed_id="+strconv.FormatInt(feedID, 10), http.StatusFound)
}

func (h *Handler) handleFeedsImport(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32<<20))
	if err != nil {
		http.Error(w, "read file failed", http.StatusBadRequest)
		return
	}
	if h.cfg.OPMLImport != nil {
		report := h.cfg.OPMLImport(r, p.UserID, data)
		data := h.baseData(r, "settings")
		data.SettingsSection = "feeds"
		data.FlashMsg = fmt.Sprintf("Импорт: создано %d лент, %d категорий, пропущено %d", report.FeedsCreated, report.CategoriesCreated, report.FeedsSkipped)
		_ = h.loadFeedsListPage(r, &data, p.UserID, "")
		h.render(w, r, "feeds_list", data)
		return
	}
	http.Redirect(w, r, "/ui/feeds", http.StatusFound)
}

func (h *Handler) handleFeedsExport(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	if h.cfg.OPMLExport != nil {
		if err := h.cfg.OPMLExport(w, r, p.UserID); err != nil {
			http.Error(w, "export failed", http.StatusInternalServerError)
		}
		return
	}
	http.Error(w, "export unavailable", http.StatusServiceUnavailable)
}

func feedParamsFromForm(r *http.Request) (storage.CreateFeedParams, error) {
	_ = r.ParseForm()
	feedURL := strings.TrimSpace(r.FormValue("feed_url"))
	title := strings.TrimSpace(r.FormValue("title"))
	interval := 60
	if v := r.FormValue("interval_minutes"); v != "" {
		if n, err := strconvAtoi(v); err == nil {
			interval = n
		}
	}
	var catID *int64
	if v := strings.TrimSpace(r.FormValue("category_id")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			catID = &n
		}
	}
	if feedURL == "" {
		return storage.CreateFeedParams{}, errRequired("feed_url")
	}
	if interval < storage.MinFeedIntervalMinutes || interval > storage.MaxFeedIntervalMinutes {
		return storage.CreateFeedParams{}, fmt.Errorf("interval_minutes must be between %d and %d", storage.MinFeedIntervalMinutes, storage.MaxFeedIntervalMinutes)
	}
	retention, err := parseEntryRetentionDays(r.FormValue("entry_retention_days"))
	if err != nil {
		return storage.CreateFeedParams{}, err
	}
	return storage.CreateFeedParams{
		FeedURL:            feedURL,
		Title:              title,
		CategoryID:         catID,
		IntervalMinutes:    interval,
		TLSInsecure:        r.FormValue("tls_insecure") == "1",
		StoreHashOnly:      r.FormValue("store_hash_only") == "1",
		EntryRetentionDays: retention,
		WebhookID:          parseFeedWebhookID(r.FormValue("webhook_id")),
	}, nil
}

func parseEntryRetentionDays(v string) (*int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, fmt.Errorf("entry_retention_days must be a number")
	}
	if n == 0 {
		return nil, nil
	}
	if err := storage.ValidateEntryRetentionDays(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

func parseFeedWebhookID(v string) *int64 {
	v = strings.TrimSpace(v)
	if v == "" || v == "0" {
		return nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	return &id
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

func errRequired(field string) error { return simpleError(field + " is required") }

func applyTelegramFormData(data *pageData, feed storage.Feed) {
	useProxy := true
	svcURL := ""
	staticProxy := ""
	st := reader.ParseBridgeState(feed.BridgeState)
	if st.Telegram != nil {
		if st.Telegram.UseProxy != nil {
			useProxy = *st.Telegram.UseProxy
		}
		svcURL = st.Telegram.ProxyServiceURL
		staticProxy = st.Telegram.StaticProxy
	}
	data.TgUseProxy = useProxy
	data.TgProxyServiceURL = svcURL
	data.TgStaticProxy = staticProxy
}

func bridgeStateFromTelegramForm(r *http.Request, existing []byte) []byte {
	st := reader.ParseBridgeState(existing)
	if st.Telegram == nil {
		st.Telegram = &reader.TelegramBridgeState{}
	}
	useProxy := r.FormValue("telegram_use_proxy") == "1"
	st.Telegram.UseProxy = &useProxy
	st.Telegram.ProxyServiceURL = strings.TrimSpace(r.FormValue("telegram_proxy_service_url"))
	st.Telegram.StaticProxy = strings.TrimSpace(r.FormValue("telegram_static_proxy"))
	return st.Marshal()
}

func (h *Handler) handleFeedsCategoryTree(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	categoryID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || categoryID <= 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.renderFeedsCategoryTree(w, r, p.UserID, categoryID)
}

func (h *Handler) handleFeedsUncategorizedTree(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.renderFeedsCategoryTree(w, r, p.UserID, 0)
}

func (h *Handler) renderFeedsCategoryTree(w http.ResponseWriter, r *http.Request, userID, categoryID int64) {
	if h.cfg.Feeds == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	limit, offset := parseCategoryFeedsPage(r)
	filter := strings.TrimSpace(r.URL.Query().Get("filter"))
	if filter != "errors" && filter != "inactive" {
		filter = ""
	}

	var feeds []storage.Feed
	total := 0
	if filter != "" {
		allFeeds, err := h.cfg.Feeds.ListFeedsByCategory(r.Context(), userID, categoryID)
		if err != nil {
			http.Error(w, "list feeds failed", http.StatusInternalServerError)
			return
		}
		filtered := filterFeeds(allFeeds, filter)
		total = len(filtered)
		feeds = sliceFeedsPage(filtered, limit, offset)
	} else {
		var err error
		feeds, total, err = h.cfg.Feeds.ListFeedsByCategoryPaginated(r.Context(), userID, categoryID, limit, offset)
		if err != nil {
			http.Error(w, "list feeds failed", http.StatusInternalServerError)
			return
		}
	}
	data := pageData{
		Feeds:       feeds,
		CSRFToken:   h.csrfToken(r),
		FeedsFilter: filter,
		IsAdmin:     principalIsAdmin(r),
		Limit:       limit,
		Offset:      offset,
		Total:       total,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "feeds_mgmt_category_feeds", data); err != nil {
		h.log.Error("feeds tree template failed", "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func principalIsAdmin(r *http.Request) bool {
	p, ok := principal(r)
	return ok && p.IsAdmin
}
