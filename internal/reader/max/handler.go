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
	RequestInterval   time.Duration // min gap between HTTP calls when no slot is busy
	ConcurrentSlots   int           // max parallel Max API requests (default 1)
	AllowPrivateAPI   bool
	FetchAllowPrivate bool
}

// Handler fetches messages from a local Max channel API.
type Handler struct {
	client *http.Client
	guard  *ssrf.Guard

	cfgMu sync.RWMutex
	cfg   Config

	mu          sync.Mutex
	rateLimited map[int64]time.Time // feedID -> until (in-memory supplement)

	slotMu      sync.Mutex
	nextSlot    time.Time
	globalUntil time.Time
	inFlight    int
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
	if cfg.RequestInterval < 0 {
		cfg.RequestInterval = 0
	}
	cfg.ConcurrentSlots = ClampConcurrentSlots(cfg.ConcurrentSlots)
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
	if cfg.RequestInterval < 0 {
		cfg.RequestInterval = 0
	}
	cfg.ConcurrentSlots = ClampConcurrentSlots(cfg.ConcurrentSlots)
	if err := validateAPIBase(cfg.APIBaseURL, h.guard, cfg.AllowPrivateAPI, cfg.FetchAllowPrivate); err != nil {
		return err
	}
	h.cfgMu.Lock()
	h.cfg = cfg
	h.cfgMu.Unlock()
	return nil
}

func (h *Handler) snapshotConfig() Config {
	h.cfgMu.RLock()
	defer h.cfgMu.RUnlock()
	return h.cfg
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

// ErrBackoff means this feed should be retried later; it is not a poll failure.
type ErrBackoff struct {
	Until time.Time
}

// Error carries no timestamp so repeated occurrences coalesce in the poll log.
func (e *ErrBackoff) Error() string {
	return "max: rate limited or slot busy, retry later"
}

func (e *ErrBackoff) RetryAt() time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.Until
}

func IsBackoff(err error) (time.Time, bool) {
	var e *ErrBackoff
	if errors.As(err, &e) && e != nil && !e.Until.IsZero() {
		return e.Until, true
	}
	return time.Time{}, false
}

func (h *Handler) Fetch(ctx context.Context, feedURL string, st FetchState) (FetchResult, error) {
	channel, ok := ParseChannelFromFeedURL(feedURL)
	if !ok || channel == "" {
		return FetchResult{}, fmt.Errorf("max: invalid feed url %q", feedURL)
	}
	now := time.Now().UTC()
	cfg := h.snapshotConfig()

	if st.RateLimitedUntil != nil && now.Before(*st.RateLimitedUntil) {
		return FetchResult{State: st}, &ErrBackoff{Until: st.RateLimitedUntil.UTC()}
	}

	lastEnd := st.LastEndTimeMs
	afterMs, endTimeMs := ComputeAfter(lastEnd, CursorParams{
		DefaultLookback: cfg.DefaultLookback,
		Overlap:         cfg.Overlap,
		NowTime:         now,
	})

	limit := clampLimit(cfg.DefaultLimit)
	apiURL, err := h.buildMessagesURL(cfg, channel, limit, afterMs, lastEnd > 0)
	if err != nil {
		return FetchResult{}, err
	}

	if until, ok := h.tryReserveSlot(); !ok {
		return FetchResult{State: st}, &ErrBackoff{Until: until.UTC()}
	}
	rateLimited := false
	defer h.releaseSlot(&rateLimited)

	body, status, err := h.doGET(ctx, cfg, apiURL)
	if err != nil {
		return FetchResult{}, err
	}
	if looksLikeRateLimit(status, body) {
		rateLimited = true
		until := time.Now().UTC().Add(cfg.RateLimitCooldown)
		st.RateLimitedUntil = &until
		return FetchResult{State: st}, &ErrBackoff{Until: until}
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

func (h *Handler) buildMessagesURL(cfg Config, channel string, limit int, afterMs int64, hasCursor bool) (string, error) {
	base, err := url.Parse(cfg.APIBaseURL + "/channel/" + url.PathEscape(channel) + "/messages")
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

func (h *Handler) doGET(ctx context.Context, cfg Config, rawURL string) ([]byte, int, error) {
	if h.guard != nil && !cfg.AllowPrivateAPI && !cfg.FetchAllowPrivate {
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

func looksLikeRateLimit(status int, body []byte) bool {
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return true
	}
	if status == http.StatusOK || status == 0 {
		return false
	}
	s := strings.ToLower(string(body))
	return strings.Contains(s, "rate limit") ||
		strings.Contains(s, "too many") ||
		strings.Contains(s, "flood") ||
		strings.Contains(s, "лимит")
}

func ClampConcurrentSlots(n int) int {
	if n < 1 {
		return 1
	}
	if n > 32 {
		return 32
	}
	return n
}

func (h *Handler) tryReserveSlot() (until time.Time, ok bool) {
	h.slotMu.Lock()
	defer h.slotMu.Unlock()
	now := time.Now()
	cfg := h.snapshotConfig()
	slots := ClampConcurrentSlots(cfg.ConcurrentSlots)
	until = h.nextSlot
	if h.globalUntil.After(until) {
		until = h.globalUntil
	}
	if h.inFlight >= slots {
		if until.Before(now.Add(50 * time.Millisecond)) {
			until = now.Add(50 * time.Millisecond)
		}
		return until, false
	}
	if now.Before(h.globalUntil) {
		return h.globalUntil, false
	}
	if h.inFlight == 0 && now.Before(h.nextSlot) {
		return h.nextSlot, false
	}
	h.inFlight++
	return time.Time{}, true
}

func (h *Handler) releaseSlot(rateLimited *bool) {
	h.slotMu.Lock()
	defer h.slotMu.Unlock()
	if h.inFlight > 0 {
		h.inFlight--
	}
	now := time.Now()
	cfg := h.snapshotConfig()
	interval := max(cfg.RequestInterval, 0)
	h.nextSlot = now.Add(interval)
	if rateLimited != nil && *rateLimited {
		cd := cfg.RateLimitCooldown
		if cd <= 0 {
			cd = 60 * time.Second
		}
		h.globalUntil = now.Add(cd)
		if h.globalUntil.After(h.nextSlot) {
			h.nextSlot = h.globalUntil
		}
	}
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
	defer h.mu.Unlock()
	now := time.Now()
	for id, until := range h.rateLimited {
		if !now.Before(until) {
			delete(h.rateLimited, id)
		}
	}
	until, ok := h.rateLimited[feedID]
	return ok && now.Before(until)
}
