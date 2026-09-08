package httpserver

import (
	"context"
	"fmt"
	"net/http"

	"rssam/internal/bridgeconfig"
	"rssam/internal/http/middleware"
	"rssam/internal/http/ui"
	"rssam/internal/storage"
)

func (s *Server) registerUI(mux *http.ServeMux) {
	if !s.uiEnabled {
		return
	}
	cfg := ui.Config{
		Logger:         s.log,
		Users:          s.users,
		Sessions:       s.sessions,
		Categories:     s.categories,
		Feeds:          s.feeds,
		Entries:        s.entries,
		Filters:        s.filters,
		FilterMatches:  s.filterMatches,
		Labels:         s.labels,
		Webhooks:       s.webhooks,
		WebhookLogs:    s.webhookLogs,
		FilterEngine:   s.filterEngine,
		Refresher:      s.refresher,
		ContentFetcher: s.contentFetcher,
		SSRFGuard:      s.ssrfGuard,
		CSRFSecret:     s.csrfSecret,
		HSTSEnabled:    s.hstsEnabled,
		MaxImportFeeds: s.maxImportFeeds,
		RateLimit:      middleware.PerIPRateLimit(s.loginLimiter),
		OPMLImport: func(r *http.Request, userID int64, data []byte) ui.ImportReport {
			rep := s.importOPMLFromBytes(r, userID, data)
			out := ui.ImportReport{
				CategoriesCreated: rep.CategoriesCreated,
				FeedsCreated:      rep.FeedsCreated,
				FeedsSkipped:      rep.FeedsSkipped,
			}
			for _, e := range rep.Errors {
				msg := e.Reason
				if e.FeedURL != "" {
					msg = e.FeedURL + ": " + msg
				}
				out.Errors = append(out.Errors, msg)
			}
			return out
		},
		OPMLExport: func(w http.ResponseWriter, r *http.Request, userID int64) error {
			return s.exportOPMLForUser(w, r.Context(), userID)
		},
		RefreshAllFeeds: func(r *http.Request, userID int64) error {
			return s.refreshAllFeedsForUser(r, userID)
		},
		TestWebhook: func(r *http.Request, userID, webhookID int64) (ui.WebhookTestResult, error) {
			res, err := s.runWebhookTest(r.Context(), userID, webhookID)
			if err != nil {
				return ui.WebhookTestResult{}, err
			}
			return ui.WebhookTestResult{OK: res.OK, Message: formatWebhookTestMessage(res)}, nil
		},
		ResolveFeedTitle: func(ctx context.Context, feedURL, feedType string, tlsInsecure bool) (string, error) {
			if s.titleResolver == nil {
				return "", nil
			}
			return s.titleResolver.DiscoverTitle(ctx, feedURL, feedType, tlsInsecure)
		},
		GetBridgeSettings: func() ui.BridgeSettings {
			if s.bridgeManager == nil {
				return ui.BridgeSettings{}
			}
			return ui.BridgeSettingsFromRuntime(s.bridgeManager.Runtime())
		},
		GetBridgeStored: func() bridgeconfig.Stored {
			if s.bridgeManager == nil {
				return bridgeconfig.Stored{}
			}
			return s.bridgeManager.Stored()
		},
		SaveBridgeSettings: func(ctx context.Context, stored bridgeconfig.Stored) error {
			if s.bridgeManager == nil {
				return fmt.Errorf("bridge manager not configured")
			}
			return s.bridgeManager.Save(ctx, stored)
		},
	}
	if af, ok := s.feeds.(storage.AdminFeedStore); ok {
		cfg.AdminFeeds = af
	}
	if aw, ok := s.webhooks.(storage.AdminWebhookStore); ok {
		cfg.AdminWebhooks = aw
	}
	if s.webhookMaxAttempts > 0 {
		cfg.WebhookMaxAttempts = s.webhookMaxAttempts
	} else {
		cfg.WebhookMaxAttempts = 10
	}
	cfg.WorkerPoolSize = s.workerPoolSize
	cfg.WebhookWorkerPoolSize = s.webhookWorkerPoolSize
	cfg.FetchTimeoutSeconds = s.fetchTimeoutSec
	cfg.EnvFilePath = s.envFilePath
	cfg.Retention = s.retention
	cfg.RunRetentionCleanup = s.runRetentionCleanup
	cfg.DatabaseURL = s.databaseURL
	cfg.GitHubRepo = s.gitHubRepo
	if d, ok := s.entries.(storage.EntryDedupStore); ok {
		cfg.Dedup = d
	}
	if s.workerControl != nil {
		cfg.PauseWorkers = s.workerControl.Pause
		cfg.ResumeWorkers = s.workerControl.Resume
		cfg.WorkersPaused = s.workerControl.Paused
	}
	h, err := ui.NewHandler(cfg)
	if err != nil {
		s.log.Error("ui handler init failed", "err", err)
		return
	}
	h.Register(mux)
	s.log.Info("web ui enabled", "path", "/ui/")
}

func (s *Server) refreshAllFeedsForUser(r *http.Request, userID int64) error {
	u, err := s.users.GetUser(r.Context(), userID)
	if err != nil || !u.IsAdmin {
		return errForbidden
	}
	if s.refreshAll == nil {
		return fmt.Errorf("job queue is not configured")
	}
	_, _, err = s.refreshAll.EnqueueRefreshAllPollJobs(r.Context())
	return err
}

var errForbidden = &forbiddenError{}

type forbiddenError struct{}

func (e *forbiddenError) Error() string { return "forbidden" }
