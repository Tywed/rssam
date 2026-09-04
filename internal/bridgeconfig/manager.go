package bridgeconfig

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"rssam/internal/config"
	"rssam/internal/reader/max"
	"rssam/internal/reader/rutube"
	"rssam/internal/reader/telegram"
	"rssam/internal/reader/vk"
	"rssam/internal/storage"
)

// Handlers holds bridge handler references for hot config reload.
type Handlers struct {
	Telegram *telegram.Handler
	Max      *max.Handler
	VK       *vk.Handler
	Rutube   *rutube.Handler
}

// Manager merges env + DB bridge settings and applies them to handlers.
type Manager struct {
	mu      sync.RWMutex
	env     config.Config
	store   storage.AppSettingsStore
	runtime Runtime
	h       Handlers
}

func NewManager(env config.Config, store storage.AppSettingsStore, h Handlers) *Manager {
	m := &Manager{
		env:   env,
		store: store,
		h:     h,
	}
	m.runtime = MergeEnv(env, Stored{})
	return m
}

func (m *Manager) Load(ctx context.Context) error {
	if m.store == nil {
		m.mu.Lock()
		m.runtime = MergeEnv(m.env, Stored{})
		m.mu.Unlock()
		m.apply()
		return nil
	}
	raw, err := m.store.GetAppSetting(ctx, SettingsKey)
	if err != nil && err != storage.ErrNotFound {
		return err
	}
	stored := ParseStored(raw)
	m.mu.Lock()
	m.runtime = MergeEnv(m.env, stored)
	m.mu.Unlock()
	m.apply()
	return nil
}

func (m *Manager) Runtime() Runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtime
}

func (m *Manager) Stored() Stored {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return RuntimeToStored(m.runtime)
}

func (m *Manager) Save(ctx context.Context, stored Stored) error {
	raw, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	if m.store != nil {
		if err := m.store.SetAppSetting(ctx, SettingsKey, raw); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.runtime = MergeEnv(m.env, stored)
	m.mu.Unlock()
	m.apply()
	return nil
}

func (m *Manager) SaveRuntime(ctx context.Context, rt Runtime) error {
	return m.Save(ctx, RuntimeToStored(rt))
}

func (m *Manager) apply() {
	m.mu.RLock()
	rt := m.runtime
	h := m.h
	m.mu.RUnlock()

	if h.Telegram != nil {
		h.Telegram.UpdateConfig(telegram.Config{
			ProxyServiceURL:       rt.Telegram.ProxyServiceURL,
			ProxyServiceToken:     rt.Telegram.ProxyServiceToken,
			ProxyTargetURL:        rt.Telegram.ProxyTargetURL,
			StaticProxy:           rt.Telegram.StaticProxy,
			UseProxy:              rt.Telegram.UseProxy,
			ProxyConnectTimeout:   rt.Telegram.ConnectTimeout,
			ProxyRequestTimeout:   rt.Telegram.RequestTimeout,
			ProxyRetry:            rt.Telegram.ProxyRetry,
			MaxPages:              rt.Telegram.MaxPages,
			UserAgent:             m.env.FetchUserAgent,
			TLSInsecureSkipVerify: m.env.FetchTLSInsecureSkipVerify,
		})
	}
	if h.Max != nil {
		_ = h.Max.UpdateConfig(max.Config{
			APIBaseURL:        rt.Max.APIBaseURL,
			DefaultLimit:      rt.Max.DefaultLimit,
			DefaultLookback:   rt.Max.DefaultLookback,
			Overlap:           rt.Max.Overlap,
			RateLimitCooldown: durationSeconds(rt.Max.RateLimitSeconds),
			RequestInterval:   durationMillis(rt.Max.RequestIntervalMs),
			ConcurrentSlots:   rt.Max.ConcurrentSlots,
			AllowPrivateAPI:   rt.Max.AllowPrivateAPI,
			FetchAllowPrivate: m.env.FetchAllowPrivateNetwork,
		})
	}
	if h.VK != nil {
		h.VK.UpdateConfig(vk.Config{
			AccessToken:       rt.VK.AccessToken,
			APIVersion:        rt.VK.APIVersion,
			DefaultCount:      rt.VK.DefaultCount,
			DefaultLookback:   rt.VK.DefaultLookback,
			Overlap:           rt.VK.Overlap,
			RateLimitCooldown: durationSeconds(rt.VK.RateLimitSeconds),
		})
	}
	if h.Rutube != nil {
		h.Rutube.UpdateConfig(rutube.Config{
			APIBaseURL: rt.Rutube.APIBaseURL,
			UserAgent:  m.env.FetchUserAgent,
		})
	}
}

func durationSeconds(sec int) time.Duration {
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec) * time.Second
}

func durationMillis(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
