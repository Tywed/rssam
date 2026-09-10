package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"

	"rssam/internal/auth"
	"rssam/internal/envfile"
	"rssam/internal/ops"
	"rssam/internal/storage"
	"rssam/internal/version"
)

func (h *Handler) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "users"
	users, _, err := h.cfg.Users.ListUsers(r.Context(), 1000, 0)
	if err != nil {
		http.Error(w, "list users failed", http.StatusInternalServerError)
		return
	}
	data.Users = users
	data.CurrentUserID = p.UserID
	data.Title = "Пользователи"
	h.render(w, r, "admin_users", data)
}

func (h *Handler) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	username := stringsTrim(r.FormValue("username"))
	password := r.FormValue("password")
	isAdmin := r.FormValue("is_admin") == "1"
	if err := auth.ValidateNewPassword(password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	u, err := h.cfg.Users.CreateUser(r.Context(), storage.CreateUserParams{
		Username:      username,
		PasswordHash:  hash,
		PlainPassword: password,
		IsAdmin:       isAdmin,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditUserCreate, "user", u.ID, map[string]any{"username": username, "is_admin": isAdmin})
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

func (h *Handler) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	id, err := parsePathID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if id == p.UserID {
		http.Error(w, "cannot delete self", http.StatusBadRequest)
		return
	}
	if err := h.cfg.Users.DeleteUser(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, storage.ErrLastAdmin):
			http.Error(w, "нельзя удалить последнего администратора", http.StatusConflict)
		default:
			http.Error(w, "delete user failed", http.StatusInternalServerError)
		}
		return
	}
	h.cfg.Audit.Record(r, storage.AuditUserDelete, "user", id, nil)
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

func (h *Handler) handleAdminRefreshAll(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	if h.cfg.RefreshAllFeeds != nil {
		_ = h.cfg.RefreshAllFeeds(r, p.UserID)
		h.cfg.Audit.Record(r, storage.AuditFeedsRefreshAll, "", 0, nil)
	}
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
}

type adminSystemInfo struct {
	Version       string
	Commit        string
	GoVersion     string
	BuildDate     string
	Arch          string
	OS            string
	UsersCount    int
	MemAllocBytes int64
	DBSizeBytes   int64
	TotalEntries  int
	TotalUnread   int
	AuditRows     int
	BinaryPath    string
	EnvFile       string
	Systemd       string
	DatabaseHost  string
	InDocker      bool
	DualWarning   string
	AuthTokenSet  bool
	CanRestart    bool
	RestartHint   string
	CanUpdate     bool
	UpdateHint    string
	Latest        string
	UpdateAvail   bool
	ReleaseNotes  string
	ReleaseURL    string
	WorkersPaused bool
}

