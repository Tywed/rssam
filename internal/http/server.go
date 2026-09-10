package httpserver

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"rssam/internal/bridgeconfig"
	"rssam/internal/filter"
	"rssam/internal/http/middleware"
	"rssam/internal/http/ui"
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
	SessionMaxAge       time.Duration
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

	// Retention settings shown on the admin system page and the hook that
	// runs one cleanup pass on demand (nil = unavailable).
	Retention           ui.RetentionSettings
	RunRetentionCleanup func(ctx context.Context) (storage.RetentionCleanupResult, error)

	HandlerRegistry *reader.HandlerRegistry
	TitleResolver   *reader.TitleResolver
	BridgeManager   *bridgeconfig.Manager
	RefreshAll      refreshAllEnqueuer

	MinPollInterval  time.Duration
	MaxPollInterval  time.Duration
	StoreEntriesMode string

	DedupStore              storage.EntryDedupStore
	CircuitBreakerThreshold int
	// FeedPollLogStore records poll history for manual refreshes triggered
	// over HTTP/UI (nil = derived from DB when available).
	FeedPollLogStore storage.FeedPollLogStore

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
	feedPollLog    storage.FeedPollLogStore
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
	sessionMaxAge         time.Duration
	maxImportFeeds        int
	importJobs            *importJobManager
	uiEnabled             bool
	csrfSecret            string
	workerPoolSize        int
	webhookWorkerPoolSize int
	fetchTimeoutSec       int
	envFilePath           string
	retention             ui.RetentionSettings
	runRetentionCleanup   func(ctx context.Context) (storage.RetentionCleanupResult, error)
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
	pollLog := dep.FeedPollLogStore
	if pollLog == nil && dep.DB != nil {
		pollLog = storage.NewPostgresStore(dep.DB)
	}
	refresher := &service.FeedRefresher{
		Feeds:                   feedStore,
		Entries:                 entryStore,
		Dedup:                   dedupStore,
		PollLog:                 pollLog,
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
		feedPollLog:        pollLog,
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
		sessionMaxAge:         dep.SessionMaxAge,
		maxImportFeeds:        dep.MaxImportFeeds,
		importJobs:            newImportJobManager(),
		uiEnabled:             dep.UIEnabled,
		csrfSecret:            dep.CSRFSecret,
		workerPoolSize:        dep.WorkerPoolSize,
		webhookWorkerPoolSize: dep.WebhookWorkerPoolSize,
		fetchTimeoutSec:       dep.FetchTimeoutSec,
		envFilePath:           dep.EnvFilePath,
		retention:             dep.Retention,
		runRetentionCleanup:   dep.RunRetentionCleanup,
		databaseURL:           dep.DatabaseURL,
		gitHubRepo:            dep.GitHubRepo,
		handlerRegistry:       registry,
		titleResolver:         dep.TitleResolver,
		bridgeManager:         dep.BridgeManager,
		refreshAll:            jobStore,
		workerControl:         dep.WorkerControl,
	}
}

// Handler builds the complete HTTP handler: public endpoints, the /v1 API
// behind wrapAPI, the WebSocket endpoint, the UI, and the outer middleware
// chain. Run serves it; tests can drive it directly.
func (s *Server) Handler() http.Handler {
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

	return s.wrapMiddleware(mux)
}

func (s *Server) Run(ctx context.Context, addr string, shutdownTimeout time.Duration) error {
	handler := s.Handler()

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
	h = middleware.RequestID(h) // outermost: the access log must see the id
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
