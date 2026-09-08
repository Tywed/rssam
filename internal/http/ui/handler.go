package ui

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"rssam/internal/bridgeconfig"
	"rssam/internal/filter"
	"rssam/internal/githubrel"
	"rssam/internal/reader"
	"rssam/internal/service"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
	"rssam/web"
)

const defaultPageLimit = 50

type Config struct {
	Logger             *slog.Logger
	Users              storage.UserStore
	Sessions           storage.SessionStore
	Categories         storage.CategoryStore
	Feeds              storage.FeedStore
	Entries            storage.EntryStore
	Filters            storage.FilterStore
	FilterMatches      storage.FilterMatchStore
	Labels             storage.LabelStore
	Webhooks           storage.WebhookStore
	WebhookLogs        storage.WebhookLogStore
	FilterEngine       *filter.Engine
	Refresher          *service.FeedRefresher
	ContentFetcher     *service.ContentFetcher
	SSRFGuard          *ssrf.Guard
	CSRFSecret         string
	HSTSEnabled        bool
	MaxImportFeeds     int
	RateLimit          func(http.Handler) http.Handler
	OPMLImport         func(r *http.Request, userID int64, data []byte) ImportReport
	OPMLExport         func(w http.ResponseWriter, r *http.Request, userID int64) error
	RefreshAllFeeds    func(r *http.Request, userID int64) error
	TestWebhook        func(r *http.Request, userID, webhookID int64) (WebhookTestResult, error)
	ResolveFeedTitle   func(ctx context.Context, feedURL, feedType string, tlsInsecure bool) (string, error)
	GetBridgeSettings  func() BridgeSettings
	GetBridgeStored    func() bridgeconfig.Stored
	SaveBridgeSettings func(ctx context.Context, stored bridgeconfig.Stored) error
	AdminFeeds         storage.AdminFeedStore
	// FeedPollLog is the per-feed poll history shown on the admin feed page
	// (nil = card hidden).
	FeedPollLog           storage.FeedPollLogStore
	AdminWebhooks         storage.AdminWebhookStore
	WebhookMaxAttempts    int
	WorkerPoolSize        int
	WebhookWorkerPoolSize int
	FetchTimeoutSeconds   int
	EnvFilePath           string
	DatabaseURL           string
	GitHubRepo            string
	PauseWorkers          func()
	ResumeWorkers         func()
	WorkersPaused         func() bool
	Dedup                 storage.EntryDedupStore
	// Retention holds the retention windows the running process was started
	// with; shown (and editable via .env) on the admin system page.
	Retention RetentionSettings
	// RunRetentionCleanup triggers one cleanup pass immediately (nil = not
	// available, e.g. workers run in another process).
	RunRetentionCleanup func(ctx context.Context) (storage.RetentionCleanupResult, error)
}

// RetentionSettings mirrors the REMOVED_RETENTION_DAYS /
// WEBHOOK_LOG_RETENTION_DAYS / FILTER_MATCH_RETENTION_DAYS /
// FEED_POLL_LOG_RETENTION_DAYS / CLEANUP_INTERVAL configuration.
type RetentionSettings struct {
	RemovedRetentionDays     int
	WebhookLogRetentionDays  int
	FilterMatchRetentionDays int
	FeedPollLogRetentionDays int
	CleanupInterval          time.Duration
}

// Configured reports whether the settings were supplied by the host process.
func (s RetentionSettings) Configured() bool {
	return s.RemovedRetentionDays > 0 || s.WebhookLogRetentionDays > 0 || s.CleanupInterval > 0
}

type WebhookTestResult struct {
	OK      bool
	Message string
}

type ImportReport struct {
	CategoriesCreated int
	FeedsCreated      int
	FeedsSkipped      int
	Errors            []string
}

type Handler struct {
	cfg         Config
	log         *slog.Logger
	templates   *template.Template
	unreadCache *unreadCountsCache
	releases    *githubrel.Client
}

func NewHandler(cfg Config) (*Handler, error) {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	h := &Handler{
		cfg:         cfg,
		log:         log,
		templates:   tmpl,
		unreadCache: newUnreadCountsCache(),
		releases:    githubrel.New(cfg.GitHubRepo),
	}
	go func() { _, _ = h.releases.Latest() }()
	return h, nil
}

func parseTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"formatTime": formatTime,
		"formatTimeSec": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("02.01.2006 15:04:05")
		},
		"dict": dict,
		"add":  func(a, b int) int { return a + b },
		"sub":  func(a, b int) int { return a - b },
		"deref": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"derefInt64": func(p *int64) string {
			if p == nil {
				return ""
			}
			return strconv.FormatInt(*p, 10)
		},
		"derefInt": func(p *int) string {
			if p == nil {
				return ""
			}
			return strconv.Itoa(*p)
		},
		"feedInCategory": func(f storage.Feed, catID int64) bool {
			if f.CategoryID == nil {
				return false
			}
			return *f.CategoryID == catID
		},
		"hasPrefix": strings.HasPrefix,
		"isReaderMode": func(nav string) bool {
			return nav == "unread" || nav == "search" || nav == "label"
		},
		"entryListQuery":  entryListQuery,
		"entrySortSuffix": entrySortSuffix,
		"catColor": func(c storage.Category) template.CSS {
			color := safeCSSColor(c.Color)
			if color == "" {
				palette := []string{"#c0392b", "#2980b9", "#27ae60", "#8e44ad", "#d35400", "#16a085", "#2c3e50", "#e67e22"}
				if c.ID <= 0 {
					color = palette[0]
				} else {
					color = palette[c.ID%int64(len(palette))]
				}
			}
			return template.CSS(color)
		},
		"labelBgColor": func(l storage.Label) template.CSS {
			if c := safeCSSColor(l.BgColor); c != "" {
				return template.CSS(c)
			}
			return template.CSS("#2980b9")
		},
		"labelFgColor": func(l storage.Label) template.CSS {
			if c := safeCSSColor(l.FgColor); c != "" {
				return template.CSS(c)
			}
			return template.CSS("#ffffff")
		},
		"querySuffix":       querySuffix,
		"formatDuration":    formatDuration,
		"feedTypeLabel":     reader.FeedTypeLabel,
		"bridgeFeedCount":   bridgeFeedCount,
		"feedHasError":      feedHasError,
		"pluralChannels":    pluralChannels,
		"categoryFeedCount": categoryFeedCount,
		"scopeFeedSelected": func(m map[int64]bool, id int64) bool { return m[id] },
		"scopeCatSelected":  func(m map[int64]bool, id int64) bool { return m[id] },
		"categoryTitleByID": func(cats []storage.Category, id *int64) string {
			if id == nil {
				return ""
			}
			for _, c := range cats {
				if c.ID == *id {
					return c.Title
				}
			}
			return ""
		},
		"actionParamSelected": func(actionType, optionKind, savedParam, optionID string) bool {
			return strings.TrimSpace(actionType) == strings.TrimSpace(optionKind) &&
				strings.TrimSpace(savedParam) == strings.TrimSpace(optionID)
		},
		"filterActionOptionValue":      filterActionOptionValue,
		"filterActionSavedOptionValue": filterActionSavedOptionValue,
		"adminFeedStatusLabel":         adminFeedStatusLabel,
		"adminFeedStatusClass":         adminFeedStatusClass,
		"adminFeedsSortLink":           adminFeedsSortLink,
		"adminFeedsPageLink":           adminFeedsPageLink,
		"feedsListPageLink":            feedsListPageLink,
		"adminFeedsSortIndicator":      adminFeedsSortIndicator,
		"truncateStr":                  truncateStr,
		"formatBytes":                  formatBytes,
		"webhookKindLabel":             webhookKindLabel,
		"webhookLabel":                 webhookLabel,
		"adminWebhookStatusLabel":      adminWebhookStatusLabel,
		"adminWebhookStatusClass":      adminWebhookStatusClass,
		"webhookLogStatusLabel":        webhookLogStatusLabel,
		"webhookLogStatusClass":        webhookLogStatusClass,
		"webhookTriggerLabel":          webhookTriggerLabel,
		"classifyWebhookError":         classifyWebhookError,
		"webhooksFilterLink":           webhooksFilterLink,
		"webhookLogsFilterLink":        webhookLogsFilterLink,
		"webhookSuccessRate":           webhookSuccessRate,
		"formatCleanupInterval":        formatCleanupInterval,
		"derefString": func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		},
		"durationValue": func(d time.Duration) string {
			if d <= 0 {
				return ""
			}
			return d.String()
		},
	}
	return template.New("root").Funcs(funcs).ParseFS(web.FS,
		"templates/layouts/*.html",
		"templates/partials/*.html",
		"templates/pages/*.html",
	)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d%(time.Hour) == 0 && d >= time.Hour {
		h := d / time.Hour
		if h == 1 {
			return "1 ч"
		}
		return fmt.Sprintf("%d ч", h)
	}
	if d%(time.Minute) == 0 && d >= time.Minute {
		m := d / time.Minute
		if m == 1 {
			return "1 мин"
		}
		return fmt.Sprintf("%d мин", m)
	}
	if d%(time.Second) == 0 && d >= time.Second {
		s := d / time.Second
		if s == 1 {
			return "1 с"
		}
		return fmt.Sprintf("%d с", s)
	}
	return d.String()
}

