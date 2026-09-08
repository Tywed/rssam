package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL      string
	DatabaseMaxConns int
	ListenAddr       string
	LogLevel         string
	LogFormat        string
	RunMigrations    bool

	AuthToken string
	// AllowDevToken permits the well-known placeholder AUTH_TOKEN=dev-token
	// from .env.example (ALLOW_DEV_TOKEN=true). Off by default: the token
	// authenticates as the first user with full admin rights, and a copied
	// example file must not become a production credential by accident.
	AllowDevToken bool
	MetricsToken  string

	AdminUsername string
	AdminPassword string

	FetchAllowPrivateNetwork   bool
	FetchAllowedCIDRs          []string
	FetchBlockedHosts          []string
	FetchTLSInsecureSkipVerify bool
	FetchUserAgent             string
	FetchTimeoutSeconds        int
	FetchViaProxyURL           string
	ScraperMaxContentBytes     int64

	FeedCircuitBreakerThreshold int
	FeedPollDailyResetEnabled   bool
	FeedPollDailyResetTZ        string
	FilterMatchTimeout          time.Duration

	MaxAPIBaseURL        string
	MaxDefaultLimit      int
	MaxDefaultLookback   time.Duration
	MaxOverlap           time.Duration
	MaxRateLimitSeconds  int
	MaxRequestIntervalMs int
	MaxConcurrentSlots   int
	MaxAllowPrivateAPI   bool

	MaxstatAccessToken      string
	MaxstatAPIBaseURL       string
	MaxstatDefaultLimit     int
	MaxstatDefaultLookback  time.Duration
	MaxstatOverlap          time.Duration
	MaxstatRateLimitSeconds int

	TelegramProxyServiceURL     string
	TelegramProxyServiceToken   string
	TelegramProxyTargetURL      string
	TelegramStaticProxy         string
	TelegramProxyConnectTimeout time.Duration
	TelegramProxyRequestTimeout time.Duration
	TelegramProxyRetry          int
	TelegramMaxPages            int

	VKAccessToken      string
	VKAPIVersion       string
	VKDefaultCount     int
	VKDefaultLookback  time.Duration
	VKOverlap          time.Duration
	VKRateLimitSeconds int

	RutubeAPIBaseURL string

	DzenSearchURL string
	DzenUserAgent string
	DzenCookie    string

	RemovedRetentionDays     int
	WebhookLogRetentionDays  int
	FilterMatchRetentionDays int
	FeedPollLogRetentionDays int
	CleanupInterval          time.Duration

	WorkerPoolSize        int
	WebhookWorkerPoolSize int
	SchedulerTick         time.Duration
	MinPollInterval       time.Duration
	MaxPollInterval       time.Duration
	WorkerInstanceID      string

	MaxFilterRulesPerFilter int
	MaxRegexLength          int

	WebhookMaxAttempts int
	WebhookTimeout     time.Duration
	WebhookRetryBase   time.Duration
	WebhookRetryMax    time.Duration

	WSEnabled      bool
	WSClientBuffer int
	WSPingInterval time.Duration

	UIEnabled bool

	// FTSLanguage is the PostgreSQL text search config for entries (simple|russian).
	FTSLanguage string

	// StoreEntriesMode: full (default) stores all polled entries; dedup_only keeps hash/url for
	// non-matches and strips entry payload after successful webhook delivery.
	StoreEntriesMode string

	// Hardening / HTTP
	HSTSEnabled      bool
	TrustedProxies   []string
	RateLimitEnabled bool
	RateLimitRPS     float64
	RateLimitBurst   int
	// LoginRateLimitRPS / LoginRateLimitBurst throttle POST /ui/login per
	// client IP separately from the general limiter: a password guess costs
	// one bcrypt round, so the budget is per-minute, not per-second.
	LoginRateLimitRPS   float64
	LoginRateLimitBurst int
	MaxRequestBodyBytes int64
	MaxImportFeeds      int
	CompressEnabled     bool
	PprofEnabled        bool
	PprofListenAddr     string
	ShutdownTimeout     time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
		DatabaseMaxConns: parseInt(getEnv("DATABASE_MAX_CONNS", "0"), 0),
		ListenAddr:       getEnv("LISTEN_ADDR", ":8080"),
		LogLevel:         strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:        strings.ToLower(getEnv("LOG_FORMAT", "json")), // json|text
		RunMigrations:    parseBool(os.Getenv("RUN_MIGRATIONS")),

		AuthToken:     strings.TrimSpace(os.Getenv("AUTH_TOKEN")),
		AllowDevToken: parseBool(os.Getenv("ALLOW_DEV_TOKEN")),
		MetricsToken:  strings.TrimSpace(os.Getenv("METRICS_TOKEN")),

		AdminUsername: strings.TrimSpace(os.Getenv("ADMIN_USERNAME")),
		AdminPassword: strings.TrimSpace(os.Getenv("ADMIN_PASSWORD")),

		FetchAllowPrivateNetwork:   parseBool(os.Getenv("FETCH_ALLOW_PRIVATE_NETWORK")),
		FetchAllowedCIDRs:          parseCSV(os.Getenv("FETCH_ALLOWED_CIDRS")),
		FetchBlockedHosts:          parseCSV(os.Getenv("FETCH_BLOCKED_HOSTS")),
		FetchTLSInsecureSkipVerify: parseBool(os.Getenv("FETCH_TLS_INSECURE")) || parseBool(os.Getenv("FETCH_INSECURE_SKIP_VERIFY")),
		FetchUserAgent:             getEnv("FETCH_USER_AGENT", "rssam"),
		FetchTimeoutSeconds:        parseInt(getEnv("FETCH_TIMEOUT_SECONDS", "15"), 15),
		FetchViaProxyURL:           strings.TrimSpace(os.Getenv("FETCH_VIA_PROXY")),
		ScraperMaxContentBytes:     int64(parseInt(getEnv("SCRAPER_MAX_CONTENT_BYTES", "1048576"), 1048576)),

		FeedCircuitBreakerThreshold: parseInt(getEnv("FEED_CIRCUIT_BREAKER_THRESHOLD", "10"), 10),
		FeedPollDailyResetEnabled:   parseBoolDefault(os.Getenv("FEED_POLL_DAILY_RESET"), true),
		FeedPollDailyResetTZ:        getEnv("FEED_POLL_DAILY_RESET_TZ", "Europe/Moscow"),
		FilterMatchTimeout:          parseDuration(getEnv("FILTER_MATCH_TIMEOUT", "50ms"), 50*time.Millisecond),

		MaxAPIBaseURL:        strings.TrimSpace(os.Getenv("MAX_API_BASE_URL")),
		MaxDefaultLimit:      parseInt(getEnv("MAX_DEFAULT_LIMIT", "100"), 100),
		MaxDefaultLookback:   parseDuration(getEnv("MAX_DEFAULT_LOOKBACK_MS", "24h"), 24*time.Hour),
		MaxOverlap:           parseDuration(getEnv("MAX_OVERLAP_MS", "2m"), 2*time.Minute),
		MaxRateLimitSeconds:  parseInt(getEnv("MAX_RATE_LIMIT_SECONDS", "60"), 60),
		MaxRequestIntervalMs: parseInt(getEnv("MAX_REQUEST_INTERVAL_MS", "1000"), 1000),
		MaxConcurrentSlots:   parseInt(getEnv("MAX_CONCURRENT_SLOTS", "1"), 1),
		MaxAllowPrivateAPI:   parseBool(os.Getenv("MAX_ALLOW_PRIVATE_API")),

		MaxstatAccessToken:      strings.TrimSpace(os.Getenv("MAXSTAT_ACCESS_TOKEN")),
		MaxstatAPIBaseURL:       strings.TrimSpace(getEnv("MAXSTAT_API_BASE_URL", "https://maxstat.ru/api/v1")),
		MaxstatDefaultLimit:     parseInt(getEnv("MAXSTAT_DEFAULT_LIMIT", "100"), 100),
		MaxstatDefaultLookback:  parseDuration(getEnv("MAXSTAT_DEFAULT_LOOKBACK", "24h"), 24*time.Hour),
		MaxstatOverlap:          parseDuration(getEnv("MAXSTAT_OVERLAP", "2m"), 2*time.Minute),
		MaxstatRateLimitSeconds: parseInt(getEnv("MAXSTAT_RATE_LIMIT_SECONDS", "1800"), 1800),

		TelegramProxyServiceURL:     strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_SERVICE_URL")),
		TelegramProxyServiceToken:   strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_SERVICE_TOKEN")),
		TelegramProxyTargetURL:      strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_TARGET_URL")),
		TelegramStaticProxy:         strings.TrimSpace(os.Getenv("TELEGRAM_STATIC_PROXY")),
		TelegramProxyConnectTimeout: parseDuration(getEnv("TELEGRAM_PROXY_CONNECT_TIMEOUT", "10s"), 10*time.Second),
		TelegramProxyRequestTimeout: parseDuration(getEnv("TELEGRAM_PROXY_REQUEST_TIMEOUT", "25s"), 25*time.Second),
		TelegramProxyRetry:          parseInt(getEnv("TELEGRAM_PROXY_RETRY", "1"), 1),
		TelegramMaxPages:            parseInt(getEnv("TELEGRAM_MAX_PAGES", "1"), 1),

		VKAccessToken:      strings.TrimSpace(os.Getenv("VK_ACCESS_TOKEN")),
		VKAPIVersion:       getEnv("VK_API_VERSION", "5.199"),
		VKDefaultCount:     parseInt(getEnv("VK_DEFAULT_COUNT", "100"), 100),
		VKDefaultLookback:  parseDuration(getEnv("VK_DEFAULT_LOOKBACK", "24h"), 24*time.Hour),
		VKOverlap:          parseDuration(getEnv("VK_OVERLAP", "2m"), 2*time.Minute),
		VKRateLimitSeconds: parseInt(getEnv("VK_RATE_LIMIT_SECONDS", "5"), 5),

		RutubeAPIBaseURL: strings.TrimSpace(getEnv("RUTUBE_API_BASE_URL", "https://rutube.ru/api")),

		DzenSearchURL: strings.TrimSpace(getEnv("DZEN_SEARCH_URL", "https://dzen.ru/news/search")),
		DzenUserAgent: strings.TrimSpace(os.Getenv("DZEN_USER_AGENT")),
		DzenCookie:    strings.TrimSpace(getEnv("DZEN_COOKIE", "zen_sso_checked=1")),

		RemovedRetentionDays:     parseInt(getEnv("REMOVED_RETENTION_DAYS", "30"), 30),
		WebhookLogRetentionDays:  parseInt(getEnv("WEBHOOK_LOG_RETENTION_DAYS", "90"), 90),
		FilterMatchRetentionDays: parseIntAllowZero(getEnv("FILTER_MATCH_RETENTION_DAYS", "90"), 90),
		FeedPollLogRetentionDays: parseIntAllowZero(getEnv("FEED_POLL_LOG_RETENTION_DAYS", "14"), 14),
		CleanupInterval:          parseDuration(getEnv("CLEANUP_INTERVAL", "24h"), 24*time.Hour),

		WorkerPoolSize:   parseInt(getEnv("WORKER_POOL_SIZE", "10"), 10),
		SchedulerTick:    parseDuration(getEnv("SCHEDULER_TICK", "5s"), 5*time.Second),
		MinPollInterval:  parseDuration(getEnv("MIN_POLL_INTERVAL", "60s"), 60*time.Second),
		MaxPollInterval:  parseDuration(getEnv("MAX_POLL_INTERVAL", "24h"), 24*time.Hour),
		WorkerInstanceID: strings.TrimSpace(os.Getenv("WORKER_INSTANCE_ID")),

		MaxFilterRulesPerFilter: parseInt(getEnv("MAX_FILTER_RULES_PER_FILTER", "50"), 50),
		MaxRegexLength:          parseInt(getEnv("MAX_REGEX_LENGTH", "2048"), 2048),

		WebhookMaxAttempts: parseInt(getEnv("WEBHOOK_MAX_ATTEMPTS", "10"), 10),
		WebhookTimeout:     parseDuration(getEnv("WEBHOOK_TIMEOUT", "10s"), 10*time.Second),
		WebhookRetryBase:   parseDuration(getEnv("WEBHOOK_RETRY_BASE", "5s"), 5*time.Second),
		WebhookRetryMax:    parseDuration(getEnv("WEBHOOK_RETRY_MAX", "1h"), time.Hour),
		WSEnabled:          parseBool(getEnv("WS_ENABLED", "true")),
		WSClientBuffer:     parseInt(getEnv("WS_CLIENT_BUFFER", "100"), 100),
		WSPingInterval:     parseDuration(getEnv("WS_PING_INTERVAL", "30s"), 30*time.Second),

		UIEnabled: parseBool(os.Getenv("UI_ENABLED")),

		FTSLanguage:      strings.ToLower(getEnv("FTS_LANGUAGE", "russian")),
		StoreEntriesMode: parseStoreEntriesMode(),

		HSTSEnabled:         parseBool(os.Getenv("HSTS")),
		TrustedProxies:      parseTrustedProxies(),
		RateLimitEnabled:    parseBoolDefault(os.Getenv("RATE_LIMIT_ENABLED"), true),
		RateLimitRPS:        parseFloat(getEnv("RATE_LIMIT_RPS", "10"), 10),
		RateLimitBurst:      parseInt(getEnv("RATE_LIMIT_BURST", "20"), 20),
		LoginRateLimitRPS:   parseFloat(getEnv("LOGIN_RATE_LIMIT_RPS", "0.2"), 0.2),
		LoginRateLimitBurst: parseInt(getEnv("LOGIN_RATE_LIMIT_BURST", "10"), 10),
		MaxRequestBodyBytes: int64(parseInt(getEnv("MAX_REQUEST_BODY_BYTES", "1048576"), 1048576)),
		MaxImportFeeds:      parseInt(getEnv("MAX_IMPORT_FEEDS", "500"), 500),
		CompressEnabled:     parseBoolDefault(os.Getenv("COMPRESS_ENABLED"), true),
		PprofEnabled:        parseBool(os.Getenv("PPROF_ENABLED")),
		PprofListenAddr:     getEnv("PPROF_LISTEN_ADDR", "127.0.0.1:6060"),
		ShutdownTimeout:     parseDuration(getEnv("SHUTDOWN_TIMEOUT", "15s"), 15*time.Second),
	}

	if raw := strings.TrimSpace(os.Getenv("WEBHOOK_WORKER_POOL_SIZE")); raw == "" {
		cfg.WebhookWorkerPoolSize = cfg.WorkerPoolSize
	} else {
		cfg.WebhookWorkerPoolSize = parseInt(raw, cfg.WorkerPoolSize)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error

	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if c.DatabaseMaxConns < 0 || c.DatabaseMaxConns > 500 {
		errs = append(errs, fmt.Errorf("DATABASE_MAX_CONNS must be between 0 and 500 (0 = auto), got %d", c.DatabaseMaxConns))
	}
	if strings.TrimSpace(c.ListenAddr) == "" {
		errs = append(errs, errors.New("LISTEN_ADDR must not be empty"))
	}
	if IsPlaceholderAuthToken(c.AuthToken) && !c.AllowDevToken {
		errs = append(errs, fmt.Errorf("AUTH_TOKEN=%q is the placeholder from .env.example and grants admin access to anyone who reads the example; set a real secret, leave it empty (UI login + API keys), or set ALLOW_DEV_TOKEN=true for local development", c.AuthToken))
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", c.LogFormat))
	}
	if c.LogLevel == "" {
		errs = append(errs, errors.New("LOG_LEVEL must not be empty"))
	}
	if strings.TrimSpace(c.FetchUserAgent) == "" {
		errs = append(errs, errors.New("FETCH_USER_AGENT must not be empty"))
	}
	if c.FetchTimeoutSeconds < 1 || c.FetchTimeoutSeconds > 300 {
		errs = append(errs, fmt.Errorf("FETCH_TIMEOUT_SECONDS must be between 1 and 300, got %d", c.FetchTimeoutSeconds))
	}
	if c.ScraperMaxContentBytes < 1024 || c.ScraperMaxContentBytes > 10*1024*1024 {
		errs = append(errs, fmt.Errorf("SCRAPER_MAX_CONTENT_BYTES must be between 1024 and 10485760, got %d", c.ScraperMaxContentBytes))
	}
	if c.MaxDefaultLimit < 1 || c.MaxDefaultLimit > 200 {
		errs = append(errs, fmt.Errorf("MAX_DEFAULT_LIMIT must be between 1 and 200, got %d", c.MaxDefaultLimit))
	}
	if c.MaxDefaultLookback <= 0 || c.MaxDefaultLookback > 30*24*time.Hour {
		errs = append(errs, fmt.Errorf("MAX_DEFAULT_LOOKBACK_MS must be between 1ms and 720h, got %s", c.MaxDefaultLookback))
	}
	if c.MaxOverlap < 0 || c.MaxOverlap > 24*time.Hour {
		errs = append(errs, fmt.Errorf("MAX_OVERLAP_MS must be between 0 and 24h, got %s", c.MaxOverlap))
	}
	if c.MaxRateLimitSeconds < 1 || c.MaxRateLimitSeconds > 86400 {
		errs = append(errs, fmt.Errorf("MAX_RATE_LIMIT_SECONDS must be between 1 and 86400, got %d", c.MaxRateLimitSeconds))
	}
	if c.MaxRequestIntervalMs < 0 || c.MaxRequestIntervalMs > 60000 {
		errs = append(errs, fmt.Errorf("MAX_REQUEST_INTERVAL_MS must be between 0 and 60000, got %d", c.MaxRequestIntervalMs))
	}
	if c.MaxConcurrentSlots < 1 || c.MaxConcurrentSlots > 32 {
		errs = append(errs, fmt.Errorf("MAX_CONCURRENT_SLOTS must be between 1 and 32, got %d", c.MaxConcurrentSlots))
	}
	if c.TelegramProxyConnectTimeout <= 0 || c.TelegramProxyConnectTimeout > 5*time.Minute {
		errs = append(errs, fmt.Errorf("TELEGRAM_PROXY_CONNECT_TIMEOUT must be between 1ms and 5m, got %s", c.TelegramProxyConnectTimeout))
	}
	if c.TelegramProxyRequestTimeout <= 0 || c.TelegramProxyRequestTimeout > 5*time.Minute {
		errs = append(errs, fmt.Errorf("TELEGRAM_PROXY_REQUEST_TIMEOUT must be between 1ms and 5m, got %s", c.TelegramProxyRequestTimeout))
	}
	if c.TelegramProxyRetry < 0 || c.TelegramProxyRetry > 20 {
		errs = append(errs, fmt.Errorf("TELEGRAM_PROXY_RETRY must be between 0 and 20, got %d", c.TelegramProxyRetry))
	}
	if c.TelegramMaxPages < 1 || c.TelegramMaxPages > 100 {
		errs = append(errs, fmt.Errorf("TELEGRAM_MAX_PAGES must be between 1 and 100, got %d", c.TelegramMaxPages))
	}
	if c.VKDefaultCount < 1 || c.VKDefaultCount > 100 {
		errs = append(errs, fmt.Errorf("VK_DEFAULT_COUNT must be between 1 and 100, got %d", c.VKDefaultCount))
	}
	if c.VKDefaultLookback <= 0 || c.VKDefaultLookback > 30*24*time.Hour {
		errs = append(errs, fmt.Errorf("VK_DEFAULT_LOOKBACK must be between 1ms and 720h, got %s", c.VKDefaultLookback))
	}
	if c.VKOverlap < 0 || c.VKOverlap > 24*time.Hour {
		errs = append(errs, fmt.Errorf("VK_OVERLAP must be between 0 and 24h, got %s", c.VKOverlap))
	}
	if c.VKRateLimitSeconds < 1 || c.VKRateLimitSeconds > 86400 {
		errs = append(errs, fmt.Errorf("VK_RATE_LIMIT_SECONDS must be between 1 and 86400, got %d", c.VKRateLimitSeconds))
	}
	if c.MaxstatDefaultLimit < 1 || c.MaxstatDefaultLimit > 100 {
		errs = append(errs, fmt.Errorf("MAXSTAT_DEFAULT_LIMIT must be between 1 and 100, got %d", c.MaxstatDefaultLimit))
	}
	if c.MaxstatDefaultLookback <= 0 || c.MaxstatDefaultLookback > 30*24*time.Hour {
		errs = append(errs, fmt.Errorf("MAXSTAT_DEFAULT_LOOKBACK must be between 1ms and 720h, got %s", c.MaxstatDefaultLookback))
	}
	if c.MaxstatOverlap < 0 || c.MaxstatOverlap > 24*time.Hour {
		errs = append(errs, fmt.Errorf("MAXSTAT_OVERLAP must be between 0 and 24h, got %s", c.MaxstatOverlap))
	}
	if c.MaxstatRateLimitSeconds < 1 || c.MaxstatRateLimitSeconds > 86400 {
		errs = append(errs, fmt.Errorf("MAXSTAT_RATE_LIMIT_SECONDS must be between 1 and 86400, got %d", c.MaxstatRateLimitSeconds))
	}
	if c.RemovedRetentionDays <= 0 || c.RemovedRetentionDays > 3650 {
		errs = append(errs, fmt.Errorf("REMOVED_RETENTION_DAYS must be between 1 and 3650, got %d", c.RemovedRetentionDays))
	}
	if c.WebhookLogRetentionDays <= 0 || c.WebhookLogRetentionDays > 3650 {
		errs = append(errs, fmt.Errorf("WEBHOOK_LOG_RETENTION_DAYS must be between 1 and 3650, got %d", c.WebhookLogRetentionDays))
	}
	if c.FilterMatchRetentionDays < 0 || c.FilterMatchRetentionDays > 3650 {
		errs = append(errs, fmt.Errorf("FILTER_MATCH_RETENTION_DAYS must be between 0 and 3650 (0 disables), got %d", c.FilterMatchRetentionDays))
	}
	if c.FeedPollLogRetentionDays < 0 || c.FeedPollLogRetentionDays > 3650 {
		errs = append(errs, fmt.Errorf("FEED_POLL_LOG_RETENTION_DAYS must be between 0 and 3650 (0 disables), got %d", c.FeedPollLogRetentionDays))
	}
	if c.CleanupInterval <= 0 || c.CleanupInterval > 7*24*time.Hour {
		errs = append(errs, fmt.Errorf("CLEANUP_INTERVAL must be between 1ms and 168h, got %s", c.CleanupInterval))
	}
	if c.WorkerPoolSize <= 0 || c.WorkerPoolSize > 1000 {
		errs = append(errs, fmt.Errorf("WORKER_POOL_SIZE must be between 1 and 1000, got %d", c.WorkerPoolSize))
	}
	if c.WebhookWorkerPoolSize <= 0 || c.WebhookWorkerPoolSize > 1000 {
		errs = append(errs, fmt.Errorf("WEBHOOK_WORKER_POOL_SIZE must be between 1 and 1000, got %d", c.WebhookWorkerPoolSize))
	}
	if c.SchedulerTick <= 0 {
		errs = append(errs, errors.New("SCHEDULER_TICK must be > 0"))
	}
	if c.MinPollInterval <= 0 {
		errs = append(errs, errors.New("MIN_POLL_INTERVAL must be > 0"))
	}
	if c.MaxPollInterval <= 0 {
		errs = append(errs, errors.New("MAX_POLL_INTERVAL must be > 0"))
	}
	if c.MinPollInterval > 0 && c.MaxPollInterval > 0 && c.MinPollInterval > c.MaxPollInterval {
		errs = append(errs, fmt.Errorf("MIN_POLL_INTERVAL must be <= MAX_POLL_INTERVAL (got %s > %s)", c.MinPollInterval, c.MaxPollInterval))
	}
	if c.MaxFilterRulesPerFilter <= 0 || c.MaxFilterRulesPerFilter > 10000 {
		errs = append(errs, fmt.Errorf("MAX_FILTER_RULES_PER_FILTER must be between 1 and 10000, got %d", c.MaxFilterRulesPerFilter))
	}
	if c.MaxRegexLength <= 0 || c.MaxRegexLength > 1048576 {
		errs = append(errs, fmt.Errorf("MAX_REGEX_LENGTH must be between 1 and 1048576, got %d", c.MaxRegexLength))
	}
	if c.WebhookMaxAttempts <= 0 || c.WebhookMaxAttempts > 1000 {
		errs = append(errs, fmt.Errorf("WEBHOOK_MAX_ATTEMPTS must be between 1 and 1000, got %d", c.WebhookMaxAttempts))
	}
	if c.WebhookTimeout <= 0 || c.WebhookTimeout > 300*time.Second {
		errs = append(errs, fmt.Errorf("WEBHOOK_TIMEOUT must be between 1s and 300s, got %s", c.WebhookTimeout))
	}
	if c.WebhookRetryBase <= 0 {
		errs = append(errs, errors.New("WEBHOOK_RETRY_BASE must be > 0"))
	}
	if c.WebhookRetryMax <= 0 {
		errs = append(errs, errors.New("WEBHOOK_RETRY_MAX must be > 0"))
	}
	if c.WebhookRetryBase > 0 && c.WebhookRetryMax > 0 && c.WebhookRetryBase > c.WebhookRetryMax {
		errs = append(errs, fmt.Errorf("WEBHOOK_RETRY_BASE must be <= WEBHOOK_RETRY_MAX (got %s > %s)", c.WebhookRetryBase, c.WebhookRetryMax))
	}
	if c.WSClientBuffer <= 0 || c.WSClientBuffer > 100000 {
		errs = append(errs, fmt.Errorf("WS_CLIENT_BUFFER must be between 1 and 100000, got %d", c.WSClientBuffer))
	}
	if c.WSPingInterval <= 0 || c.WSPingInterval > 10*time.Minute {
		errs = append(errs, fmt.Errorf("WS_PING_INTERVAL must be between 1ms and 10m, got %s", c.WSPingInterval))
	}
	switch c.FTSLanguage {
	case "", "simple", "russian", "ru":
	default:
		errs = append(errs, fmt.Errorf("FTS_LANGUAGE must be simple or russian, got %q", c.FTSLanguage))
	}
	switch strings.ToLower(strings.TrimSpace(c.StoreEntriesMode)) {
	case "full", "dedup_only":
	default:
		errs = append(errs, fmt.Errorf("STORE_ENTRIES_MODE must be full or dedup_only, got %q", c.StoreEntriesMode))
	}
	if c.RateLimitRPS <= 0 || c.RateLimitRPS > 10000 {
		errs = append(errs, fmt.Errorf("RATE_LIMIT_RPS must be between 0.01 and 10000, got %v", c.RateLimitRPS))
	}
	if c.RateLimitBurst <= 0 || c.RateLimitBurst > 100000 {
		errs = append(errs, fmt.Errorf("RATE_LIMIT_BURST must be between 1 and 100000, got %d", c.RateLimitBurst))
	}
	if c.LoginRateLimitRPS <= 0 || c.LoginRateLimitRPS > 1000 {
		errs = append(errs, fmt.Errorf("LOGIN_RATE_LIMIT_RPS must be between 0.001 and 1000, got %v", c.LoginRateLimitRPS))
	}
	if c.LoginRateLimitBurst <= 0 || c.LoginRateLimitBurst > 10000 {
		errs = append(errs, fmt.Errorf("LOGIN_RATE_LIMIT_BURST must be between 1 and 10000, got %d", c.LoginRateLimitBurst))
	}
	if c.MaxRequestBodyBytes < 1024 || c.MaxRequestBodyBytes > 32*1024*1024 {
		errs = append(errs, fmt.Errorf("MAX_REQUEST_BODY_BYTES must be between 1024 and 33554432, got %d", c.MaxRequestBodyBytes))
	}
	if c.MaxImportFeeds <= 0 || c.MaxImportFeeds > 100000 {
		errs = append(errs, fmt.Errorf("MAX_IMPORT_FEEDS must be between 1 and 100000, got %d", c.MaxImportFeeds))
	}
	if strings.TrimSpace(c.PprofListenAddr) == "" {
		errs = append(errs, errors.New("PPROF_LISTEN_ADDR must not be empty"))
	}
	if c.ShutdownTimeout <= 0 || c.ShutdownTimeout > 5*time.Minute {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT must be between 1ms and 5m, got %s", c.ShutdownTimeout))
	}

	return joinErrors(errs)
}

