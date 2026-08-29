package ui

import (
	"net/http"
	"runtime"

	"rssam/internal/auth"
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
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, err = h.cfg.Users.CreateUser(r.Context(), storage.CreateUserParams{
		Username:      username,
		PasswordHash:  hash,
		PlainPassword: password,
		IsAdmin:       isAdmin,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
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
		http.NotFound(w, r)
		return
	}
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
	}
	http.Redirect(w, r, "/ui/admin/users", http.StatusFound)
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
	info := adminSystemInfo{
		Version:       version.Version,
		GoVersion:     runtime.Version(),
		BuildDate:     version.Date,
		Arch:          runtime.GOARCH,
		OS:            runtime.GOOS,
		UsersCount:    count,
		MemAllocBytes: int64(ms.Alloc),
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
	data.Info = info
	data.Title = "Система"
	h.render(w, r, "admin_system", data)
}
