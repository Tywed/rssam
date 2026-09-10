package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rssam/internal/model"
	"rssam/internal/proxy"
	"rssam/internal/storage"
)

const feedTypeTelegram = "telegram"

// Handler fetches public Telegram channels via proxy-service + SOCKS5 or direct HTTP.
type Handler struct {
	proxyClient *proxy.Client
	httpClient  *http.Client
	cfg         Config
	slots       *fetchSlots
}

func NewHandler(proxyClient *proxy.Client, httpClient *http.Client, cfg Config) *Handler {
	if cfg.ProxyRetry < 0 {
		cfg.ProxyRetry = 0
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 1
	}
	if cfg.ProxyConnectTimeout <= 0 {
		cfg.ProxyConnectTimeout = 10 * time.Second
	}
	if cfg.ProxyRequestTimeout <= 0 {
		cfg.ProxyRequestTimeout = 25 * time.Second
	}
	if cfg.ProxyServiceURL != "" || cfg.StaticProxy != "" {
		cfg.UseProxy = true
	}
	if proxyClient != nil && cfg.ProxyServiceToken != "" {
		proxyClient.ServiceToken = cfg.ProxyServiceToken
	}
	cfg.ConcurrentSlots = ClampConcurrentSlots(cfg.ConcurrentSlots)
	return &Handler{proxyClient: proxyClient, httpClient: httpClient, cfg: cfg, slots: newFetchSlots(cfg.ConcurrentSlots)}
}

// UpdateConfig replaces global Telegram bridge settings at runtime.
func (h *Handler) UpdateConfig(cfg Config) {
	if cfg.ProxyRetry < 0 {
		cfg.ProxyRetry = 0
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 1
	}
	if cfg.ProxyConnectTimeout <= 0 {
		cfg.ProxyConnectTimeout = 10 * time.Second
	}
	if cfg.ProxyRequestTimeout <= 0 {
		cfg.ProxyRequestTimeout = 25 * time.Second
	}
	if cfg.ProxyServiceURL != "" || cfg.StaticProxy != "" {
		cfg.UseProxy = true
	}
	if h.proxyClient != nil && cfg.ProxyServiceToken != "" {
		h.proxyClient.ServiceToken = cfg.ProxyServiceToken
	}
	cfg.ConcurrentSlots = ClampConcurrentSlots(cfg.ConcurrentSlots)
	h.slots.setLimit(cfg.ConcurrentSlots)
	h.cfg = cfg
}

func (h *Handler) Name() string { return feedTypeTelegram }

func (h *Handler) DetectFeedType(feedURL string) string {
	if DetectFeedURL(feedURL) {
		return feedTypeTelegram
	}
	return ""
}

// FetchResult holds entries from a channel poll.
type FetchResult struct {
	Entries []storage.CreateEntryParams
}

// Fetch loads channel messages using configured proxy mode.
func (h *Handler) Fetch(ctx context.Context, feedURL string, override *BridgeOverride) (FetchResult, error) {
	if h == nil {
		return FetchResult{}, fmt.Errorf("telegram: handler is not configured")
	}
	cfg := EffectiveConfig(h.cfg, override)
	if err := cfg.validateFetch(); err != nil {
		return FetchResult{}, err
	}

	username, ok := ParseUsernameFromFeedURL(feedURL)
	if !ok || username == "" {
		return FetchResult{}, fmt.Errorf("telegram: cannot extract channel username from URL: %s", feedURL)
	}

	maxPages := min(max(cfg.MaxPages, 1), 100)

	// Wait for a slot with at most half of the remaining budget so that the
	// fetch itself still has time; a longer wait defers the poll instead of
	// turning a slow neighbour into this feed's error.
	waitCtx := ctx
	if dl, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithDeadline(ctx, time.Now().Add(time.Until(dl)/2))
		defer cancel()
	}
	if err := h.slots.acquire(waitCtx); err != nil {
		return FetchResult{}, &ErrSlotsBusy{Until: time.Now().UTC().Add(30 * time.Second)}
	}
	defer h.slots.release()

	pageURL := PreviewURL(username)
	var all []ParsedMessage

	for page := range maxPages {
		body, err := h.fetchPage(ctx, cfg, pageURL)
		if err != nil {
			return FetchResult{}, err
		}
		msgs, err := ParseHTML(body, username)
		if err != nil {
			return FetchResult{}, err
		}
		all = append(all, msgs...)

		next, ok := NextPageURL(body)
		if !ok || page+1 >= maxPages {
			break
		}
		pageURL = next
	}

	// Oldest-first order (PHP reverses per page then reverses all).
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	entries := make([]storage.CreateEntryParams, 0, len(all))
	for _, m := range all {
		entries = append(entries, messageToEntry(m))
	}
	return FetchResult{Entries: entries}, nil
}

