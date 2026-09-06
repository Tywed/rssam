package ui

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
	"rssam/internal/version"
)

const (
	docsURL   = "https://github.com/Tywed/rssam/blob/main/README.md"
	githubURL = "https://github.com/Tywed/rssam"
)

type pageData struct {
	Title                      string
	Layout                     bool
	Nav                        string
	BodyHTML                   template.HTML
	Username                   string
	IsAdmin                    bool
	CSRFToken                  string
	SessionID                  string
	FlashMsg                   string
	FlashErr                   string
	UnreadCount                int
	FeedUnreadCounts           map[int64]int
	CategoryUnreadCounts       map[int64]int
	CategoryFeedCounts         map[int64]int
	UncategorizedFeedCount     int
	TotalFeedCount             int
	SidebarLazyFeeds           bool
	SidebarExpandCategoryID    int64
	SidebarExpandUncategorized bool
	FeedsTreeLazy              bool
	ErrorFeedCount             int
	InactiveFeedCount          int
	FeedsFilter                string
	ListFeeds                  []storage.Feed
	ListCategoryFeedCounts     map[int64]int
	ListUncategorizedFeedCount int
	FeedsListPage              int
	FeedsListPageCount         int
	Feeds                      []storage.Feed
	Categories                 []storage.Category
	FeedID                     int64
	CategoryID                 int64
	SearchQuery                string
	Entries                    []storage.Entry
	Total                      int
	Limit                      int
	Offset                     int
	Query                      map[string]string
	EntrySort                  string
	AssetVersion               string
	Entry                      storage.Entry
	EntryContent               template.HTML
	SelectedEntryID            int64
	Feed                       storage.Feed
	Filter                     storage.Filter
	RulesJSON                  string
	Filters                    []storage.Filter
	Matches                    []storage.FilterMatchWithEntry
	Labels                     []storage.Label
	Label                      storage.Label
	LabelID                    int64
	LabelEntryCounts           map[int64]int
	LabelUnreadCounts          map[int64]int
	ScopeFeedIDs               map[int64]bool
	ScopeCategoryIDs           map[int64]bool
	ScopeFeeds                 []storage.Feed
	Webhooks                   []storage.Webhook
	Webhook                    storage.Webhook
	WebhookID                  int64
	Logs                       []storage.WebhookLog
	WebhookLogRows             []storage.WebhookLogRow
	WebhookLogsFilter          string
	WebhookLogsPeriod          string
	WebhookMaxAttempts         int
	AdminWebhookSummary        storage.AdminWebhookSummary
	AdminWebhookStatusCounts   adminWebhookStatusCounts
	AdminWebhookRows           []adminWebhookRowView
	AdminWebhookDetail         adminWebhookRowView
	AdminWebhooksFilter        string
	WebhookBindingFeeds        []storage.WebhookBindingFeed
	WebhookBindingFilters      []storage.WebhookBindingFilter
	WebhookTelegram            storage.TelegramProviderConfig
	WebhookMax                 storage.MaxProviderConfig
	WebhookTelegramTokenSet    bool
	APIKeys                    []storage.APIKey
	NewToken                   string
	Users                      []storage.User
	CurrentUserID              int64
	Info                       adminSystemInfo
	Error                      string
	SettingsSection            string
	BridgeSection              string
	BridgeSettings             BridgeSettings
	BridgeFeedCounts           map[string]int
	TgUseProxy                 bool
	TgProxyServiceURL          string
	TgStaticProxy              string
	AdminFeedsFilter           string
	AdminFeedsSort             string
	AdminFeedsOrder            string
	AdminFeedsPage             int
	AdminFeedsPageCount        int
	AdminFeedsTotal            int
	AdminFeedSummary           storage.AdminFeedSummary
	AdminFeedRows              []adminFeedRowView
	AdminFeedDetail            adminFeedDetailView
	PollFeedJobCounts          storage.PollFeedJobCounts
	WorkerAdvice               workerAdviceView
	DBSizeBytes                int64
	MemAllocBytes              int64
	AppVersion                 string
	GitHubURL                  string
	DocsURL                    string
	VersionLatest              string
	VersionUpdate              bool
	VersionChecked             bool
}

type ctxKey int

const (
	principalKey ctxKey = 1
	sessionKey   ctxKey = 2
)

func (h *Handler) sessionCookieCfg(r *http.Request) auth.SessionCookieConfig {
	return auth.DefaultSessionCookieConfig(auth.RequestIsSecure(r, h.cfg.HSTSEnabled))
}