// EffectiveDatabaseMaxConns is DATABASE_MAX_CONNS, or worker pools plus HTTP headroom.
func (c Config) EffectiveDatabaseMaxConns() int {
	if c.DatabaseMaxConns > 0 {
		return c.DatabaseMaxConns
	}
	n := min(max(c.WorkerPoolSize+c.WebhookWorkerPoolSize+8, 16), 200)
	return n
}

func getEnv(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseStoreEntriesMode() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("STORE_ENTRIES_MODE"))); v != "" {
		return v
	}
	if parseBool(os.Getenv("DEDUP_ONLY_STORAGE")) {
		return "dedup_only"
	}
	return "full"
}

func parseBoolDefault(v string, def bool) bool {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return parseBool(v)
}

func parseFloat(v string, def float64) float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	var n float64
	var frac float64
	var div float64 = 1
	dot := false
	for _, r := range v {
		if r == '.' {
			dot = true
			continue
		}
		if r < '0' || r > '9' {
			return def
		}
		if dot {
			div *= 10
			frac = frac*10 + float64(r-'0')
		} else {
			n = n*10 + float64(r-'0')
		}
	}
	if dot {
		n += frac / div
	}
	if n <= 0 {
		return def
	}
	return n
}

// parseTrustedProxies distinguishes "unset" (loopback default) from an
// explicitly empty TRUSTED_PROXIES= (trust no forwarding headers at all).
func parseTrustedProxies() []string {
	v, ok := os.LookupEnv("TRUSTED_PROXIES")
	if !ok {
		return []string{"127.0.0.0/8", "::1/128"}
	}
	return parseCSV(v)
}

func parseCSV(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func parseInt(v string, def int) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		return def
	}
	return n
}

// placeholderAuthTokens are the values shipped in .env.example / docs that
// people copy verbatim. They are refused at start unless ALLOW_DEV_TOKEN=true.
var placeholderAuthTokens = map[string]struct{}{
	"dev-token": {},
}

// IsPlaceholderAuthToken reports whether token is a well-known example value.
func IsPlaceholderAuthToken(token string) bool {
	_, ok := placeholderAuthTokens[strings.ToLower(strings.TrimSpace(token))]
	return ok
}

// parseIntAllowZero is parseInt for settings where an explicit "0" is a valid
// value ("disabled") rather than "use the default".
func parseIntAllowZero(v string, def int) int {
	if strings.TrimSpace(v) == "0" {
		return 0
	}
	return parseInt(v, def)
}

func parseDuration(v string, def time.Duration) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	var b strings.Builder
	b.WriteString("config validation failed:")
	for _, e := range errs {
		b.WriteString("\n- ")
		b.WriteString(e.Error())
	}
	return errors.New(b.String())
}
