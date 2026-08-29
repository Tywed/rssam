package config

import (
	"os"
	"testing"
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
	os.Unsetenv("CLEANUP_INTERVAL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr=%q", cfg.ListenAddr)
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
	t.Setenv("STORE_ENTRIES_MODE", "")
	t.Setenv("DEDUP_ONLY_STORAGE", "")
	if got := parseStoreEntriesMode(); got != "full" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("DEDUP_ONLY_STORAGE", "true")
	if got := parseStoreEntriesMode(); got != "dedup_only" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("DEDUP_ONLY_STORAGE", "")
	t.Setenv("STORE_ENTRIES_MODE", "dedup_only")
	if got := parseStoreEntriesMode(); got != "dedup_only" {
		t.Fatalf("got %q", got)
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

	t.Setenv("FETCH_TLS_INSECURE", "")
	t.Setenv("FETCH_INSECURE_SKIP_VERIFY", "yes")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.FetchTLSInsecureSkipVerify {
		t.Fatal("expected alias FETCH_INSECURE_SKIP_VERIFY=true")
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