func (h *Handler) csrfToken(r *http.Request) string {
	sid := auth.SessionIDFromRequest(r)
	if sid == "" {
		return auth.CSRFToken(h.cfg.CSRFSecret, "login")
	}
	return auth.CSRFToken(h.cfg.CSRFSecret, sid)
}

func parseRequestForm(r *http.Request) error {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return r.ParseMultipartForm(32 << 20)
	}
	return r.ParseForm()
}

func (h *Handler) validateCSRF(r *http.Request) bool {
	if err := parseRequestForm(r); err != nil {
		return false
	}
	token := r.FormValue("csrf_token")
	sid := auth.SessionIDFromRequest(r)
	if sid == "" {
		sid = "login"
	}
	return auth.ValidateCSRF(h.cfg.CSRFSecret, sid, token)
}

func (h *Handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, sid, ok := h.authenticate(r)
		if !ok {
			http.Redirect(w, r, "/ui/login?next="+urlQueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		ctx := auth.WithPrincipal(r.Context(), p)
		ctx = context.WithValue(ctx, sessionKey, sid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth.PrincipalFromContext(r.Context())
		if !ok || !p.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) authenticate(r *http.Request) (auth.Principal, string, bool) {
	if h.cfg.Sessions == nil || h.cfg.Users == nil {
		return auth.Principal{}, "", false
	}
	sid := auth.SessionIDFromRequest(r)
	if sid == "" {
		return auth.Principal{}, "", false
	}
	sess, err := h.cfg.Sessions.LookupSession(r.Context(), sid)
	if err != nil {
		return auth.Principal{}, "", false
	}
	u, err := h.cfg.Users.GetUser(r.Context(), sess.UserID)
	if err != nil {
		return auth.Principal{}, "", false
	}
	now := time.Now().UTC()
	if storage.SessionNeedsTouch(sess.ExpiresAt, now) {
		_ = h.cfg.Sessions.TouchSession(r.Context(), sid, now.Add(storage.DefaultSessionTTL))
	}
	return auth.Principal{UserID: u.ID, IsAdmin: u.IsAdmin, Username: u.Username}, sid, true
}

func principal(r *http.Request) (auth.Principal, bool) {
	return auth.PrincipalFromContext(r.Context())
}

func (h *Handler) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authenticate(r); ok {
		http.Redirect(w, r, "/ui/unread", http.StatusFound)
		return
	}
	h.render(w, r, "login", pageData{
		CSRFToken:    h.csrfToken(r),
		AssetVersion: assetVersion(),
	})
}

func (h *Handler) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		h.render(w, r, "login", pageData{Error: "Неверный CSRF-токен", CSRFToken: h.csrfToken(r), AssetVersion: assetVersion()})
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	u, err := h.cfg.Users.GetUserByUsername(r.Context(), username)
	if err != nil {
		u = storage.User{} // unknown user: VerifyLogin still burns one bcrypt round
	}
	// Evaluate VerifyLogin unconditionally (no short-circuit on err) so an
	// unknown username costs the same as a wrong password.
	ok := auth.VerifyLogin(u.PasswordHash, password)
	if err != nil || !ok {
		h.render(w, r, "login", pageData{Error: "Неверные учётные данные", CSRFToken: h.csrfToken(r), AssetVersion: assetVersion()})
		return
	}
	sid, err := auth.NewSessionID()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	expires := time.Now().UTC().Add(storage.DefaultSessionTTL)
	if _, err := h.cfg.Sessions.CreateSession(r.Context(), u.ID, sid, expires); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	auth.SetSessionCookie(w, h.sessionCookieCfg(r), sid)
	next := strings.TrimSpace(r.FormValue("next"))
	if next == "" {
		next = strings.TrimSpace(r.URL.Query().Get("next"))
	}
	next = localUIPath(next, "/ui/unread")
	http.Redirect(w, r, next, http.StatusFound)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if h.validateCSRF(r) {
		sid := auth.SessionIDFromRequest(r)
		if sid != "" {
			_ = h.cfg.Sessions.DeleteSession(r.Context(), sid)
		}
	}
	auth.ClearSessionCookie(w, h.sessionCookieCfg(r))
	http.Redirect(w, r, "/ui/login", http.StatusFound)
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ui/" && r.URL.Path != "/ui" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/ui/unread", http.StatusFound)
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

func assetVersion() string {
	v := strings.TrimSpace(version.Date)
	if v == "" || v == "unknown" {
		v = strings.TrimSpace(version.Commit)
		if v == "" || v == "none" {
			v = strings.TrimSpace(version.Version)
		} else if len(v) > 12 {
			v = v[:12]
		}
	}
	if v == "" {
		v = "dev"
	}
	return strings.NewReplacer(":", "", "T", "-").Replace(v)
}

func (h *Handler) baseData(r *http.Request, nav string) pageData {
	p, _ := principal(r)
	data := pageData{
		Layout:       true,
		Nav:          nav,
		CSRFToken:    h.csrfToken(r),
		IsAdmin:      p.IsAdmin,
		EntrySort:    parseEntrySort(r),
		AssetVersion: assetVersion(),
		AppVersion:   version.Version,
		GitHubURL:    githubURL,
		DocsURL:      docsURL,
	}
	if h.releases != nil {
		st := h.releases.Status()
		data.VersionLatest = st.Latest
		data.VersionUpdate = st.UpdateAvail
		data.VersionChecked = st.CheckedOK
	}
	data.Username = p.Username
	if data.Username == "" && h.cfg.Users != nil && p.UserID > 0 {
		// Principal built without a username (tests, custom middleware).
		if u, err := h.cfg.Users.GetUser(r.Context(), p.UserID); err == nil {
			data.Username = u.Username
		}
	}
	if h.cfg.Feeds != nil {
		var feeds []storage.Feed
		var total int
		if counts, err := h.cfg.Feeds.FeedCountsByCategory(r.Context(), p.UserID); err == nil {
			data.CategoryFeedCounts = counts.ByCategory
			data.UncategorizedFeedCount = counts.Uncategorized
			data.TotalFeedCount = counts.Total
		}
		if data.TotalFeedCount > sidebarLazyFeedThreshold {
			data.SidebarLazyFeeds = true
		} else {
			feeds, total, _ = h.cfg.Feeds.ListFeeds(r.Context(), p.UserID, storage.NoLimit, 0)
			data.Feeds = feeds
			if data.TotalFeedCount == 0 {
				data.TotalFeedCount = total
				data.CategoryFeedCounts, data.UncategorizedFeedCount = categoryFeedCounts(feeds)
			}
		}
		if errCount, inactiveCount, err := h.cfg.Feeds.CountFeedStatuses(r.Context(), p.UserID); err == nil {
			data.ErrorFeedCount = errCount
			data.InactiveFeedCount = inactiveCount
		} else if len(feeds) > 0 {
			data.ErrorFeedCount, data.InactiveFeedCount = countFeedStatuses(feeds)
		}
	}
	if h.cfg.Categories != nil {
		cats, _, _ := h.cfg.Categories.ListCategories(r.Context(), p.UserID, storage.NoLimit, 0)
		data.Categories = cats
	}
	if h.cfg.Entries != nil {
		if feedCounts, catCounts, total, err := h.unreadCounts(r.Context(), p.UserID); err == nil {
			data.UnreadCount = total
			data.FeedUnreadCounts = feedCounts
			data.CategoryUnreadCounts = catCounts
		}
	}
	if h.cfg.Labels != nil {
		labels, _, _ := h.cfg.Labels.ListLabels(r.Context(), p.UserID, 500, 0)
		data.Labels = labels
		if counts, err := h.cfg.Labels.UnreadCountsByLabel(r.Context(), p.UserID); err == nil {
			data.LabelUnreadCounts = counts
		}
		if counts, err := h.cfg.Labels.EntryCountsByLabel(r.Context(), p.UserID); err == nil {
			data.LabelEntryCounts = counts
		}
	}
	return data
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, page string, data pageData) {
	var body bytes.Buffer
	if err := h.templates.ExecuteTemplate(&body, "body_"+page, data); err != nil {
		h.log.Error("body template render failed", "page", page, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	data.BodyHTML = template.HTML(body.String())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "layout", data); err != nil {
		if !errors.Is(err, context.Canceled) {
			h.log.Error("template render failed", "page", page, "err", err)
		}
		if w.Header().Get("Content-Type") == "" {
			http.Error(w, "render error", http.StatusInternalServerError)
		}
	}
}

func (h *Handler) renderPartial(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		h.log.Error("partial render failed", "template", name, "err", err)
	}
}

func parsePage(r *http.Request) (limit, offset int) {
	limit = defaultPageLimit
	offset = 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconvAtoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconvAtoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func strconvAtoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func parsePathID(r *http.Request, keys ...string) (int64, error) {
	key := "id"
	if len(keys) > 0 {
		key = keys[0]
	}
	raw := strings.TrimSpace(r.PathValue(key))
	if raw == "" {
		return 0, errors.New("id required")
	}
	var id int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid id")
		}
		id = id*10 + int64(c-'0')
	}
	if id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func isHX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}