func (h *Handler) handleAdminSystem(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "system"
	count := 0
	if h.cfg.Users != nil {
		count, _ = h.cfg.Users.CountUsers(r.Context())
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	canR, rHint := ops.CanRestart()
	canU, uHint := ops.CanUpdate()
	info := adminSystemInfo{
		Version:       version.Version,
		Commit:        version.Commit,
		GoVersion:     runtime.Version(),
		BuildDate:     version.Date,
		Arch:          runtime.GOARCH,
		OS:            runtime.GOOS,
		UsersCount:    count,
		MemAllocBytes: int64(ms.Alloc),
		BinaryPath:    ops.BinaryPath(),
		EnvFile:       h.envPath(),
		Systemd:       ops.SystemdActive(),
		DatabaseHost:  ops.RedactDatabaseURL(h.cfg.DatabaseURL),
		InDocker:      ops.InDocker(),
		DualWarning:   ops.DualProcessWarning(),
		AuthTokenSet:  h.cfg.AuthTokenSet,
		CanRestart:    canR,
		RestartHint:   rHint,
		CanUpdate:     canU,
		UpdateHint:    uHint,
		WorkersPaused: h.cfg.WorkersPaused != nil && h.cfg.WorkersPaused(),
	}
	if h.releases != nil {
		_, _ = h.releases.Refresh()
		st := h.releases.Status()
		info.Latest = st.Latest
		info.UpdateAvail = st.UpdateAvail
		info.ReleaseNotes = st.Notes
		info.ReleaseURL = st.HTMLURL
	}
	if h.cfg.AdminFeeds != nil {
		if size, err := h.cfg.AdminFeeds.EstimateDatabaseSize(r.Context()); err == nil {
			info.DBSizeBytes = size
		}
		summary, err := h.cfg.AdminFeeds.AdminFeedSummary(r.Context())
		if err == nil {
			info.TotalEntries = summary.TotalEntries
			info.TotalUnread = summary.TotalUnread
			jobs, jerr := h.cfg.AdminFeeds.PollFeedJobCounts(r.Context())
			if jerr != nil {
				jobs = storage.PollFeedJobCounts{}
			}
			data.WorkerAdvice = h.loadWorkerAdvice(r.Context(), summary, jobs)
		}
	}
	if h.cfg.Audit != nil && h.cfg.Audit.Store != nil {
		info.AuditRows, _ = h.cfg.Audit.Store.CountAuditLog(r.Context())
	}
	data.Info = info
	data.Title = "Система"
	data.Retention = h.cfg.Retention
	data.RetentionCleanupAvailable = h.cfg.RunRetentionCleanup != nil
	if n := strings.TrimSpace(r.URL.Query().Get("hashed")); n != "" {
		data.FlashMsg = "Свёрнуто в хеш: " + n + " записей (избранные сохранены)"
	}
	if n := strings.TrimSpace(r.URL.Query().Get("cleaned")); n != "" {
		data.FlashMsg = "Очистка выполнена: удалено строк — " + n
	}
	if r.URL.Query().Get("saved") == "1" {
		data.FlashMsg = "Сохранено в .env. Чтобы применить — перезапустите сервис."
	}
	h.render(w, r, "admin_system", data)
}

func (h *Handler) handleAdminHashEntries(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	n, err := h.collapseEntries(r, p.UserID, nil, nil, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditEntriesCollapse, "", 0, map[string]any{"collapsed": n})
	http.Redirect(w, r, "/ui/admin/system?hashed="+fmt.Sprint(n), http.StatusFound)
}

func (h *Handler) envPath() string {
	if strings.TrimSpace(h.cfg.EnvFilePath) != "" {
		return h.cfg.EnvFilePath
	}
	return ops.EnvFilePath()
}

func (h *Handler) handleAdminWorkersSave(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	poll, err1 := strconv.Atoi(strings.TrimSpace(r.FormValue("worker_pool_size")))
	hook, err2 := strconv.Atoi(strings.TrimSpace(r.FormValue("webhook_worker_pool_size")))
	if err1 != nil || err2 != nil || poll < 1 || poll > 1000 || hook < 1 || hook > 1000 {
		http.Error(w, "WORKER_POOL_SIZE and WEBHOOK_WORKER_POOL_SIZE must be 1–1000", http.StatusBadRequest)
		return
	}
	path := h.envPath()
	keys := map[string]string{
		"WORKER_POOL_SIZE":         strconv.Itoa(poll),
		"WEBHOOK_WORKER_POOL_SIZE": strconv.Itoa(hook),
	}
	if err := envfile.SetKeys(path, keys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditEnvUpdate, "", 0, map[string]any{"keys": keys})
	http.Redirect(w, r, "/ui/admin/system?saved=1", http.StatusFound)
}

func (h *Handler) handleAdminWorkersPause(w http.ResponseWriter, r *http.Request) {
	h.setWorkersPaused(w, r, true)
}

func (h *Handler) handleAdminWorkersResume(w http.ResponseWriter, r *http.Request) {
	h.setWorkersPaused(w, r, false)
}

func (h *Handler) setWorkersPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	if !h.validateCSRF(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "forbidden"})
		return
	}
	if paused {
		if h.cfg.PauseWorkers == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "управление воркерами недоступно"})
			return
		}
		h.cfg.PauseWorkers()
		h.cfg.Audit.Record(r, storage.AuditWorkersPause, "", 0, nil)
	} else {
		if h.cfg.ResumeWorkers == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "управление воркерами недоступно"})
			return
		}
		h.cfg.ResumeWorkers()
		h.cfg.Audit.Record(r, storage.AuditWorkersResume, "", 0, nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "paused": paused})
}

func (h *Handler) handleAdminRestart(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "forbidden"})
		return
	}
	if err := ops.Restart(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	h.cfg.Audit.Record(r, storage.AuditServiceRestart, "", 0, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "restart queued"})
}

func (h *Handler) handleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "forbidden"})
		return
	}
	ver := strings.TrimSpace(r.FormValue("version"))
	if ver == "" && strings.Contains(r.Header.Get("Content-Type"), "json") {
		var body struct {
			Version string `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ver = strings.TrimSpace(body.Version)
	}
	if ver == "" && h.releases != nil {
		if rel, err := h.releases.Refresh(); err == nil {
			ver = strings.TrimSpace(rel.Tag)
		}
	}
	if ver == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "неизвестна версия для установки"})
		return
	}
	if err := ops.StartUpdate(ver); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	h.cfg.Audit.Record(r, storage.AuditServiceUpdate, "", 0, map[string]any{"version": ver})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "update started", "version": ver})
}

func (h *Handler) handleAdminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	running := ops.UpdateRunning()
	logText := ops.ReadUpdateLog()
	done, ok, errMsg := ops.ParseUpdateLog(logText, running)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"running": running,
		"log":     logText,
		"done":    done,
		"success": ok,
		"error":   errMsg,
	})
}

func (h *Handler) handleVersionJSON(w http.ResponseWriter, r *http.Request) {
	st := struct {
		Current     string `json:"current"`
		Latest      string `json:"latest"`
		UpdateAvail bool   `json:"update_available"`
		Checked     bool   `json:"checked"`
		URL         string `json:"url"`
	}{Current: version.Version}
	if h.releases != nil {
		s := h.releases.Status()
		st.Latest = s.Latest
		st.UpdateAvail = s.UpdateAvail
		st.Checked = s.CheckedOK
		st.URL = s.HTMLURL
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Handler) handleAdminBackupHint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "pg_dump --no-owner --no-acl $DATABASE_URL > rssam-$(date +%%Y%%m%%d).sql\n")
	fmt.Fprintf(w, "# Секреты из %s в дамп не входят. Бэкапьте .env отдельно на диск, не через браузер.\n", h.envPath())
	if _, err := os.Stat(ops.UpdateLogPath()); err == nil {
		fmt.Fprintf(w, "# update log: %s\n", ops.UpdateLogPath())
	}
}
