package httpserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rssam/internal/bridgeconfig"
	"rssam/internal/filter"
	"rssam/internal/http/middleware"
	"rssam/internal/reader"
	"rssam/internal/scraper"
	"rssam/internal/service"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
	"rssam/internal/ws"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type refreshAllEnqueuer interface {
	EnqueueRefreshAllPollJobs(ctx context.Context) (feeds int, queued int, err error)
}

type Dependencies struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool

	CategoryStore    storage.CategoryStore
	FeedStore        storage.FeedStore
	EntryStore       storage.EntryStore
	EntryDedupStore  storage.EntryDedupStore
	FilterStore      storage.FilterStore
	FilterMatchStore storage.FilterMatchStore
	LabelStore       storage.LabelStore
	WebhookStore     storage.WebhookStore
	WebhookLogStore  storage.WebhookLogStore
	UserStore        storage.UserStore
	SessionStore     storage.SessionStore
	FilterEngine     *filter.Engine

	AuthToken     string
	AdminUsername string
	AdminPassword string
	MetricsToken  string

	HTTPClient                 *http.Client
	FetchUserAgent             string
	FetchTimeoutSec            int
	FetchAllowPrivateNet       bool
	FetchAllowedCIDRs          []string
	FetchBlockedHosts          []string
	FetchViaProxyURL           string
	FetchTLSInsecureSkipVerify bool
	ScraperMaxContentBytes     int64
	SSRFGuard                  *ssrf.Guard

	WebhookHTTPClient  *http.Client
	WebhookMaxAttempts int

	WSEnabled      bool
	WSHub          *ws.Hub
	WSClientBuffer int
	WSPingInterval time.Duration

	UIEnabled  bool
	CSRFSecret string

	HSTSEnabled         bool
	RateLimitEnabled    bool
	RateLimitRPS        float64
	RateLimitBurst      int
	LoginRateLimitRPS   float64
	LoginRateLimitBurst int
	MaxRequestBodyBytes int64
	CompressEnabled     bool
	MaxImportFeeds      int

	WorkerPoolSize        int
	WebhookWorkerPoolSize int
	EnvFilePath           string
	DatabaseURL           string
	GitHubRepo            string

	HandlerRegistry *reader.HandlerRegistry
	TitleResolver   *reader.TitleResolver
	BridgeManager   *bridgeconfig.Manager
	RefreshAll      refreshAllEnqueuer

	MinPollInterval  time.Duration
	MaxPollInterval  time.Duration
	StoreEntriesMode string

	DedupStore              storage.EntryDedupStore
	CircuitBreakerThreshold int

	WorkerControl WorkerControl
}

// WorkerControl pauses poll and webhook workers without stopping HTTP/UI.
type WorkerControl interface {
	Pause()
	Resume()
	Paused() bool
}

type Server struct {
	log           *slog.Logger
	db            *pgxpool.Pool
	categories    storage.CategoryStore
	feeds         storage.FeedStore
	entries       storage.EntryStore
	filters       storage.FilterStore
	filterMatches storage.FilterMatchStore
	labels        storage.LabelStore
	webhooks      storage.WebhookStore
	webhookLogs   storage.WebhookLogStore
	users         storage.UserStore
	sessions      storage.SessionStore
	filterEngine  *filter.Engine
	authToken     string
	adminUsername string
	adminPassword string
	metricsToken  string

	refresher      *service.FeedRefresher
	contentFetcher *service.ContentFetcher
	ssrfGuard      *ssrf.Guard

	webhookHTTPClient  *http.Client
	webhookMaxAttempts int
	wsEnabled          bool
	wsHub              *ws.Hub

	rateLimiter           *middleware.Limiter
	loginLimiter          *middleware.Limiter
	maxRequestBodyBytes   int64
	compressEnabled       bool
	hstsEnabled           bool
	maxImportFeeds        int
	importJobs            *importJobManager
	uiEnabled             bool
	csrfSecret            string
	workerPoolSize        int
	webhookWorkerPoolSize int
	fetchTimeoutSec       int
	envFilePath           string
	databaseURL           string
	gitHubRepo            string
	handlerRegistry       *reader.HandlerRegistry
	titleResolver         *reader.TitleResolver
	bridgeManager         *bridgeconfig.Manager
	refreshAll            refreshAllEnqueuer
	workerControl         WorkerControl
}