// DiscoverChannelTitle loads t.me/s preview and returns the channel display name.
func (h *Handler) DiscoverChannelTitle(ctx context.Context, feedURL string) (string, error) {
	if h == nil {
		return "", fmt.Errorf("telegram: handler is not configured")
	}
	username, ok := ParseUsernameFromFeedURL(feedURL)
	if !ok || username == "" {
		return "", fmt.Errorf("telegram: cannot extract channel username from URL: %s", feedURL)
	}
	body, err := h.fetchPage(ctx, h.cfg, PreviewURL(username))
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(ChannelTitleFromHTML(body))
	if title == "" {
		return "@" + NormalizeUsername(username), nil
	}
	return title, nil
}

func (h *Handler) fetchPage(ctx context.Context, cfg Config, pageURL string) ([]byte, error) {
	switch cfg.ResolveProxyMode() {
	case ProxyModeDirect:
		return h.fetchDirect(ctx, cfg, pageURL)
	case ProxyModeStatic:
		return h.fetchViaStatic(ctx, cfg, pageURL)
	default:
		return h.fetchPageWithRetry(ctx, cfg, pageURL)
	}
}

func (h *Handler) fetchDirect(ctx context.Context, cfg Config, pageURL string) ([]byte, error) {
	client := h.httpClient
	if client == nil && h.proxyClient != nil {
		client = h.proxyClient.HTTP
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.ProxyRequestTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: direct fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: direct fetch status %d", resp.StatusCode)
	}
	return body, nil
}

func (h *Handler) fetchViaStatic(ctx context.Context, cfg Config, pageURL string) ([]byte, error) {
	p, err := cfg.StaticProxyEndpoint()
	if err != nil {
		return nil, err
	}
	body, status, err := proxy.FetchGET(ctx, p, pageURL, cfg.UserAgent, cfg.ProxyConnectTimeout, cfg.ProxyRequestTimeout, cfg.TLSInsecureSkipVerify)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("telegram: static proxy fetch status %d", status)
	}
	return body, nil
}

func (h *Handler) fetchPageWithRetry(ctx context.Context, cfg Config, pageURL string) ([]byte, error) {
	if h.proxyClient == nil {
		return nil, fmt.Errorf("telegram: proxy client is not configured")
	}
	maxTries := max(1+cfg.ProxyRetry, 1)
	var lastErr error
	for range maxTries {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		p, err := h.proxyClient.WorkingProxy(ctx, cfg.ProxyServiceURL, cfg.ProxyTargetURL)
		if err != nil {
			return nil, fmt.Errorf("telegram: get proxy: %w", err)
		}
		body, status, err := h.proxyClient.FetchGET(ctx, p, pageURL, cfg.UserAgent, cfg.ProxyConnectTimeout, cfg.ProxyRequestTimeout, cfg.TLSInsecureSkipVerify)
		if err == nil && status == http.StatusOK {
			return body, nil
		}
		var reason string
		if err != nil {
			reason = err.Error()
			lastErr = err
		} else {
			lastErr = fmt.Errorf("telegram: unexpected status %d", status)
			reason = lastErr.Error()
		}
		_ = h.proxyClient.ReportBadProxy(ctx, cfg.ProxyServiceURL, p.ID, reason)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("telegram: fetch failed after retries")
}

func messageToEntry(m ParsedMessage) storage.CreateEntryParams {
	uri := model.NormalizeURL(strings.TrimSpace(m.URI))
	var author *string
	if a := strings.TrimSpace(m.Author); a != "" {
		author = &a
	}
	var pub *time.Time
	if !m.Timestamp.IsZero() {
		t := m.Timestamp
		pub = &t
	}
	content := m.Content
	if len(m.Enclosures) > 0 {
		var b strings.Builder
		b.WriteString(content)
		for _, enc := range m.Enclosures {
			if strings.Contains(content, enc) {
				continue
			}
			fmt.Fprintf(&b, `<p><a href=%q rel="nofollow">media</a></p>`, enc)
		}
		content = b.String()
	}
	return storage.CreateEntryParams{
		Title:       strings.TrimSpace(m.Title),
		URL:         uri,
		Content:     content,
		Author:      author,
		PublishedAt: pub,
		Hash:        model.DedupHashFromURL(uri),
		Status:      storage.EntryStatusUnread,
	}
}
