package max

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

const feedTypeMax = "max"

// Config controls the Max channel bridge.
type Config struct {
	APIBaseURL        string
	DefaultLimit      int
	DefaultLookback   time.Duration
	Overlap           time.Duration
	RateLimitCooldown time.Duration
	AllowPrivateAPI   bool
	FetchAllowPrivate bool
}

// Handler fetches messages from a local Max channel API.
type Handler struct {
	client *http.Client
	guard  *ssrf.Guard
	cfg    Config

	mu          sync.Mutex
	rateLimited map[int64]time.Time // feedID -> until (in-memory supplement)
}

func NewHandler(client *http.Client, guard *ssrf.Guard, cfg Config) (*Handler, error) {
	cfg.APIBaseURL = strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if cfg.APIBaseURL == "" {
		return nil, errors.New("max: API base URL is required")
	}
	if cfg.DefaultLimit <= 0 {
		cfg.DefaultLimit = 100
	}
	if cfg.DefaultLookback <= 0 {
		cfg.DefaultLookback = 24 * time.Hour
	}
	if cfg.Overlap <= 0 {
		cfg.Overlap = 2 * time.Minute
	}
	if cfg.RateLimitCooldown <= 0 {
		cfg.RateLimitCooldown = 60 * time.Second
	}
	if err := validateAPIBase(cfg.APIBaseURL, guard, cfg.AllowPrivateAPI, cfg.FetchAllowPrivate); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Handler{
		client:      client,
		guard:       guard,
		cfg:         cfg,
		rateLimited: make(map[int64]time.Time),
	}, nil
}

func validateAPIBase(raw string, guard *ssrf.Guard, allowPrivateAPI, fetchAllowPrivate bool) error {
	if guard == nil {
		return nil
	}
	if allowPrivateAPI || fetchAllowPrivate {
		return nil
	}
	return guard.ValidateURL(raw)
}

func (h *Handler) Name() string { return feedTypeMax }

// UpdateConfig replaces global Max bridge settings at runtime.
func (h *Handler) UpdateConfig(cfg Config) error {
	cfg.APIBaseURL = strings.TrimRight(strings.TrimSpace(cfg.APIBaseURL), "/")
	if cfg.APIBaseURL == "" {
		return nil
	}
	if cfg.DefaultLimit <= 0 {
		cfg.DefaultLimit = 100
	}
	if cfg.DefaultLookback <= 0 {
		cfg.DefaultLookback = 24 * time.Hour
	}
	if cfg.Overlap <= 0 {
		cfg.Overlap = 2 * time.Minute
	}
	if cfg.RateLimitCooldown <= 0 {
		cfg.RateLimitCooldown = 60 * time.Second
	}
	if err := validateAPIBase(cfg.APIBaseURL, h.guard, cfg.AllowPrivateAPI, cfg.FetchAllowPrivate); err != nil {
		return err
	}
	h.cfg = cfg
	return nil
}

func (h *Handler) DetectFeedType(feedURL string) string {
	if _, ok := ParseChannelFromFeedURL(feedURL); ok {
		return feedTypeMax
	}
	return ""
}

// FetchState is bridge cursor state for Max feeds.
type FetchState struct {
	LastEndTimeMs    int64
	RateLimitedUntil *time.Time
}

// FetchResult is the outcome of a Max channel poll.
type FetchResult struct {
	Entries []storage.CreateEntryParams
	State   FetchState
}

func (h *Handler) Fetch(ctx context.Context, feedURL string, st FetchState) (FetchResult, error) {
	channel, ok := ParseChannelFromFeedURL(feedURL)
	if !ok || channel == "" {
		return FetchResult{}, fmt.Errorf("max: invalid feed url %q", feedURL)
	}
	now := time.Now().UTC()

	if st.RateLimitedUntil != nil && now.Before(*st.RateLimitedUntil) {
		return FetchResult{}, fmt.Errorf("max: rate limited until %s", st.RateLimitedUntil.Format(time.RFC3339))
	}

	lastEnd := st.LastEndTimeMs
	afterMs, endTimeMs := ComputeAfter(lastEnd, CursorParams{
		DefaultLookback: h.cfg.DefaultLookback,
		Overlap:         h.cfg.Overlap,
		NowTime:         now,
	})

	limit := clampLimit(h.cfg.DefaultLimit)
	apiURL, err := h.buildMessagesURL(channel, limit, afterMs, lastEnd > 0)
	if err != nil {
		return FetchResult{}, err
	}

	body, status, err := h.doGET(ctx, apiURL)
	if err != nil {
		return FetchResult{}, err
	}
	if status == http.StatusTooManyRequests {
		until := now.Add(h.cfg.RateLimitCooldown)
		st.RateLimitedUntil = &until
		return FetchResult{State: st}, fmt.Errorf("max: rate limited (HTTP 429)")
	}
	if status != http.StatusOK {
		return FetchResult{}, fmt.Errorf("max: unexpected status %d", status)
	}

	var parsed APIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return FetchResult{}, fmt.Errorf("max: decode response: %w", err)
	}

	entries := make([]storage.CreateEntryParams, 0, len(parsed.Messages))
	for _, msg := range parsed.Messages {
		if ShouldSkipMessage(msg.Time, lastEnd) {
			continue
		}
		entries = append(entries, EntryFromMessage(channel, msg))
	}

	st.LastEndTimeMs = endTimeMs
	st.RateLimitedUntil = nil

	return FetchResult{
		Entries: entries,
		State:   st,
	}, nil
}

func (h *Handler) buildMessagesURL(channel string, limit int, afterMs int64, hasCursor bool) (string, error) {
	base, err := url.Parse(h.cfg.APIBaseURL + "/channel/" + url.PathEscape(channel) + "/messages")
	if err != nil {
		return "", fmt.Errorf("max: build url: %w", err)
	}
	q := base.Query()
	q.Set("limit", fmt.Sprintf("%d", limit))
	if hasCursor {
		q.Set("after", fmt.Sprintf("%d", afterMs))
	}
	base.RawQuery = q.Encode()
	return base.String(), nil
}

func (h *Handler) doGET(ctx context.Context, rawURL string) ([]byte, int, error) {
	if h.guard != nil && !h.cfg.AllowPrivateAPI && !h.cfg.FetchAllowPrivate {
		if err := h.guard.ValidateURL(rawURL); err != nil {
			return nil, 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("max: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("max: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("max: read body: %w", err)
	}
	return body, resp.StatusCode, nil
}

func clampLimit(n int) int {
	if n < 1 {
		return 1
	}
	if n > 200 {
		return 200
	}
	return n
}

// SetFeedRateLimit marks a feed as rate-limited in memory (optional supplement to bridge_state).
func (h *Handler) SetFeedRateLimit(feedID int64, until time.Time) {
	h.mu.Lock()
	h.rateLimited[feedID] = until
	h.mu.Unlock()
}

func (h *Handler) IsFeedRateLimited(feedID int64) bool {
	h.mu.Lock()
	until, ok := h.rateLimited[feedID]
	h.mu.Unlock()
	return ok && time.Now().Before(until)
}