func New(dep Dependencies) *Server {
	log := dep.Logger
	if log == nil {
		log = slog.Default()
	}
	categoryStore := dep.CategoryStore
	feedStore := dep.FeedStore
	entryStore := dep.EntryStore
	filterStore := dep.FilterStore
	filterMatchStore := dep.FilterMatchStore
	labelStore := dep.LabelStore
	webhookStore := dep.WebhookStore
	webhookLogStore := dep.WebhookLogStore
	if dep.DB != nil {
		pgStore := storage.NewPostgresStore(dep.DB)
		if categoryStore == nil {
			categoryStore = pgStore
		}
		if feedStore == nil {
			feedStore = pgStore
		}
		if entryStore == nil {
			entryStore = pgStore
		}
		if filterStore == nil {
			filterStore = pgStore
		}
		if filterMatchStore == nil {
			filterMatchStore = pgStore
		}
		if labelStore == nil {
			labelStore = pgStore
		}
		if webhookStore == nil {
			webhookStore = pgStore
		}
		if webhookLogStore == nil {
			webhookLogStore = pgStore
		}
	}
	userStore := dep.UserStore
	sessionStore := dep.SessionStore
	if dep.DB != nil {
		pgStore := storage.NewPostgresStore(dep.DB)
		if userStore == nil {
			userStore = pgStore
		}
		if sessionStore == nil {
			sessionStore = pgStore
		}
	}

	fetchTimeout := 15 * time.Second
	if dep.FetchTimeoutSec > 0 {
		fetchTimeout = time.Duration(dep.FetchTimeoutSec) * time.Second
	}

	guard := dep.SSRFGuard
	if guard == nil {
		var guardErr error
		guard, guardErr = ssrf.New(ssrf.Config{
			AllowPrivateNetwork:   dep.FetchAllowPrivateNet,
			AllowedCIDRs:          dep.FetchAllowedCIDRs,
			BlockedHosts:          dep.FetchBlockedHosts,
			TLSInsecureSkipVerify: dep.FetchTLSInsecureSkipVerify,
		})
		if guardErr != nil {
			log.Error("ssrf guard init failed", "err", guardErr)
		}
	}

	client := dep.HTTPClient
	if client == nil && guard != nil {
		client = guard.HTTPClient(fetchTimeout)
	} else if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}
	webhookClient := dep.WebhookHTTPClient
	if webhookClient == nil && guard != nil {
		webhookClient = guard.HTTPClient(10 * time.Second)
	} else if webhookClient == nil {
		webhookClient = &http.Client{Timeout: 10 * time.Second}
	}
	wsHub := dep.WSHub
	if wsHub == nil {
		wsHub = ws.NewHub(dep.WSClientBuffer, dep.WSPingInterval)
	}

	registry := dep.HandlerRegistry
	if registry == nil {
		var regErr error
		var bundle *reader.RegistryBundle
		bundle, regErr = reader.NewRegistry(reader.RegistryConfig{
			HTTPClient:           client,
			SSRFGuard:            guard,
			UserAgent:            dep.FetchUserAgent,
			FetchAllowPrivateNet: dep.FetchAllowPrivateNet,
		})
		if regErr != nil {
			log.Error("handler registry init failed", "err", regErr)
		} else if bundle != nil {
			registry = bundle.Registry
		}
	}
	dedupStore := dep.DedupStore
	if dedupStore == nil && dep.DB != nil {
		dedupStore = storage.NewPostgresStore(dep.DB)
	}
	refresher := &service.FeedRefresher{
		Feeds:                   feedStore,
		Entries:                 entryStore,
		Dedup:                   dedupStore,
		Registry:                registry,
		Filters:                 filterStore,
		Matches:                 filterMatchStore,
		Labels:                  labelStore,
		Engine:                  dep.FilterEngine,
		Webhooks:                webhookStore,
		WebhookLogs:             webhookLogStore,
		StoreEntriesMode:        dep.StoreEntriesMode,
		CircuitBreakerThreshold: dep.CircuitBreakerThreshold,
		MinPollInterval:         dep.MinPollInterval,
		MaxPollInterval:         dep.MaxPollInterval,
	}
	if dep.WSEnabled {
		refresher.Realtime = ws.NewPublisher(log, wsHub, entryStore)
	}

	var contentFetcher *service.ContentFetcher
	if feedStore != nil && entryStore != nil && guard != nil {
		maxBytes := dep.ScraperMaxContentBytes
		if maxBytes <= 0 {
			maxBytes = 1 << 20
		}
		contentFetcher = &service.ContentFetcher{
			Feeds:   feedStore,
			Entries: entryStore,
			Scraper: scraper.NewFetcher(scraper.Options{
				HTTPClient:       client,
				UserAgent:        dep.FetchUserAgent,
				MaxBodyBytes:     maxBytes,
				SSRFGuard:        guard,
				FetchViaProxyURL: dep.FetchViaProxyURL,
			}),
		}
	}

	maxBody := dep.MaxRequestBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}

	jobStore := dep.RefreshAll
	if jobStore == nil && dep.DB != nil {
		jobStore = storage.NewPostgresStore(dep.DB)
	}

	return &Server{
		log:                log,
		db:                 dep.DB,
		categories:         categoryStore,
		feeds:              feedStore,
		entries:            entryStore,
		filters:            filterStore,
		filterMatches:      filterMatchStore,
		labels:             labelStore,
		webhooks:           webhookStore,
		webhookLogs:        webhookLogStore,
		users:              userStore,
		sessions:           sessionStore,
		filterEngine:       dep.FilterEngine,
		authToken:          dep.AuthToken,
		adminUsername:      dep.AdminUsername,
		adminPassword:      dep.AdminPassword,
		metricsToken:       dep.MetricsToken,
		refresher:          refresher,
		contentFetcher:     contentFetcher,
		ssrfGuard:          guard,
		webhookHTTPClient:  webhookClient,
		webhookMaxAttempts: dep.WebhookMaxAttempts,
		wsEnabled:          dep.WSEnabled,
		wsHub:              wsHub,

		rateLimiter:           middleware.NewLimiter(dep.RateLimitEnabled, dep.RateLimitRPS, dep.RateLimitBurst),
		loginLimiter:          middleware.NewLimiter(dep.RateLimitEnabled, dep.LoginRateLimitRPS, dep.LoginRateLimitBurst),
		maxRequestBodyBytes:   maxBody,
		compressEnabled:       dep.CompressEnabled,
		hstsEnabled:           dep.HSTSEnabled,
		maxImportFeeds:        dep.MaxImportFeeds,
		importJobs:            newImportJobManager(),
		uiEnabled:             dep.UIEnabled,
		csrfSecret:            dep.CSRFSecret,
		workerPoolSize:        dep.WorkerPoolSize,
		webhookWorkerPoolSize: dep.WebhookWorkerPoolSize,
		fetchTimeoutSec:       dep.FetchTimeoutSec,
		envFilePath:           dep.EnvFilePath,
		databaseURL:           dep.DatabaseURL,
		gitHubRepo:            dep.GitHubRepo,
		handlerRegistry:       registry,
		titleResolver:         dep.TitleResolver,
		bridgeManager:         dep.BridgeManager,
		refreshAll:            jobStore,
		workerControl:         dep.WorkerControl,
	}
}

