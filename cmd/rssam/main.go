package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"rssam/internal/bridgeconfig"
	"rssam/internal/config"
	"rssam/internal/envfile"
	"rssam/internal/filter"
	httpserver "rssam/internal/http"
	"rssam/internal/http/middleware"
	"rssam/internal/logger"
	"rssam/internal/metrics"
	"rssam/internal/migrations"
	"rssam/internal/ops"
	"rssam/internal/proxy"
	"rssam/internal/reader"
	"rssam/internal/service"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
	"rssam/internal/version"
	"rssam/internal/worker"
	"rssam/internal/ws"
)

func main() {
	var (
		printVersion = flag.Bool("version", false, "print version and exit")
		runMigrate   = flag.Bool("migrate", false, "apply database migrations and exit")
		listenAddr   = flag.String("listen", "", "HTTP listen address (overrides LISTEN_ADDR)")
	)
	flag.Parse()

	if *printVersion {
		fmt.Println(version.String())
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *listenAddr != "" {
		cfg.ListenAddr = *listenAddr
	}

	log := logger.New(cfg.LogLevel, cfg.LogFormat)

	if err := middleware.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Error("invalid TRUSTED_PROXIES", "err", err)
		os.Exit(2)
	}

	db, err := storage.NewPostgresPool(ctx, cfg.DatabaseURL, cfg.EffectiveDatabaseMaxConns())
	if err != nil {
		log.Error("database connection failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if *runMigrate || cfg.RunMigrations {
		if err := migrations.Apply(ctx, db, log); err != nil {
			log.Error("migrations failed", "err", err)
			os.Exit(1)
		}
		if *runMigrate {
			return
		}
	}

	metrics.Init()
	pgStore := storage.NewPostgresStore(db, cfg.FTSLanguage)
	if err := pgStore.EnsureBootstrapAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Error("bootstrap admin failed", "err", err)
		os.Exit(1)
	}
	wsHub := ws.NewHub(cfg.WSClientBuffer, cfg.WSPingInterval)
	wsPublisher := ws.NewPublisher(log, wsHub, pgStore)

	fetchTimeout := 15 * time.Second
	if cfg.FetchTimeoutSeconds > 0 {
		fetchTimeout = time.Duration(cfg.FetchTimeoutSeconds) * time.Second
	}

	var ssrfAllowedHosts []string
	if h, err := proxy.ServiceHost(cfg.TelegramProxyServiceURL); err == nil && h != "" {
		ssrfAllowedHosts = append(ssrfAllowedHosts, h)
	}
	ssrfGuard, err := ssrf.New(ssrf.Config{
		AllowPrivateNetwork:   cfg.FetchAllowPrivateNetwork,
		AllowedCIDRs:          cfg.FetchAllowedCIDRs,
		BlockedHosts:          cfg.FetchBlockedHosts,
		AllowedHosts:          ssrfAllowedHosts,
		TLSInsecureSkipVerify: cfg.FetchTLSInsecureSkipVerify,
	})
	if err != nil {
		log.Error("ssrf guard init failed", "err", err)
		os.Exit(1)
	}
	if cfg.FetchTLSInsecureSkipVerify {
		log.Warn("FETCH_TLS_INSECURE is enabled: TLS certificate verification is disabled for outbound fetches")
	}

	httpClient := ssrfGuard.HTTPClient(fetchTimeout)
	webhookClient := ssrfGuard.HTTPClient(cfg.WebhookTimeout)

	registryBundle, err := reader.NewRegistry(reader.RegistryConfig{
		HTTPClient:           httpClient,
		SSRFGuard:            ssrfGuard,
		UserAgent:            cfg.FetchUserAgent,
		FetchAllowPrivateNet: cfg.FetchAllowPrivateNetwork,
		FetchViaProxyURL:     cfg.FetchViaProxyURL,
		FetchTLSInsecure:     cfg.FetchTLSInsecureSkipVerify,
		MaxAPIBaseURL:        cfg.MaxAPIBaseURL,
		MaxDefaultLimit:      cfg.MaxDefaultLimit,
		MaxDefaultLookback:   cfg.MaxDefaultLookback,
		MaxOverlap:           cfg.MaxOverlap,
		MaxRateLimitSeconds:  cfg.MaxRateLimitSeconds,
		MaxRequestIntervalMs: cfg.MaxRequestIntervalMs,
		MaxConcurrentSlots:   cfg.MaxConcurrentSlots,
		MaxAllowPrivateAPI:   cfg.MaxAllowPrivateAPI,

		MaxstatAccessToken:      cfg.MaxstatAccessToken,
		MaxstatAPIBaseURL:       cfg.MaxstatAPIBaseURL,
		MaxstatDefaultLimit:     cfg.MaxstatDefaultLimit,
		MaxstatDefaultLookback:  cfg.MaxstatDefaultLookback,
		MaxstatOverlap:          cfg.MaxstatOverlap,
		MaxstatRateLimitSeconds: cfg.MaxstatRateLimitSeconds,

		TelegramProxyServiceURL:     cfg.TelegramProxyServiceURL,
		TelegramProxyServiceToken:   cfg.TelegramProxyServiceToken,
		TelegramProxyTargetURL:      cfg.TelegramProxyTargetURL,
		TelegramStaticProxy:         cfg.TelegramStaticProxy,
		TelegramProxyConnectTimeout: cfg.TelegramProxyConnectTimeout,
		TelegramProxyRequestTimeout: cfg.TelegramProxyRequestTimeout,
		TelegramProxyRetry:          cfg.TelegramProxyRetry,
		TelegramMaxPages:            cfg.TelegramMaxPages,

		VKAccessToken:      cfg.VKAccessToken,
		VKAPIVersion:       cfg.VKAPIVersion,
		VKDefaultCount:     cfg.VKDefaultCount,
		VKDefaultLookback:  cfg.VKDefaultLookback,
		VKOverlap:          cfg.VKOverlap,
		VKRateLimitSeconds: cfg.VKRateLimitSeconds,

		RutubeAPIBaseURL: cfg.RutubeAPIBaseURL,

		DzenSearchURL: cfg.DzenSearchURL,
		DzenUserAgent: cfg.DzenUserAgent,
		DzenCookie:    cfg.DzenCookie,

		SmotrimUserAgent: cfg.FetchUserAgent,
	})
	if err != nil {
		log.Error("handler registry init failed", "err", err)
		os.Exit(1)
	}

	bridgeMgr := bridgeconfig.NewManager(cfg, pgStore, bridgeconfig.Handlers{
		Telegram: registryBundle.Telegram,
		Max:      registryBundle.Max,
		VK:       registryBundle.VK,
		Rutube:   registryBundle.Rutube,
	})
	if err := bridgeMgr.Load(ctx); err != nil {
		log.Error("bridge settings load failed", "err", err)
		os.Exit(1)
	}

	titleResolver, err := reader.NewTitleResolver(reader.RegistryConfig{
		HTTPClient:                  httpClient,
		SSRFGuard:                   ssrfGuard,
		UserAgent:                   cfg.FetchUserAgent,
		FetchAllowPrivateNet:        cfg.FetchAllowPrivateNetwork,
		FetchViaProxyURL:            cfg.FetchViaProxyURL,
		FetchTLSInsecure:            cfg.FetchTLSInsecureSkipVerify,
		TelegramProxyServiceURL:     cfg.TelegramProxyServiceURL,
		TelegramProxyServiceToken:   cfg.TelegramProxyServiceToken,
		TelegramProxyTargetURL:      cfg.TelegramProxyTargetURL,
		TelegramStaticProxy:         cfg.TelegramStaticProxy,
		TelegramProxyConnectTimeout: cfg.TelegramProxyConnectTimeout,
		TelegramProxyRequestTimeout: cfg.TelegramProxyRequestTimeout,
		TelegramProxyRetry:          cfg.TelegramProxyRetry,
		TelegramMaxPages:            cfg.TelegramMaxPages,
	}, registryBundle.Telegram)
	if err != nil {
		log.Error("title resolver init failed", "err", err)
		os.Exit(1)
	}

	refresher := &service.FeedRefresher{
		Feeds:                   pgStore,
		Entries:                 pgStore,
		Dedup:                   pgStore,
		Registry:                registryBundle.Registry,
		Log:                     log,
		StoreEntriesMode:        cfg.StoreEntriesMode,
		CircuitBreakerThreshold: cfg.FeedCircuitBreakerThreshold,
		MinPollInterval:         cfg.MinPollInterval,
		MaxPollInterval:         cfg.MaxPollInterval,
	}

	filterEngine := filter.New(filter.Config{
		MaxRulesPerFilter: cfg.MaxFilterRulesPerFilter,
		MaxRegexLength:    cfg.MaxRegexLength,
		MatchTimeout:      cfg.FilterMatchTimeout,
	})
	refresher.Filters = pgStore
	refresher.Matches = pgStore
	refresher.Engine = filterEngine
	refresher.Labels = pgStore
	refresher.Webhooks = pgStore
	refresher.WebhookLogs = pgStore
	if cfg.WSEnabled {
		refresher.Realtime = wsPublisher
	}

	w := &worker.Runner{
		Log:       log,
		Store:     pgStore,
		Refresher: refresher,
		Cfg: worker.Config{
			PoolSize:        cfg.WorkerPoolSize,
			WebhookPoolSize: cfg.WebhookWorkerPoolSize,
			SchedulerTick:   cfg.SchedulerTick,
			MinPollInterval: cfg.MinPollInterval,
			MaxPollInterval: cfg.MaxPollInterval,
			FetchTimeout:    30 * time.Second,
			InstanceID:      cfg.WorkerInstanceID,

			WebhookMaxAttempts: cfg.WebhookMaxAttempts,
			WebhookTimeout:     cfg.WebhookTimeout,
			WebhookRetryBase:   cfg.WebhookRetryBase,
			WebhookRetryMax:    cfg.WebhookRetryMax,
			DedupOnlyStorage:   cfg.StoreEntriesMode == "dedup_only",

			RemovedRetentionDays:     cfg.RemovedRetentionDays,
			WebhookLogRetentionDays:  cfg.WebhookLogRetentionDays,
			FilterMatchRetentionDays: cfg.FilterMatchRetentionDays,
			CleanupInterval:          cfg.CleanupInterval,

			FeedPollDailyResetEnabled: cfg.FeedPollDailyResetEnabled,
			FeedPollDailyResetTZ:      cfg.FeedPollDailyResetTZ,
		},
		WebhookHTTPClient: webhookClient,
		SSRFGuard:         ssrfGuard,
	}
	go func() {
		if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("worker stopped", "err", err)
		}
	}()

	pprofShutdown, err := httpserver.StartPprof(ctx, log, cfg.PprofEnabled, cfg.PprofListenAddr)
	if err != nil {
		log.Error("pprof server failed", "err", err)
		os.Exit(1)
	}

	srv := httpserver.New(httpserver.Dependencies{
		Logger:                 log,
		HandlerRegistry:        registryBundle.Registry,
		TitleResolver:          titleResolver,
		BridgeManager:          bridgeMgr,
		RefreshAll:             pgStore,
		DB:                     db,
		CategoryStore:          pgStore,
		FeedStore:              pgStore,
		EntryStore:             pgStore,
		FilterStore:            pgStore,
		FilterMatchStore:       pgStore,
		LabelStore:             pgStore,
		WebhookStore:           pgStore,
		WebhookLogStore:        pgStore,
		FilterEngine:           filterEngine,
		AuthToken:              cfg.AuthToken,
		MetricsToken:           cfg.MetricsToken,
		AdminUsername:          cfg.AdminUsername,
		AdminPassword:          cfg.AdminPassword,
		UserStore:              pgStore,
		HTTPClient:             httpClient,
		FetchUserAgent:         cfg.FetchUserAgent,
		FetchTimeoutSec:        cfg.FetchTimeoutSeconds,
		FetchAllowPrivateNet:   cfg.FetchAllowPrivateNetwork,
		FetchAllowedCIDRs:      cfg.FetchAllowedCIDRs,
		FetchBlockedHosts:      cfg.FetchBlockedHosts,
		FetchViaProxyURL:       cfg.FetchViaProxyURL,
		ScraperMaxContentBytes: cfg.ScraperMaxContentBytes,
		SSRFGuard:              ssrfGuard,
		WebhookHTTPClient:      webhookClient,
		WebhookMaxAttempts:     cfg.WebhookMaxAttempts,
		WSEnabled:              cfg.WSEnabled,
		WSHub:                  wsHub,
		WSClientBuffer:         cfg.WSClientBuffer,
		WSPingInterval:         cfg.WSPingInterval,

		HSTSEnabled:             cfg.HSTSEnabled,
		RateLimitEnabled:        cfg.RateLimitEnabled,
		RateLimitRPS:            cfg.RateLimitRPS,
		RateLimitBurst:          cfg.RateLimitBurst,
		LoginRateLimitRPS:       cfg.LoginRateLimitRPS,
		LoginRateLimitBurst:     cfg.LoginRateLimitBurst,
		MaxRequestBodyBytes:     cfg.MaxRequestBodyBytes,
		MaxImportFeeds:          cfg.MaxImportFeeds,
		CompressEnabled:         cfg.CompressEnabled,
		UIEnabled:               cfg.UIEnabled,
		CSRFSecret:              csrfSecret(cfg),
		MinPollInterval:         cfg.MinPollInterval,
		MaxPollInterval:         cfg.MaxPollInterval,
		DedupStore:              pgStore,
		StoreEntriesMode:        cfg.StoreEntriesMode,
		CircuitBreakerThreshold: cfg.FeedCircuitBreakerThreshold,
		WorkerPoolSize:          cfg.WorkerPoolSize,
		WebhookWorkerPoolSize:   cfg.WebhookWorkerPoolSize,
		EnvFilePath:             os.Getenv("RSSAM_ENV_FILE"),
		DatabaseURL:             cfg.DatabaseURL,
		GitHubRepo:              strings.TrimSpace(os.Getenv("GITHUB_REPO")),
		WorkerControl:           w,
	})

	if err := srv.Run(ctx, cfg.ListenAddr, cfg.ShutdownTimeout); err != nil {
		log.Error("http server stopped", "err", err)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := pprofShutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("pprof shutdown", "err", err)
	}
	log.Info("shutdown complete")
}

func csrfSecret(cfg config.Config) string {
	if v := cfg.AuthToken; v != "" {
		return v
	}
	if v := cfg.MetricsToken; v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("CSRF_SECRET")); v != "" {
		return v
	}

	secret, err := ops.GenerateHexSecret(32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "csrf secret: %v\n", err)
		os.Exit(1)
	}

	envPath := ops.EnvFilePath()
	if err := envfile.SetKeys(envPath, map[string]string{"CSRF_SECRET": secret}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: generated CSRF secret but could not save to %s: %v\n", envPath, err)
	} else {
		_ = os.Setenv("CSRF_SECRET", secret)
		fmt.Fprintf(os.Stderr, "warning: AUTH_TOKEN and METRICS_TOKEN are empty; generated CSRF secret and saved to %s\n", envPath)
	}
	return secret
}
