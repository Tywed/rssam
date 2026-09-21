package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoad_RequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("LISTEN_ADDR", ":8080")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestParseBool(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"no":    false,
		"true":  true,
		"1":     true,
		"YES":   true,
		"On":    true,
		" off ": false,
	}
	for in, want := range cases {
		if got := parseBool(in); got != want {
			t.Fatalf("parseBool(%q)=%v, want %v", in, got, want)
		}
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	os.Unsetenv("LISTEN_ADDR")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("LOG_FORMAT")
	os.Unsetenv("FETCH_USER_AGENT")
	os.Unsetenv("FETCH_TIMEOUT_SECONDS")
	os.Unsetenv("WORKER_POOL_SIZE")
	os.Unsetenv("WEBHOOK_WORKER_POOL_SIZE")
	os.Unsetenv("SCHEDULER_TICK")
	os.Unsetenv("MIN_POLL_INTERVAL")
	os.Unsetenv("MAX_POLL_INTERVAL")
	os.Unsetenv("MAX_FILTER_RULES_PER_FILTER")
	os.Unsetenv("MAX_REGEX_LENGTH")
	os.Unsetenv("WS_ENABLED")
	os.Unsetenv("WS_CLIENT_BUFFER")
	os.Unsetenv("WS_PING_INTERVAL")
	os.Unsetenv("FTS_LANGUAGE")
	os.Unsetenv("STORE_ENTRIES_MODE")
	os.Unsetenv("DEDUP_ONLY_STORAGE")
	os.Unsetenv("WEBHOOK_LOG_RETENTION_DAYS")
	os.Unsetenv("FILTER_MATCH_RETENTION_DAYS")
	os.Unsetenv("FEED_POLL_LOG_RETENTION_DAYS")
	os.Unsetenv("CLEANUP_INTERVAL")
	os.Unsetenv("UI_ENABLED")
	os.Unsetenv("MAX_DEFAULT_LOOKBACK")
	os.Unsetenv("MAX_DEFAULT_LOOKBACK_MS")
	os.Unsetenv("MAX_OVERLAP")
	os.Unsetenv("MAX_OVERLAP_MS")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr=%q", cfg.ListenAddr)
	}
	if !cfg.UIEnabled {
		t.Fatal("UIEnabled must default to true")
	}
	if cfg.MaxDefaultLookback != 24*time.Hour || cfg.MaxOverlap != 2*time.Minute {
		t.Fatalf("MaxDefaultLookback=%s MaxOverlap=%s", cfg.MaxDefaultLookback, cfg.MaxOverlap)
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("Warnings=%v", cfg.Warnings)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel=%q", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("LogFormat=%q", cfg.LogFormat)
	}
	if cfg.RemovedRetentionDays != 30 {
		t.Fatalf("RemovedRetentionDays=%d", cfg.RemovedRetentionDays)
	}
	if cfg.WebhookLogRetentionDays != 90 {
		t.Fatalf("WebhookLogRetentionDays=%d", cfg.WebhookLogRetentionDays)
	}
	if cfg.FilterMatchRetentionDays != 90 {
		t.Fatalf("FilterMatchRetentionDays=%d", cfg.FilterMatchRetentionDays)
	}
	if cfg.FeedPollLogRetentionDays != 14 {
		t.Fatalf("FeedPollLogRetentionDays=%d", cfg.FeedPollLogRetentionDays)
	}
	if cfg.CleanupInterval.String() != "24h0m0s" {
		t.Fatalf("CleanupInterval=%s", cfg.CleanupInterval)
	}
	if cfg.FetchUserAgent != "rssam" {
		t.Fatalf("FetchUserAgent=%q", cfg.FetchUserAgent)
	}
	if cfg.FetchTimeoutSeconds != 15 {
		t.Fatalf("FetchTimeoutSeconds=%d", cfg.FetchTimeoutSeconds)
	}
	if cfg.WorkerPoolSize != 10 {
		t.Fatalf("WorkerPoolSize=%d", cfg.WorkerPoolSize)
	}
	if cfg.EffectiveDatabaseMaxConns() != 28 {
		t.Fatalf("EffectiveDatabaseMaxConns=%d want 28", cfg.EffectiveDatabaseMaxConns())
	}
	if cfg.WebhookWorkerPoolSize != 10 {
		t.Fatalf("WebhookWorkerPoolSize=%d want fallback 10", cfg.WebhookWorkerPoolSize)
	}
	if cfg.SchedulerTick.String() != "5s" {
		t.Fatalf("SchedulerTick=%s", cfg.SchedulerTick)
	}
	if cfg.MinPollInterval.String() != "1m0s" {
		t.Fatalf("MinPollInterval=%s", cfg.MinPollInterval)
	}
	if cfg.MaxPollInterval.String() != "24h0m0s" {
		t.Fatalf("MaxPollInterval=%s", cfg.MaxPollInterval)
	}
	if cfg.MaxFilterRulesPerFilter != 50 {
		t.Fatalf("MaxFilterRulesPerFilter=%d", cfg.MaxFilterRulesPerFilter)
	}
	if cfg.MaxRegexLength != 2048 {
		t.Fatalf("MaxRegexLength=%d", cfg.MaxRegexLength)
	}
	if !cfg.WSEnabled {
		t.Fatalf("WSEnabled=%v", cfg.WSEnabled)
	}
	if cfg.WSClientBuffer != 100 {
		t.Fatalf("WSClientBuffer=%d", cfg.WSClientBuffer)
	}
	if cfg.WSPingInterval.String() != "30s" {
		t.Fatalf("WSPingInterval=%s", cfg.WSPingInterval)
	}
	if cfg.FTSLanguage != "russian" {
		t.Fatalf("FTSLanguage=%q", cfg.FTSLanguage)
	}
	if cfg.StoreEntriesMode != "full" {
		t.Fatalf("StoreEntriesMode=%q", cfg.StoreEntriesMode)
	}
	if !cfg.RateLimitEnabled {
		t.Fatalf("RateLimitEnabled=%v", cfg.RateLimitEnabled)
	}
	if cfg.RateLimitRPS != 10 {
		t.Fatalf("RateLimitRPS=%v", cfg.RateLimitRPS)
	}
	if cfg.RateLimitBurst != 20 {
		t.Fatalf("RateLimitBurst=%d", cfg.RateLimitBurst)
	}
	if cfg.LoginRateLimitRPS != 0.2 || cfg.LoginRateLimitBurst != 10 {
		t.Fatalf("LoginRateLimit=%v/%d", cfg.LoginRateLimitRPS, cfg.LoginRateLimitBurst)
	}
	if cfg.MaxRequestBodyBytes != 1048576 {
		t.Fatalf("MaxRequestBodyBytes=%d", cfg.MaxRequestBodyBytes)
	}
	if !cfg.CompressEnabled {
		t.Fatalf("CompressEnabled=%v", cfg.CompressEnabled)
	}
	if cfg.PprofListenAddr != "127.0.0.1:6060" {
		t.Fatalf("PprofListenAddr=%q", cfg.PprofListenAddr)
	}
}

