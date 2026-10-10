package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
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

	// AllowDevToken permits the ADMIN_PASSWORD=changeme placeholder from
	// .env.example on a fresh database when ALLOW_DEV_TOKEN=true. Off by
	// default: a copied example file must not become a production
	// credential by accident.
	AllowDevToken bool
	MetricsToken  string

	AdminUsername string
	AdminPassword string

	// Warnings are non-fatal findings from Load (deprecated variable names);
	// main logs them once at start-up.
	Warnings []string

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
	TelegramConcurrentSlots     int

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
	// FeedSilentDays: an active feed without a new item for this long is
	// listed as "silent" in the admin dashboard (0 disables).
	FeedSilentDays int
	// BackupDir is where rssam-backup.sh writes dumps; a system-alert
	// webhook is told when the newest dump is older than BackupMaxAge
	// ("" disables the check).
	BackupDir             string
	BackupMaxAge          time.Duration
	AuditLogRetentionDays int
	CleanupInterval       time.Duration

	WorkerPoolSize        int
	WebhookWorkerPoolSize int
	SchedulerTick         time.Duration
	MinPollInterval       time.Duration
	MaxPollInterval       time.Duration
	// AdaptiveMaxInterval caps the stretched interval of feeds with
	// adaptive_interval enabled (also bounded by MaxPollInterval).
	AdaptiveMaxInterval time.Duration
	WorkerInstanceID    string

	MaxFilterRulesPerFilter int
	MaxRegexLength          int

	// Editor quotas (0 = unlimited; admins are exempt).
	MaxFeedsPerEditor     int
	MaxWebhooksPerEditor  int
	EditorMinPollInterval time.Duration

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
	HSTSEnabled bool
	// SessionMaxAge is the sliding lifetime of a UI session (cookie and
	// sessions.expires_at); each request past SessionTouchInterval extends it.
	SessionMaxAge    time.Duration
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
	env := &envReader{}
	cfg := Config{
		DatabaseURL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
		DatabaseMaxConns: env.int("DATABASE_MAX_CONNS", 0),
		ListenAddr:       getEnv("LISTEN_ADDR", ":8080"),
		LogLevel:         strings.ToLower(getEnv("LOG_LEVEL", "info")),
		LogFormat:        strings.ToLower(getEnv("LOG_FORMAT", "json")), // json|text
		RunMigrations:    parseBool(os.Getenv("RUN_MIGRATIONS")),

		AllowDevToken: parseBool(os.Getenv("ALLOW_DEV_TOKEN")),
		MetricsToken:  strings.TrimSpace(os.Getenv("METRICS_TOKEN")),

		AdminUsername: strings.TrimSpace(os.Getenv("ADMIN_USERNAME")),
		AdminPassword: strings.TrimSpace(os.Getenv("ADMIN_PASSWORD")),

		FetchAllowPrivateNetwork:   parseBool(os.Getenv("FETCH_ALLOW_PRIVATE_NETWORK")),
		FetchAllowedCIDRs:          parseCSV(os.Getenv("FETCH_ALLOWED_CIDRS")),
		FetchBlockedHosts:          parseCSV(os.Getenv("FETCH_BLOCKED_HOSTS")),
		FetchTLSInsecureSkipVerify: parseBool(env.get("FETCH_TLS_INSECURE", "FETCH_INSECURE_SKIP_VERIFY")),
		FetchUserAgent:             getEnv("FETCH_USER_AGENT", "rssam"),
		FetchTimeoutSeconds:        env.int("FETCH_TIMEOUT_SECONDS", 15),
		FetchViaProxyURL:           strings.TrimSpace(os.Getenv("FETCH_VIA_PROXY")),
		ScraperMaxContentBytes:     int64(env.int("SCRAPER_MAX_CONTENT_BYTES", 1048576)),

		FeedCircuitBreakerThreshold: env.int("FEED_CIRCUIT_BREAKER_THRESHOLD", 10),
		FeedPollDailyResetEnabled:   parseBoolDefault(os.Getenv("FEED_POLL_DAILY_RESET")),
		FeedPollDailyResetTZ:        getEnv("FEED_POLL_DAILY_RESET_TZ", "Europe/Moscow"),

		MaxAPIBaseURL:        strings.TrimSpace(os.Getenv("MAX_API_BASE_URL")),
		MaxDefaultLimit:      env.int("MAX_DEFAULT_LIMIT", 100),
		MaxDefaultLookback:   env.duration("MAX_DEFAULT_LOOKBACK", 24*time.Hour, "MAX_DEFAULT_LOOKBACK_MS"),
		MaxOverlap:           env.duration("MAX_OVERLAP", 2*time.Minute, "MAX_OVERLAP_MS"),
		MaxRateLimitSeconds:  env.int("MAX_RATE_LIMIT_SECONDS", 60),
		MaxRequestIntervalMs: env.int("MAX_REQUEST_INTERVAL_MS", 1000),
		MaxConcurrentSlots:   env.int("MAX_CONCURRENT_SLOTS", 1),
		MaxAllowPrivateAPI:   parseBool(os.Getenv("MAX_ALLOW_PRIVATE_API")),

		MaxstatAccessToken:      strings.TrimSpace(os.Getenv("MAXSTAT_ACCESS_TOKEN")),
		MaxstatAPIBaseURL:       strings.TrimSpace(getEnv("MAXSTAT_API_BASE_URL", "https://maxstat.ru/api/v1")),
		MaxstatDefaultLimit:     env.int("MAXSTAT_DEFAULT_LIMIT", 100),
		MaxstatDefaultLookback:  env.duration("MAXSTAT_DEFAULT_LOOKBACK", 24*time.Hour),
		MaxstatOverlap:          env.duration("MAXSTAT_OVERLAP", 2*time.Minute),
		MaxstatRateLimitSeconds: env.int("MAXSTAT_RATE_LIMIT_SECONDS", 1800),

		TelegramProxyServiceURL:     strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_SERVICE_URL")),
		TelegramProxyServiceToken:   strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_SERVICE_TOKEN")),
		TelegramProxyTargetURL:      strings.TrimSpace(os.Getenv("TELEGRAM_PROXY_TARGET_URL")),
		TelegramStaticProxy:         strings.TrimSpace(os.Getenv("TELEGRAM_STATIC_PROXY")),
		TelegramProxyConnectTimeout: env.duration("TELEGRAM_PROXY_CONNECT_TIMEOUT", 10*time.Second),
		TelegramProxyRequestTimeout: env.duration("TELEGRAM_PROXY_REQUEST_TIMEOUT", 25*time.Second),
		TelegramProxyRetry:          env.int("TELEGRAM_PROXY_RETRY", 1),
		TelegramMaxPages:            env.int("TELEGRAM_MAX_PAGES", 1),
		TelegramConcurrentSlots:     env.int("TELEGRAM_CONCURRENT_SLOTS", 4),

		VKAccessToken:      strings.TrimSpace(os.Getenv("VK_ACCESS_TOKEN")),
		VKAPIVersion:       getEnv("VK_API_VERSION", "5.199"),
		VKDefaultCount:     env.int("VK_DEFAULT_COUNT", 100),
		VKDefaultLookback:  env.duration("VK_DEFAULT_LOOKBACK", 24*time.Hour),
		VKOverlap:          env.duration("VK_OVERLAP", 2*time.Minute),
		VKRateLimitSeconds: env.int("VK_RATE_LIMIT_SECONDS", 5),

		RutubeAPIBaseURL: strings.TrimSpace(getEnv("RUTUBE_API_BASE_URL", "https://rutube.ru/api")),

		DzenSearchURL: strings.TrimSpace(getEnv("DZEN_SEARCH_URL", "https://dzen.ru/news/search")),
		DzenUserAgent: strings.TrimSpace(os.Getenv("DZEN_USER_AGENT")),
		DzenCookie:    strings.TrimSpace(getEnv("DZEN_COOKIE", "zen_sso_checked=1")),

		RemovedRetentionDays:     env.int("REMOVED_RETENTION_DAYS", 30),
		WebhookLogRetentionDays:  env.int("WEBHOOK_LOG_RETENTION_DAYS", 90),
		FilterMatchRetentionDays: env.int("FILTER_MATCH_RETENTION_DAYS", 90),
		FeedPollLogRetentionDays: env.int("FEED_POLL_LOG_RETENTION_DAYS", 14),
		FeedSilentDays:           env.int("FEED_SILENT_DAYS", 7),
		BackupDir:                strings.TrimSpace(os.Getenv("BACKUP_DIR")),
		BackupMaxAge:             env.duration("BACKUP_MAX_AGE", 36*time.Hour),
		AuditLogRetentionDays:    env.int("AUDIT_LOG_RETENTION_DAYS", 180),
		CleanupInterval:          env.duration("CLEANUP_INTERVAL", 24*time.Hour),

		WorkerPoolSize:      env.int("WORKER_POOL_SIZE", 10),
		SchedulerTick:       env.duration("SCHEDULER_TICK", 30*time.Second),
		MinPollInterval:     env.duration("MIN_POLL_INTERVAL", 60*time.Second),
		MaxPollInterval:     env.duration("MAX_POLL_INTERVAL", 24*time.Hour),
		AdaptiveMaxInterval: env.duration("ADAPTIVE_MAX_INTERVAL", 6*time.Hour),
		WorkerInstanceID:    strings.TrimSpace(os.Getenv("WORKER_INSTANCE_ID")),

		MaxFilterRulesPerFilter: env.int("MAX_FILTER_RULES_PER_FILTER", 50),
		MaxRegexLength:          env.int("MAX_REGEX_LENGTH", 2048),
		MaxFeedsPerEditor:       env.int("MAX_FEEDS_PER_EDITOR", 0),
		MaxWebhooksPerEditor:    env.int("MAX_WEBHOOKS_PER_EDITOR", 0),
		EditorMinPollInterval:   env.duration("EDITOR_MIN_POLL_INTERVAL", 0),

		WebhookMaxAttempts: env.int("WEBHOOK_MAX_ATTEMPTS", 10),
		WebhookTimeout:     env.duration("WEBHOOK_TIMEOUT", 10*time.Second),
		WebhookRetryBase:   env.duration("WEBHOOK_RETRY_BASE", 5*time.Second),
		WebhookRetryMax:    env.duration("WEBHOOK_RETRY_MAX", time.Hour),
		WSEnabled:          parseBool(getEnv("WS_ENABLED", "true")),
		WSClientBuffer:     env.int("WS_CLIENT_BUFFER", 100),
		WSPingInterval:     env.duration("WS_PING_INTERVAL", 30*time.Second),

		UIEnabled: parseBoolDefault(os.Getenv("UI_ENABLED")),

		FTSLanguage:      strings.ToLower(getEnv("FTS_LANGUAGE", "russian")),
		StoreEntriesMode: env.storeEntriesMode(),

		HSTSEnabled:         parseBool(os.Getenv("HSTS")),
		SessionMaxAge:       env.duration("SESSION_MAX_AGE", 30*24*time.Hour),
		TrustedProxies:      parseTrustedProxies(),
		RateLimitEnabled:    parseBoolDefault(os.Getenv("RATE_LIMIT_ENABLED")),
		RateLimitRPS:        env.float("RATE_LIMIT_RPS", 10),
		RateLimitBurst:      env.int("RATE_LIMIT_BURST", 20),
		LoginRateLimitRPS:   env.float("LOGIN_RATE_LIMIT_RPS", 0.2),
		LoginRateLimitBurst: env.int("LOGIN_RATE_LIMIT_BURST", 10),
		MaxRequestBodyBytes: int64(env.int("MAX_REQUEST_BODY_BYTES", 1048576)),
		MaxImportFeeds:      env.int("MAX_IMPORT_FEEDS", 500),
		CompressEnabled:     parseBoolDefault(os.Getenv("COMPRESS_ENABLED")),
		PprofEnabled:        parseBool(os.Getenv("PPROF_ENABLED")),
		PprofListenAddr:     getEnv("PPROF_LISTEN_ADDR", "127.0.0.1:6060"),
		ShutdownTimeout:     env.duration("SHUTDOWN_TIMEOUT", 15*time.Second),
	}

	cfg.WebhookWorkerPoolSize = env.int("WEBHOOK_WORKER_POOL_SIZE", cfg.WorkerPoolSize)
	cfg.Warnings = env.warns
	if strings.TrimSpace(os.Getenv("AUTH_TOKEN")) != "" {
		env.errs = append(env.errs, errors.New("AUTH_TOKEN is no longer supported: remove it from .env and use a personal API key (Settings → API keys or POST /v1/me/api-keys)"))
	}

	if err := errors.Join(env.errs...); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	for _, f := range []struct {
		name  string
		value string
	}{
		{"LISTEN_ADDR", c.ListenAddr},
		{"LOG_LEVEL", c.LogLevel},
		{"FETCH_USER_AGENT", c.FetchUserAgent},
		{"PPROF_LISTEN_ADDR", c.PprofListenAddr},
	} {
		if strings.TrimSpace(f.value) == "" {
			fail("%s must not be empty", f.name)
		}
	}
	if c.DatabaseURL == "" {
		fail("DATABASE_URL is required")
	}

	const day = 24 * time.Hour
	for _, r := range []struct {
		name   string
		v      int64
		lo, hi int64
		note   string
	}{
		{"DATABASE_MAX_CONNS", int64(c.DatabaseMaxConns), 0, 500, " (0 = auto)"},
		{"FETCH_TIMEOUT_SECONDS", int64(c.FetchTimeoutSeconds), 1, 300, ""},
		{"SCRAPER_MAX_CONTENT_BYTES", c.ScraperMaxContentBytes, 1024, 10 * 1024 * 1024, ""},
		{"MAX_DEFAULT_LIMIT", int64(c.MaxDefaultLimit), 1, 200, ""},
		{"MAX_RATE_LIMIT_SECONDS", int64(c.MaxRateLimitSeconds), 1, 86400, ""},
		{"MAX_REQUEST_INTERVAL_MS", int64(c.MaxRequestIntervalMs), 0, 60000, ""},
		{"MAX_CONCURRENT_SLOTS", int64(c.MaxConcurrentSlots), 1, 32, ""},
		{"TELEGRAM_PROXY_RETRY", int64(c.TelegramProxyRetry), 0, 20, ""},
		{"TELEGRAM_MAX_PAGES", int64(c.TelegramMaxPages), 1, 100, ""},
		{"TELEGRAM_CONCURRENT_SLOTS", int64(c.TelegramConcurrentSlots), 1, 32, ""},
		{"VK_DEFAULT_COUNT", int64(c.VKDefaultCount), 1, 100, ""},
		{"VK_RATE_LIMIT_SECONDS", int64(c.VKRateLimitSeconds), 1, 86400, ""},
		{"MAXSTAT_DEFAULT_LIMIT", int64(c.MaxstatDefaultLimit), 1, 100, ""},
		{"MAXSTAT_RATE_LIMIT_SECONDS", int64(c.MaxstatRateLimitSeconds), 1, 86400, ""},
		{"REMOVED_RETENTION_DAYS", int64(c.RemovedRetentionDays), 1, 3650, ""},
		{"WEBHOOK_LOG_RETENTION_DAYS", int64(c.WebhookLogRetentionDays), 1, 3650, ""},
		{"FILTER_MATCH_RETENTION_DAYS", int64(c.FilterMatchRetentionDays), 0, 3650, " (0 disables)"},
		{"FEED_SILENT_DAYS", int64(c.FeedSilentDays), 0, 3650, " (0 disables)"},
		{"FEED_POLL_LOG_RETENTION_DAYS", int64(c.FeedPollLogRetentionDays), 0, 3650, " (0 disables)"},
		{"AUDIT_LOG_RETENTION_DAYS", int64(c.AuditLogRetentionDays), 0, 3650, " (0 disables)"},
		{"WORKER_POOL_SIZE", int64(c.WorkerPoolSize), 1, 1000, ""},
		{"WEBHOOK_WORKER_POOL_SIZE", int64(c.WebhookWorkerPoolSize), 1, 1000, ""},
		{"MAX_FILTER_RULES_PER_FILTER", int64(c.MaxFilterRulesPerFilter), 1, 10000, ""},
		{"MAX_REGEX_LENGTH", int64(c.MaxRegexLength), 1, 1048576, ""},
		{"MAX_FEEDS_PER_EDITOR", int64(c.MaxFeedsPerEditor), 0, 100000, ""},
		{"MAX_WEBHOOKS_PER_EDITOR", int64(c.MaxWebhooksPerEditor), 0, 10000, ""},
		{"WEBHOOK_MAX_ATTEMPTS", int64(c.WebhookMaxAttempts), 1, 1000, ""},
		{"WS_CLIENT_BUFFER", int64(c.WSClientBuffer), 1, 100000, ""},
		{"RATE_LIMIT_BURST", int64(c.RateLimitBurst), 1, 100000, ""},
		{"LOGIN_RATE_LIMIT_BURST", int64(c.LoginRateLimitBurst), 1, 10000, ""},
		{"MAX_REQUEST_BODY_BYTES", c.MaxRequestBodyBytes, 1024, 32 * 1024 * 1024, ""},
		{"MAX_IMPORT_FEEDS", int64(c.MaxImportFeeds), 1, 100000, ""},
	} {
		if r.v < r.lo || r.v > r.hi {
			fail("%s must be between %d and %d%s, got %d", r.name, r.lo, r.hi, r.note, r.v)
		}
	}

	for _, r := range []struct {
		name   string
		v      time.Duration
		lo, hi time.Duration
	}{
		{"MAX_DEFAULT_LOOKBACK", c.MaxDefaultLookback, time.Millisecond, 30 * day},
		{"MAX_OVERLAP", c.MaxOverlap, 0, day},
		{"TELEGRAM_PROXY_CONNECT_TIMEOUT", c.TelegramProxyConnectTimeout, time.Millisecond, 5 * time.Minute},
		{"TELEGRAM_PROXY_REQUEST_TIMEOUT", c.TelegramProxyRequestTimeout, time.Millisecond, 5 * time.Minute},
		{"VK_DEFAULT_LOOKBACK", c.VKDefaultLookback, time.Millisecond, 30 * day},
		{"VK_OVERLAP", c.VKOverlap, 0, day},
		{"MAXSTAT_DEFAULT_LOOKBACK", c.MaxstatDefaultLookback, time.Millisecond, 30 * day},
		{"MAXSTAT_OVERLAP", c.MaxstatOverlap, 0, day},
		{"BACKUP_MAX_AGE", c.BackupMaxAge, time.Hour, math.MaxInt64},
		{"SESSION_MAX_AGE", c.SessionMaxAge, 5 * time.Minute, 365 * day},
		{"CLEANUP_INTERVAL", c.CleanupInterval, time.Millisecond, 7 * day},
		{"SCHEDULER_TICK", c.SchedulerTick, time.Millisecond, math.MaxInt64},
		{"MIN_POLL_INTERVAL", c.MinPollInterval, time.Millisecond, math.MaxInt64},
		{"MAX_POLL_INTERVAL", c.MaxPollInterval, time.Millisecond, math.MaxInt64},
		{"ADAPTIVE_MAX_INTERVAL", c.AdaptiveMaxInterval, time.Millisecond, math.MaxInt64},
		{"EDITOR_MIN_POLL_INTERVAL", c.EditorMinPollInterval, 0, c.MaxPollInterval},
		{"WEBHOOK_TIMEOUT", c.WebhookTimeout, time.Millisecond, 300 * time.Second},
		{"WEBHOOK_RETRY_BASE", c.WebhookRetryBase, time.Millisecond, math.MaxInt64},
		{"WEBHOOK_RETRY_MAX", c.WebhookRetryMax, time.Millisecond, math.MaxInt64},
		{"WS_PING_INTERVAL", c.WSPingInterval, time.Millisecond, 10 * time.Minute},
		{"SHUTDOWN_TIMEOUT", c.ShutdownTimeout, time.Millisecond, 5 * time.Minute},
	} {
		switch {
		case r.v < r.lo || r.v > r.hi:
			if r.hi == math.MaxInt64 {
				fail("%s must be at least %s, got %s", r.name, shortDuration(r.lo), shortDuration(r.v))
			} else {
				fail("%s must be between %s and %s, got %s", r.name, shortDuration(r.lo), shortDuration(r.hi), shortDuration(r.v))
			}
		}
	}
	if c.MinPollInterval > 0 && c.MaxPollInterval > 0 && c.MinPollInterval > c.MaxPollInterval {
		fail("MIN_POLL_INTERVAL must be <= MAX_POLL_INTERVAL (got %s > %s)", c.MinPollInterval, c.MaxPollInterval)
	}
	if c.WebhookRetryBase > 0 && c.WebhookRetryMax > 0 && c.WebhookRetryBase > c.WebhookRetryMax {
		fail("WEBHOOK_RETRY_BASE must be <= WEBHOOK_RETRY_MAX (got %s > %s)", c.WebhookRetryBase, c.WebhookRetryMax)
	}

	if c.RateLimitRPS <= 0 || c.RateLimitRPS > 10000 {
		fail("RATE_LIMIT_RPS must be between 0.01 and 10000, got %v", c.RateLimitRPS)
	}
	if c.LoginRateLimitRPS <= 0 || c.LoginRateLimitRPS > 1000 {
		fail("LOGIN_RATE_LIMIT_RPS must be between 0.001 and 1000, got %v", c.LoginRateLimitRPS)
	}
	switch c.LogFormat {
	case "json", "text":
	default:
		fail("LOG_FORMAT must be json or text, got %q", c.LogFormat)
	}
	switch c.FTSLanguage {
	case "", "simple", "russian", "ru":
	default:
		fail("FTS_LANGUAGE must be simple or russian, got %q", c.FTSLanguage)
	}
	switch strings.ToLower(strings.TrimSpace(c.StoreEntriesMode)) {
	case "full", "dedup_only":
	default:
		fail("STORE_ENTRIES_MODE must be full or dedup_only, got %q", c.StoreEntriesMode)
	}

	return joinErrors(errs)
}