func (s *Server) Run(ctx context.Context, addr string, shutdownTimeout time.Duration) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/openapi.json", s.handleOpenAPISpec)
	mux.Handle("/metrics", s.wrapMetrics(promhttp.Handler()))

	api := http.NewServeMux()
	api.HandleFunc("GET /v1/categories", s.handleListCategories)
	api.HandleFunc("POST /v1/categories", s.handleCreateCategory)
	api.HandleFunc("PUT /v1/categories/{id}", s.handleUpdateCategory)
	api.HandleFunc("DELETE /v1/categories/{id}", s.handleDeleteCategory)
	api.HandleFunc("PUT /v1/categories/{categoryID}/mark-all-as-read", s.handleMarkCategoryAllRead)
	api.HandleFunc("GET /v1/feeds", s.handleListFeeds)
	api.HandleFunc("POST /v1/feeds", s.handleCreateFeed)
	api.HandleFunc("GET /v1/feeds/import/jobs/{jobID}", s.handleGetImportJob)
	api.HandleFunc("POST /v1/feeds/import", s.handleImportFeeds)
	api.HandleFunc("GET /v1/feeds/export", s.handleExportFeeds)
	api.HandleFunc("POST /v1/feeds/refresh", s.handleRefreshAllFeeds)
	api.HandleFunc("GET /v1/feeds/{id}", s.handleGetFeed)
	api.HandleFunc("PUT /v1/feeds/{id}", s.handleUpdateFeed)
	api.HandleFunc("DELETE /v1/feeds/{id}", s.handleDeleteFeed)
	api.HandleFunc("PUT /v1/feeds/{feedID}/mark-all-as-read", s.handleMarkFeedAllRead)
	api.HandleFunc("GET /v1/feeds/{feedID}/icon", s.handleFeedIcon)
	api.HandleFunc("POST /v1/feeds/{feedID}/refresh", s.handleRefreshFeed)
	api.HandleFunc("GET /v1/feeds/{feedID}/entries", s.handleListFeedEntries)
	api.HandleFunc("GET /v1/feeds/{feedID}/entries/{entryID}", s.handleGetFeedEntry)
	api.HandleFunc("PUT /v1/feeds/{feedID}/entries/{entryID}", s.handleUpdateFeedEntry)
	api.HandleFunc("GET /v1/entries", s.handleListEntries)
	api.HandleFunc("PUT /v1/entries", s.handleBulkUpdateEntries)
	api.HandleFunc("GET /v1/entries/{id}", s.handleGetEntry)
	api.HandleFunc("GET /v1/entries/{id}/fetch-content", s.handleFetchEntryContent)
	api.HandleFunc("GET /v1/filters", s.handleListFilters)
	api.HandleFunc("POST /v1/filters", s.handleCreateFilter)
	api.HandleFunc("GET /v1/filters/{id}", s.handleGetFilter)
	api.HandleFunc("PUT /v1/filters/{id}", s.handleUpdateFilter)
	api.HandleFunc("DELETE /v1/filters/{id}", s.handleDeleteFilter)
	api.HandleFunc("POST /v1/filters/{id}/test", s.handleTestFilter)
	api.HandleFunc("GET /v1/filters/{id}/matches", s.handleListFilterMatches)
	api.HandleFunc("GET /v1/webhooks", s.handleListWebhooks)
	api.HandleFunc("POST /v1/webhooks", s.handleCreateWebhook)
	api.HandleFunc("GET /v1/webhooks/{id}", s.handleGetWebhook)
	api.HandleFunc("PUT /v1/webhooks/{id}", s.handleUpdateWebhook)
	api.HandleFunc("DELETE /v1/webhooks/{id}", s.handleDeleteWebhook)
	api.HandleFunc("POST /v1/webhooks/{id}/test", s.handleTestWebhook)
	api.HandleFunc("GET /v1/webhooks/{id}/logs", s.handleListWebhookLogs)
	api.HandleFunc("POST /v1/webhook-logs/{logID}/retry", s.handleRetryWebhookLog)
	api.HandleFunc("GET /v1/users", s.handleListUsers)
	api.HandleFunc("POST /v1/users", s.handleCreateUser)
	api.HandleFunc("DELETE /v1/users/{id}", s.handleDeleteUser)
	api.HandleFunc("GET /v1/me", s.handleGetMe)
	api.HandleFunc("PUT /v1/me", s.handleUpdateMe)
	api.HandleFunc("GET /v1/me/api-keys", s.handleListAPIKeys)
	api.HandleFunc("POST /v1/me/api-keys", s.handleCreateAPIKey)
	api.HandleFunc("DELETE /v1/me/api-keys/{id}", s.handleDeleteAPIKey)
	api.HandleFunc("GET /v1/system/info", s.handleSystemInfo)
	mux.Handle("/v1/", s.wrapAPI(api))
	mux.HandleFunc("GET /ws/v1", s.handleWS)

	s.registerUI(mux)

	handler := s.wrapMiddleware(mux)

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		// Bound slow readers and idle keep-alives; without these a handful of
		// stalled connections can pin goroutines indefinitely. WebSocket
		// connections are hijacked and therefore unaffected by WriteTimeout.
		WriteTimeout:   120 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == nil {
			return nil
		}
		return fmt.Errorf("listen: %w", err)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "db not configured", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		http.Error(w, "db ping failed", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) wrapMiddleware(next http.Handler) http.Handler {
	h := middleware.SensitiveRateLimit(s.rateLimiter)(next)
	h = middleware.BodyLimit(s.maxRequestBodyBytes)(h)
	h = middleware.Compress(s.compressEnabled)(h)
	h = middleware.SecurityHeaders(middleware.SecurityConfig{HSTSEnabled: s.hstsEnabled})(h)
	h = middleware.AccessLog(s.log)(h)
	return h
}