func TestParseStoreEntriesMode(t *testing.T) {
	env := &envReader{}
	t.Setenv("STORE_ENTRIES_MODE", "")
	t.Setenv("DEDUP_ONLY_STORAGE", "")
	if got := env.storeEntriesMode(); got != "full" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("DEDUP_ONLY_STORAGE", "true")
	if got := env.storeEntriesMode(); got != "dedup_only" {
		t.Fatalf("got %q", got)
	}
	if len(env.warns) != 1 || !strings.Contains(env.warns[0], "DEDUP_ONLY_STORAGE is deprecated") {
		t.Fatalf("warns=%v", env.warns)
	}
	t.Setenv("DEDUP_ONLY_STORAGE", "")
	t.Setenv("STORE_ENTRIES_MODE", "dedup_only")
	if got := env.storeEntriesMode(); got != "dedup_only" {
		t.Fatalf("got %q", got)
	}
	if len(env.warns) != 1 {
		t.Fatalf("warns=%v", env.warns)
	}
}

func TestLoad_FetchTLSInsecure(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("FETCH_TLS_INSECURE", "true")
	os.Unsetenv("FETCH_INSECURE_SKIP_VERIFY")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.FetchTLSInsecureSkipVerify {
		t.Fatal("expected FetchTLSInsecureSkipVerify=true")
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("Warnings=%v", cfg.Warnings)
	}

	t.Setenv("FETCH_TLS_INSECURE", "")
	t.Setenv("FETCH_INSECURE_SKIP_VERIFY", "yes")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.FetchTLSInsecureSkipVerify {
		t.Fatal("expected alias FETCH_INSECURE_SKIP_VERIFY=true")
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "FETCH_INSECURE_SKIP_VERIFY is deprecated, use FETCH_TLS_INSECURE") {
		t.Fatalf("Warnings=%v", cfg.Warnings)
	}
}