// shortDuration renders 720h0m0s as 720h: bounds in error messages read
// like the values people type into .env.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
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

func (e *envReader) storeEntriesMode() string {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("STORE_ENTRIES_MODE"))); v != "" {
		return v
	}
	if parseBool(os.Getenv("DEDUP_ONLY_STORAGE")) {
		e.deprecated("DEDUP_ONLY_STORAGE", "STORE_ENTRIES_MODE=dedup_only")
		return "dedup_only"
	}
	return "full"
}

// parseBoolDefault reads a flag that is on unless explicitly disabled.
func parseBoolDefault(v string) bool {
	if strings.TrimSpace(v) == "" {
		return true
	}
	return parseBool(v)
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

// IsPlaceholderPassword reports whether password is the ADMIN_PASSWORD value
// shipped in .env.example. A fresh database refuses to bootstrap the admin
// with it (unless ALLOW_DEV_TOKEN=true), and a UI login with it lands on the
// password form.
func IsPlaceholderPassword(password string) bool {
	return strings.EqualFold(strings.TrimSpace(password), "changeme")
}

// envReader parses numeric settings and records every malformed value so
// Load can refuse to start with the variable named. A silent fallback to the
// default (the previous behaviour) hid typos like FETCH_TIMEOUT_SECONDS=15s
// or WEBHOOK_TIMEOUT=10 until the service misbehaved in production.
type envReader struct {
	errs  []error
	warns []string
}

func (e *envReader) deprecated(old, replacement string) {
	e.warns = append(e.warns, old+" is deprecated, use "+replacement)
}

// get returns the trimmed value of key, falling back to the deprecated
// aliases (recorded as a warning when one is used).
func (e *envReader) get(key string, aliases ...string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	for _, a := range aliases {
		if v := strings.TrimSpace(os.Getenv(a)); v != "" {
			e.deprecated(a, key)
			return v
		}
	}
	return ""
}

func (e *envReader) int(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %q is not an integer", key, v))
		return def
	}
	return n
}

func (e *envReader) float(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %q is not a number", key, v))
		return def
	}
	return n
}

func (e *envReader) duration(key string, def time.Duration, aliases ...string) time.Duration {
	v := e.get(key, aliases...)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("%s: %q is not a duration (use 30s, 5m, 24h)", key, v))
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