func (s *Server) wrapMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metricsToken == "" {
			writeError(w, http.StatusForbidden, "metrics disabled")
			return
		}
		if !hasBearerToken(r, s.metricsToken) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasBearerToken(r *http.Request, token string) bool {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if len(v) <= len(prefix) || v[:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(v[len(prefix):]), []byte(token)) == 1
}

type listResponse[T any] struct {
	Data  T   `json:"data"`
	Total int `json:"total"`
}

type errorResponse struct {
	ErrorMessage string `json:"error_message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{ErrorMessage: msg})
}

type feedDTO struct {
	ID                 int64  `json:"id"`
	FeedURL            string `json:"feed_url"`
	FeedType           string `json:"feed_type,omitempty"`
	Title              string `json:"title"`
	CategoryID         *int64 `json:"category_id,omitempty"`
	IntervalMinutes    int    `json:"interval_minutes"`
	ScraperRules       string `json:"scraper_rules,omitempty"`
	RewriteRules       string `json:"rewrite_rules,omitempty"`
	BlockedRules       string `json:"blocked_rules,omitempty"`
	KeepRules          string `json:"keep_rules,omitempty"`
	FetchViaProxy      bool   `json:"fetch_via_proxy"`
	TLSInsecure        bool   `json:"tls_insecure"`
	Crawler            bool   `json:"crawler"`
	UserAgent          string `json:"user_agent,omitempty"`
	StoreHashOnly      bool   `json:"store_hash_only,omitempty"`
	EntryRetentionDays *int   `json:"entry_retention_days,omitempty"`
}

type categoryDTO struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Color string `json:"color,omitempty"`
}

type categoryWriteRequest struct {
	Title string `json:"title"`
	Color string `json:"color"`
}

type feedWriteRequest struct {
	FeedURL            string `json:"feed_url"`
	Title              string `json:"title"`
	CategoryID         *int64 `json:"category_id"`
	IntervalMinutes    int    `json:"interval_minutes"`
	ScraperRules       string `json:"scraper_rules"`
	RewriteRules       string `json:"rewrite_rules"`
	BlockedRules       string `json:"blocked_rules"`
	KeepRules          string `json:"keep_rules"`
	FetchViaProxy      bool   `json:"fetch_via_proxy"`
	TLSInsecure        bool   `json:"tls_insecure"`
	Crawler            bool   `json:"crawler"`
	UserAgent          string `json:"user_agent"`
	StoreHashOnly      bool   `json:"store_hash_only,omitempty"`
	EntryRetentionDays *int   `json:"entry_retention_days,omitempty"`
}

type deletedDTO struct {
	Deleted bool `json:"deleted"`
}

const defaultIntervalMinutes = 60

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	categories, total, err := s.categories.ListCategories(r.Context(), p.UserID, limit, offset)
	if err != nil {
		s.log.Error("list categories failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]categoryDTO, 0, len(categories))
	for _, c := range categories {
		out = append(out, categoryDTO{
			ID:    c.ID,
			Title: c.Title,
			Color: c.Color,
		})
	}
	writeJSON(w, http.StatusOK, listResponse[[]categoryDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
		return
	}
	var req categoryWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	category, err := s.categories.CreateCategory(r.Context(), p.UserID, req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		s.log.Error("create category failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color},
		Total: 1,
	})
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req categoryWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	category, err := s.categories.UpdateCategory(r.Context(), p.UserID, id, req.Title, strings.TrimSpace(req.Color))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		s.log.Error("update category failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[categoryDTO]{
		Data:  categoryDTO{ID: category.ID, Title: category.Title, Color: category.Color},
		Total: 1,
	})
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.categories == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "category storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.categories.DeleteCategory(r.Context(), p.UserID, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		s.log.Error("delete category failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feeds, total, err := s.feeds.ListFeeds(r.Context(), p.UserID, limit, offset)
	if err != nil {
		s.log.Error("list feeds failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out := make([]feedDTO, 0, len(feeds))
	for _, f := range feeds {
		out = append(out, toFeedDTO(f))
	}
	writeJSON(w, http.StatusOK, listResponse[[]feedDTO]{Data: out, Total: total})
}

func (s *Server) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	var req feedWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := validateFeedWriteRequest(req, s.ssrfGuard)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if params.Title == "" && s.titleResolver != nil {
		if t, err := s.titleResolver.DiscoverTitle(r.Context(), params.FeedURL, params.FeedType, params.TLSInsecure); err == nil && strings.TrimSpace(t) != "" {
			params.Title = strings.TrimSpace(t)
		}
	}
	if params.Title == "" {
		params.Title = params.FeedURL
	}
	feed, err := s.feeds.CreateFeed(r.Context(), p.UserID, params)
	if err != nil {
		if errors.Is(err, storage.ErrDuplicateFeedURL) {
			writeError(w, http.StatusConflict, "feed_url already exists")
			return
		}
		if errors.Is(err, storage.ErrInvalidReference) {
			writeError(w, http.StatusBadRequest, "invalid category_id")
			return
		}
		s.log.Error("create feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, listResponse[feedDTO]{Data: toFeedDTO(feed), Total: 1})
}

func (s *Server) handleGetFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feed, err := s.feeds.GetFeed(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.Error("get feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[feedDTO]{Data: toFeedDTO(feed), Total: 1})
}

func (s *Server) handleUpdateFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req feedWriteRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	params, err := validateFeedWriteRequest(req, s.ssrfGuard)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feed, err := s.feeds.UpdateFeed(r.Context(), p.UserID, storage.UpdateFeedParams{
		ID:                 id,
		FeedURL:            params.FeedURL,
		Title:              params.Title,
		CategoryID:         params.CategoryID,
		IntervalMinutes:    params.IntervalMinutes,
		ScraperRules:       params.ScraperRules,
		RewriteRules:       params.RewriteRules,
		BlockedRules:       params.BlockedRules,
		KeepRules:          params.KeepRules,
		FetchViaProxy:      params.FetchViaProxy,
		TLSInsecure:        params.TLSInsecure,
		Crawler:            params.Crawler,
		UserAgent:          params.UserAgent,
		StoreHashOnly:      params.StoreHashOnly,
		EntryRetentionDays: params.EntryRetentionDays,
	})
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeError(w, http.StatusNotFound, "feed not found")
			return
		case errors.Is(err, storage.ErrDuplicateFeedURL):
			writeError(w, http.StatusConflict, "feed_url already exists")
			return
		case errors.Is(err, storage.ErrInvalidReference):
			writeError(w, http.StatusBadRequest, "invalid category_id")
			return
		default:
			s.log.Error("update feed failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	if s.refresher != nil {
		_ = s.refresher.RescheduleFeed(r.Context(), id, params.IntervalMinutes)
	}
	writeJSON(w, http.StatusOK, listResponse[feedDTO]{Data: toFeedDTO(feed), Total: 1})
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireAdmin(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.feeds.DeleteFeed(r.Context(), p.UserID, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.Error("delete feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[deletedDTO]{Data: deletedDTO{Deleted: true}, Total: 1})
}

func parseLimitOffset(r *http.Request, defLimit, capLimit int) (limit, offset int, err error) {
	q := r.URL.Query()
	limit = defLimit
	offset = 0

	if v := q.Get("limit"); v != "" {
		limit, err = atoiNonNeg(v)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid limit")
		}
	}
	if v := q.Get("offset"); v != "" {
		offset, err = atoiNonNeg(v)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid offset")
		}
	}
	if limit > capLimit {
		limit = capLimit
	}
	return limit, offset, nil
}

func atoiNonNeg(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func decodeJSONBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if middleware.IsBodyTooLarge(err) {
			return errors.New("request body too large")
		}
		return errors.New("invalid JSON body")
	}
	return nil
}

func parsePathID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.PathValue("id"))
	if raw == "" {
		return 0, errors.New("id is required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func validateFeedWriteRequest(req feedWriteRequest, guard *ssrf.Guard) (storage.CreateFeedParams, error) {
	feedURL := strings.TrimSpace(req.FeedURL)
	if err := validateFeedURL(feedURL, guard); err != nil {
		return storage.CreateFeedParams{}, err
	}
	interval := req.IntervalMinutes
	if interval == 0 {
		interval = defaultIntervalMinutes
	}
	if interval < storage.MinFeedIntervalMinutes || interval > storage.MaxFeedIntervalMinutes {
		return storage.CreateFeedParams{}, fmt.Errorf("interval_minutes must be between %d and %d", storage.MinFeedIntervalMinutes, storage.MaxFeedIntervalMinutes)
	}
	feedType := reader.DetectFeedTypeFromURL(feedURL)
	if err := storage.ValidateEntryRetentionDays(req.EntryRetentionDays); err != nil {
		return storage.CreateFeedParams{}, err
	}
	return storage.CreateFeedParams{
		FeedURL:            feedURL,
		FeedType:           feedType,
		Title:              strings.TrimSpace(req.Title),
		CategoryID:         req.CategoryID,
		IntervalMinutes:    interval,
		ScraperRules:       strings.TrimSpace(req.ScraperRules),
		RewriteRules:       req.RewriteRules,
		BlockedRules:       req.BlockedRules,
		KeepRules:          req.KeepRules,
		FetchViaProxy:      req.FetchViaProxy,
		TLSInsecure:        req.TLSInsecure,
		Crawler:            req.Crawler,
		UserAgent:          strings.TrimSpace(req.UserAgent),
		StoreHashOnly:      req.StoreHashOnly,
		EntryRetentionDays: req.EntryRetentionDays,
	}, nil
}

func toFeedDTO(f storage.Feed) feedDTO {
	ft := strings.TrimSpace(f.FeedType)
	if ft == "" {
		ft = reader.FeedTypeRSS
	}
	return feedDTO{
		ID:                 f.ID,
		FeedURL:            f.FeedURL,
		FeedType:           ft,
		Title:              f.Title,
		CategoryID:         f.CategoryID,
		IntervalMinutes:    f.IntervalMinutes,
		ScraperRules:       f.ScraperRules,
		RewriteRules:       f.RewriteRules,
		BlockedRules:       f.BlockedRules,
		KeepRules:          f.KeepRules,
		FetchViaProxy:      f.FetchViaProxy,
		TLSInsecure:        f.TLSInsecure,
		Crawler:            f.Crawler,
		UserAgent:          f.UserAgent,
		StoreHashOnly:      f.StoreHashOnly,
		EntryRetentionDays: f.EntryRetentionDays,
	}
}

type entryDTO struct {
	ID              int64          `json:"id"`
	FeedID          int64          `json:"feed_id"`
	Title           string         `json:"title"`
	URL             string         `json:"url"`
	Content         string         `json:"content"`
	OriginalContent string         `json:"original_content,omitempty"`
	ContentFetched  bool           `json:"content_fetched"`
	Author          *string        `json:"author,omitempty"`
	PublishedAt     *time.Time     `json:"published_at,omitempty"`
	Hash            string         `json:"hash"`
	Status          string         `json:"status"`
	Starred         bool           `json:"starred"`
	Enclosures      []enclosureDTO `json:"enclosures,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func toEntryDTO(e storage.Entry, encs []storage.Enclosure) entryDTO {
	return entryDTO{
		ID:              e.ID,
		FeedID:          e.FeedID,
		Title:           e.Title,
		URL:             e.URL,
		Content:         e.Content,
		OriginalContent: e.OriginalContent,
		ContentFetched:  e.ContentFetched,
		Author:          e.Author,
		PublishedAt:     e.PublishedAt,
		Hash:            e.Hash,
		Status:          e.Status,
		Starred:         e.Starred,
		Enclosures:      toEnclosureDTOs(encs),
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter, searchQuery, err := parseEntriesFilter(r, limit, offset, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var entries []storage.Entry
	var total int
	if searchQuery != "" {
		entries, total, err = s.entries.SearchEntries(r.Context(), p.UserID, storage.SearchEntriesFilter{
			Query:   searchQuery,
			FeedID:  filter.FeedID,
			Status:  filter.Status,
			Starred: filter.Starred,
			Sort:    filter.Sort,
			Limit:   filter.Limit,
			Offset:  filter.Offset,
			Rank:    true,
		})
		if err != nil {
			s.log.Error("search entries failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	} else {
		entries, total, err = s.entries.ListEntries(r.Context(), p.UserID, filter)
		if err != nil {
			s.log.Error("list entries failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	out, err := s.entriesToDTOs(r.Context(), p.UserID, entries)
	if err != nil {
		s.log.Error("load entry enclosures failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[[]entryDTO]{Data: out, Total: total})
}

func (s *Server) handleGetEntry(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	id, err := parsePathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entry, err := s.entries.GetEntry(r.Context(), p.UserID, id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "entry not found")
			return
		}
		s.log.Error("get entry failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	dto, err := s.entryToDTO(r.Context(), p.UserID, entry)
	if err != nil {
		s.log.Error("load entry enclosures failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[entryDTO]{Data: dto, Total: 1})
}

func (s *Server) handleListFeedEntries(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.entries == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		}
		return
	}
	feedID, err := parsePathInt64(r, "feedID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, offset, err := parseLimitOffset(r, 100, 10000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter, _, err := parseEntriesFilter(r, limit, offset, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, total, err := s.entries.ListFeedEntries(r.Context(), p.UserID, feedID, filter)
	if err != nil {
		s.log.Error("list feed entries failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	out, err := s.entriesToDTOs(r.Context(), p.UserID, entries)
	if err != nil {
		s.log.Error("load entry enclosures failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, listResponse[[]entryDTO]{Data: out, Total: total})
}

func parseEntriesFilter(r *http.Request, limit, offset int, allowFeedID bool) (storage.ListEntriesFilter, string, error) {
	q := r.URL.Query()
	filter := storage.ListEntriesFilter{Limit: limit, Offset: offset}
	searchQuery := strings.TrimSpace(q.Get("q"))

	if allowFeedID {
		if v := strings.TrimSpace(q.Get("feed_id")); v != "" {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil || id <= 0 {
				return storage.ListEntriesFilter{}, "", errors.New("invalid feed_id")
			}
			filter.FeedID = &id
		}
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		if !storage.IsValidEntryStatus(v) {
			return storage.ListEntriesFilter{}, "", errors.New("invalid status")
		}
		filter.Status = &v
	}
	if v := strings.TrimSpace(q.Get("starred")); v != "" {
		starred, err := strconv.ParseBool(v)
		if err != nil {
			return storage.ListEntriesFilter{}, "", errors.New("invalid starred")
		}
		filter.Starred = &starred
	}
	filter.Sort = storage.NormalizeEntrySort(q.Get("sort"))
	return filter, searchQuery, nil
}

func parsePathInt64(r *http.Request, key string) (int64, error) {
	raw := strings.TrimSpace(r.PathValue(key))
	if raw == "" {
		return 0, errors.New("id is required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

type refreshResultDTO struct {
	Inserted int `json:"inserted"`
}

func (s *Server) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok {
		return
	}
	if s.feeds == nil {
		writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		return
	}
	if s.entries == nil {
		writeError(w, http.StatusServiceUnavailable, "entry storage is not configured")
		return
	}
	feedID, err := parsePathInt64(r, "feedID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if _, err := s.feeds.GetFeed(ctx, p.UserID, feedID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.Error("refresh feed lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	inserted, err := s.refresher.RefreshFeedManual(ctx, feedID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		if errors.Is(err, service.ErrFetchFeed) {
			writeError(w, http.StatusBadGateway, "feed fetch failed")
			return
		}
		s.log.Error("refresh feed failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, listResponse[refreshResultDTO]{Data: refreshResultDTO{Inserted: inserted}, Total: 1})
}