func TestLoad_InvalidFTSLanguage(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("FTS_LANGUAGE", "english")

	_, err := Load()
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoad_WebhookWorkerPoolIndependent(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("WORKER_POOL_SIZE", "40")
	t.Setenv("WEBHOOK_WORKER_POOL_SIZE", "5")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WorkerPoolSize != 40 {
		t.Fatalf("WorkerPoolSize=%d", cfg.WorkerPoolSize)
	}
	if cfg.WebhookWorkerPoolSize != 5 {
		t.Fatalf("WebhookWorkerPoolSize=%d", cfg.WebhookWorkerPoolSize)
	}
}

func TestTrustedProxiesEnv(t *testing.T) {
	os.Unsetenv("TRUSTED_PROXIES")
	if got := parseTrustedProxies(); len(got) != 2 {
		t.Fatalf("default should be loopback, got %v", got)
	}
	t.Setenv("TRUSTED_PROXIES", "")
	if got := parseTrustedProxies(); len(got) != 0 {
		t.Fatalf("explicit empty should disable proxy headers, got %v", got)
	}
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.1")
	if got := parseTrustedProxies(); len(got) != 2 || got[1] != "192.168.1.1" {
		t.Fatalf("got %v", got)
	}
}

func TestLoad_FeedPollLogRetentionRange(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	// "0" is documented as "keep forever" for both log-like tables; it must
	// not be treated as "unset".
	t.Setenv("FEED_POLL_LOG_RETENTION_DAYS", "0")
	t.Setenv("FILTER_MATCH_RETENTION_DAYS", "0")
	cfg, err := Load()
	if err != nil || cfg.FeedPollLogRetentionDays != 0 || cfg.FilterMatchRetentionDays != 0 {
		t.Fatalf("0 must disable cleanup: poll=%d filter=%d err=%v", cfg.FeedPollLogRetentionDays, cfg.FilterMatchRetentionDays, err)
	}
	t.Setenv("FEED_POLL_LOG_RETENTION_DAYS", "7")
	if cfg, err := Load(); err != nil || cfg.FeedPollLogRetentionDays != 7 {
		t.Fatalf("7: cfg=%d err=%v", cfg.FeedPollLogRetentionDays, err)
	}
	t.Setenv("FEED_POLL_LOG_RETENTION_DAYS", "3651")
	if _, err := Load(); err == nil {
		t.Fatal("3651 must be rejected")
	}
}

func TestLoad_RejectsMalformedNumbers(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	cases := []struct{ key, val string }{
		{"FEED_POLL_LOG_RETENTION_DAYS", "abc"},
		{"FETCH_TIMEOUT_SECONDS", "15s"},
		{"WEBHOOK_TIMEOUT", "10"},
		{"SESSION_MAX_AGE", "30d"},
		{"RATE_LIMIT_RPS", "ten"},
		{"WORKER_POOL_SIZE", "-3"},
		{"SCHEDULER_TICK", "0s"},
		{"WEBHOOK_WORKER_POOL_SIZE", "x"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			t.Setenv(c.key, c.val)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Fatalf("%s=%q must fail naming the variable, got %v", c.key, c.val, err)
			}
		})
	}
	t.Setenv("FETCH_TIMEOUT_SECONDS", "x")
	t.Setenv("WEBHOOK_TIMEOUT", "y")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "FETCH_TIMEOUT_SECONDS") || !strings.Contains(err.Error(), "WEBHOOK_TIMEOUT") {
		t.Fatalf("all malformed variables must be reported at once, got %v", err)
	}
}

func TestLoad_RefusesAuthToken(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("AUTH_TOKEN", "s3cret-real-token")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AUTH_TOKEN") || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("AUTH_TOKEN must be refused with a hint, got %v", err)
	}
	t.Setenv("AUTH_TOKEN", "")
	if _, err := Load(); err != nil {
		t.Fatalf("empty AUTH_TOKEN is fine: %v", err)
	}
}

func TestLoad_SessionMaxAge(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("SESSION_MAX_AGE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionMaxAge != 30*24*time.Hour {
		t.Fatalf("default SessionMaxAge=%s", cfg.SessionMaxAge)
	}
	t.Setenv("SESSION_MAX_AGE", "12h")
	if cfg, err = Load(); err != nil || cfg.SessionMaxAge != 12*time.Hour {
		t.Fatalf("SessionMaxAge=%s err=%v", cfg.SessionMaxAge, err)
	}
	t.Setenv("SESSION_MAX_AGE", "1m")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "SESSION_MAX_AGE") {
		t.Fatalf("1m must be rejected, got %v", err)
	}
}

func TestIsPlaceholderPassword(t *testing.T) {
	for _, s := range []string{"changeme", " ChangeMe ", "CHANGEME"} {
		if !IsPlaceholderPassword(s) {
			t.Errorf("%q must be recognised", s)
		}
	}
	for _, s := range []string{"", "changeme1", "s3cret-pass"} {
		if IsPlaceholderPassword(s) {
			t.Errorf("%q must not be recognised", s)
		}
	}
}

func TestLoad_UIEnabledFalse(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("UI_ENABLED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.UIEnabled {
		t.Fatal("UI_ENABLED=false must disable the UI")
	}
}

func TestLoad_MaxDurationAliases(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("MAX_DEFAULT_LOOKBACK", "")
	t.Setenv("MAX_DEFAULT_LOOKBACK_MS", "12h")
	t.Setenv("MAX_OVERLAP", "5m")
	t.Setenv("MAX_OVERLAP_MS", "9m")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxDefaultLookback != 12*time.Hour {
		t.Fatalf("MaxDefaultLookback=%s want 12h via alias", cfg.MaxDefaultLookback)
	}
	if cfg.MaxOverlap != 5*time.Minute {
		t.Fatalf("MaxOverlap=%s: new name must win over the alias", cfg.MaxOverlap)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "MAX_DEFAULT_LOOKBACK_MS is deprecated, use MAX_DEFAULT_LOOKBACK") {
		t.Fatalf("Warnings=%v", cfg.Warnings)
	}

	t.Setenv("MAX_DEFAULT_LOOKBACK_MS", "86400000")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MAX_DEFAULT_LOOKBACK") {
		t.Fatalf("raw milliseconds must be rejected, got %v", err)
	}
}