func formatTime(t any) string {
	switch v := t.(type) {
	case time.Time:
		return v.Local().Format("02.01.2006 15:04")
	case *time.Time:
		if v == nil {
			return ""
		}
		return v.Local().Format("02.01.2006 15:04")
	default:
		return fmt.Sprint(t)
	}
}

func dict(values ...any) (map[string]any, error) {
	if len(values)%2 != 0 {
		return nil, fmt.Errorf("dict: odd args")
	}
	m := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key not string")
		}
		m[key] = values[i+1]
	}
	return m, nil
}

func querySuffix(q map[string]string) string {
	if len(q) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range q {
		if v == "" {
			continue
		}
		b.WriteString("&")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(v)
	}
	return b.String()
}

func entryListQuery(q map[string]string) string {
	if len(q) == 0 {
		return ""
	}
	var b strings.Builder
	for _, k := range []string{"feed_id", "category_id", "q", "sort"} {
		if v := q[k]; v != "" {
			b.WriteString("&")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(v)
		}
	}
	return b.String()
}

func entrySortSuffix(sort string) string {
	if storage.NormalizeEntrySort(sort) != storage.EntrySortOldest {
		return ""
	}
	return "&sort=oldest"
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /ui/static/", http.StripPrefix("/ui/static/", h.staticHandler()))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/static/favicon.ico", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /ui/login", h.handleLoginGet)
	loginPost := http.HandlerFunc(h.handleLoginPost)
	if h.cfg.RateLimit != nil {
		mux.Handle("POST /ui/login", h.cfg.RateLimit(loginPost))
	} else {
		mux.Handle("POST /ui/login", loginPost)
	}
	mux.HandleFunc("POST /ui/logout", h.handleLogout)

	auth := h.requireAuth
	mux.Handle("GET /ui/", auth(http.HandlerFunc(h.handleRoot)))
	mux.Handle("GET /ui/unread", auth(http.HandlerFunc(h.handleUnread)))
	mux.Handle("POST /ui/unread/mark-read", auth(http.HandlerFunc(h.handleUnreadMarkRead)))
	mux.Handle("GET /ui/search", auth(http.HandlerFunc(h.handleSearch)))
	mux.Handle("GET /ui/entries/{id}/preview", auth(http.HandlerFunc(h.handleEntryPreview)))
	mux.Handle("GET /ui/entries/{id}", auth(http.HandlerFunc(h.handleEntryGet)))
	mux.Handle("POST /ui/entries/{id}/read", auth(http.HandlerFunc(h.handleEntryRead)))
	mux.Handle("POST /ui/entries/{id}/star", auth(http.HandlerFunc(h.handleEntryStar)))

	mux.Handle("GET /ui/feeds/detect-type", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedDetectType))))
	mux.Handle("GET /ui/feeds/suggest", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedSuggest))))
	mux.Handle("GET /ui/feeds", auth(http.HandlerFunc(h.handleFeedsList)))
	mux.Handle("GET /ui/feeds/new", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedNew))))
	mux.Handle("POST /ui/feeds", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedCreate))))
	mux.Handle("GET /ui/feeds/export", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedsExport))))
	mux.Handle("POST /ui/feeds/import", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedsImport))))
	mux.Handle("GET /ui/feeds/{id}", auth(http.HandlerFunc(h.handleFeedShow)))
	mux.Handle("GET /ui/feeds/{id}/edit", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedEdit))))
	mux.Handle("POST /ui/feeds/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedUpdate))))
	mux.Handle("POST /ui/feeds/{id}/delete", auth(h.requireAdmin(http.HandlerFunc(h.handleFeedDelete))))
	mux.Handle("POST /ui/feeds/{id}/refresh", auth(http.HandlerFunc(h.handleFeedRefresh)))
	mux.Handle("POST /ui/feeds/{feedID}/mark-read", auth(http.HandlerFunc(h.handleFeedMarkRead)))

	mux.Handle("GET /ui/categories", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoriesList))))
	mux.Handle("GET /ui/sidebar/categories/{id}/feeds", auth(http.HandlerFunc(h.handleSidebarCategoryFeeds)))
	mux.Handle("GET /ui/sidebar/uncategorized/feeds", auth(http.HandlerFunc(h.handleSidebarUncategorizedFeeds)))
	mux.Handle("GET /ui/feeds/categories/{id}/tree", auth(http.HandlerFunc(h.handleFeedsCategoryTree)))
	mux.Handle("GET /ui/feeds/uncategorized/tree", auth(http.HandlerFunc(h.handleFeedsUncategorizedTree)))
	mux.Handle("POST /ui/categories", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryCreate))))
	mux.Handle("POST /ui/categories/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryUpdate))))
	mux.Handle("POST /ui/categories/{id}/delete", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryDelete))))
	mux.Handle("POST /ui/categories/reorder", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryReorder))))
	mux.Handle("POST /ui/categories/{categoryID}/mark-read", auth(http.HandlerFunc(h.handleCategoryMarkRead)))
	mux.Handle("POST /ui/categories/{categoryID}/feeds/bulk-interval", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryBulkInterval))))
	mux.Handle("POST /ui/categories/{categoryID}/feeds/bulk-webhook", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryBulkWebhook))))
	mux.Handle("POST /ui/categories/{categoryID}/feeds/bulk-hash-only", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryBulkHashOnly))))
	mux.Handle("POST /ui/categories/{categoryID}/feeds/bulk-hash-entries", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryBulkHashEntries))))
	mux.Handle("POST /ui/categories/{categoryID}/feeds/bulk-refresh", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryBulkRefresh))))
	mux.Handle("POST /ui/categories/{categoryID}/feeds/bulk-pause", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryBulkPause))))
	mux.Handle("POST /ui/categories/{categoryID}/feeds/bulk-move", auth(h.requireAdmin(http.HandlerFunc(h.handleCategoryBulkMove))))

	mux.Handle("GET /ui/filters", auth(h.requireAdmin(http.HandlerFunc(h.handleFiltersList))))
	mux.Handle("GET /ui/filters/new", auth(h.requireAdmin(http.HandlerFunc(h.handleFilterNew))))
	mux.Handle("POST /ui/filters", auth(h.requireAdmin(http.HandlerFunc(h.handleFilterCreate))))
	mux.Handle("GET /ui/filters/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleFilterEdit))))
	mux.Handle("POST /ui/filters/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleFilterUpdate))))
	mux.Handle("POST /ui/filters/{id}/delete", auth(h.requireAdmin(http.HandlerFunc(h.handleFilterDelete))))
	mux.Handle("GET /ui/filters/{id}/matches", auth(h.requireAdmin(http.HandlerFunc(h.handleFilterMatches))))
	mux.Handle("GET /ui/filters/{id}/test", auth(h.requireAdmin(http.HandlerFunc(h.handleFilterTest))))

	mux.Handle("GET /ui/labels", auth(h.requireAdmin(http.HandlerFunc(h.handleLabelsList))))
	mux.Handle("GET /ui/labels/{id}", auth(http.HandlerFunc(h.handleLabelEntries)))
	mux.Handle("POST /ui/labels", auth(h.requireAdmin(http.HandlerFunc(h.handleLabelCreate))))
	mux.Handle("POST /ui/labels/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleLabelUpdate))))
	mux.Handle("POST /ui/labels/{id}/delete", auth(h.requireAdmin(http.HandlerFunc(h.handleLabelDelete))))

	mux.Handle("GET /ui/webhooks", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhooksList))))
	mux.Handle("GET /ui/webhooks/new", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookNew))))
	mux.Handle("POST /ui/webhooks", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookCreate))))
	mux.Handle("GET /ui/webhooks/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookEdit))))
	mux.Handle("POST /ui/webhooks/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookUpdate))))
	mux.Handle("POST /ui/webhooks/{id}/delete", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookDelete))))
	mux.Handle("POST /ui/webhooks/{id}/pause", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookPause))))
	mux.Handle("POST /ui/webhooks/{id}/unpause", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookUnpause))))
	mux.Handle("POST /ui/webhooks/{id}/test", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookTest))))
	mux.Handle("GET /ui/webhooks/{id}/logs", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookLogs))))
	mux.Handle("POST /ui/webhooks/{id}/retry-all", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookRetryAll))))
	mux.Handle("POST /ui/webhooks/{id}/reset-stats", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookResetStats))))
	mux.Handle("POST /ui/webhook-logs/{id}/retry", auth(h.requireAdmin(http.HandlerFunc(h.handleWebhookLogRetry))))

	mux.Handle("GET /ui/settings/bridges", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridges))))
	mux.Handle("GET /ui/settings/bridges/telegram", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeTelegram))))
	mux.Handle("POST /ui/settings/bridges/telegram", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeTelegramSave))))
	mux.Handle("GET /ui/settings/bridges/max", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeMax))))
	mux.Handle("POST /ui/settings/bridges/max", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeMaxSave))))
	mux.Handle("GET /ui/settings/bridges/vk", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeVK))))
	mux.Handle("POST /ui/settings/bridges/vk", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeVKSave))))
	mux.Handle("GET /ui/settings/bridges/rutube", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeRutube))))
	mux.Handle("POST /ui/settings/bridges/rutube", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsBridgeRutubeSave))))
	mux.Handle("GET /ui/settings/telegram", auth(h.requireAdmin(http.HandlerFunc(h.handleSettingsTelegramRedirect))))
	mux.Handle("GET /ui/settings", auth(http.HandlerFunc(h.handleSettings)))
	mux.Handle("POST /ui/settings/password", auth(http.HandlerFunc(h.handleSettingsPassword)))
	mux.Handle("POST /ui/settings/api-keys", auth(http.HandlerFunc(h.handleAPIKeyCreate)))
	mux.Handle("POST /ui/settings/api-keys/{id}/delete", auth(http.HandlerFunc(h.handleAPIKeyDelete)))

	mux.Handle("GET /ui/admin/users", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminUsers))))
	mux.Handle("POST /ui/admin/users", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminUserCreate))))
	mux.Handle("POST /ui/admin/users/{id}/delete", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminUserDelete))))
	mux.Handle("POST /ui/admin/feeds/refresh-all", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminRefreshAll))))
	mux.Handle("POST /ui/admin/feeds/reset-circuits", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedsResetCircuits))))
	mux.Handle("GET /ui/admin/feeds", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedsList))))
	mux.Handle("GET /ui/admin/feeds/{id}", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedShow))))
	mux.Handle("POST /ui/admin/feeds/{id}/refresh", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedRefresh))))
	mux.Handle("POST /ui/admin/feeds/{id}/pause", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedPause))))
	mux.Handle("POST /ui/admin/feeds/{id}/unpause", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedUnpause))))
	mux.Handle("POST /ui/admin/feeds/{id}/reset-circuit", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedResetCircuit))))
	mux.Handle("POST /ui/admin/feeds/{id}/delete", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedDelete))))
	mux.Handle("POST /ui/admin/feeds/{id}/hash-entries", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminFeedHashEntries))))
	mux.Handle("GET /ui/version", auth(http.HandlerFunc(h.handleVersionJSON)))
	mux.Handle("GET /ui/admin/system", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminSystem))))
	mux.Handle("POST /ui/admin/system/hash-entries", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminHashEntries))))
	mux.Handle("POST /ui/admin/system/workers", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminWorkersSave))))
	mux.Handle("POST /ui/admin/system/retention", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminRetentionSave))))
	mux.Handle("POST /ui/admin/system/retention/cleanup", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminRetentionCleanupNow))))
	mux.Handle("POST /ui/admin/system/workers/pause", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminWorkersPause))))
	mux.Handle("POST /ui/admin/system/workers/resume", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminWorkersResume))))
	mux.Handle("POST /ui/admin/system/restart", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminRestart))))
	mux.Handle("POST /ui/admin/system/update", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminUpdate))))
	mux.Handle("GET /ui/admin/system/update/status", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminUpdateStatus))))
	mux.Handle("GET /ui/admin/system/backup.txt", auth(h.requireAdmin(http.HandlerFunc(h.handleAdminBackupHint))))
}

func (h *Handler) staticHandler() http.Handler {
	sub, err := fs.Sub(web.FS, "static")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "static unavailable", http.StatusInternalServerError)
		})
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		files.ServeHTTP(w, r)
	})
}

// cssHexColor is the only colour shape the templates inject into style
// attributes as template.CSS (which bypasses html/template escaping).
var cssHexColor = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// safeCSSColor returns the trimmed colour if it is a hex literal, else "".
// Colours are admin-supplied, but a stored value like
// "red;background:url(https://x/)" must still never reach a style attribute.
func safeCSSColor(v string) string {
	v = strings.TrimSpace(v)
	if cssHexColor.MatchString(v) {
		return v
	}
	return ""
}
