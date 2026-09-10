package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "profile"
	keys, err := h.cfg.Users.ListAPIKeys(r.Context(), p.UserID)
	if err != nil {
		http.Error(w, "list api keys failed", http.StatusInternalServerError)
		return
	}
	data.APIKeys = keys
	data.Title = "Настройки"
	data.SessionMaxAge = formatSessionAge(h.sessionTTL())
	if c, err := r.Cookie(newTokenCookie); err == nil && strings.TrimSpace(c.Value) != "" {
		data.NewToken = strings.TrimSpace(c.Value)
		http.SetCookie(w, &http.Cookie{Name: newTokenCookie, Value: "", Path: "/ui/settings", MaxAge: -1, HttpOnly: true})
	}
	switch r.URL.Query().Get("pw") {
	case "changed":
		data.FlashMsg = "Пароль изменён. Остальные сессии завершены."
	case "sessions":
		data.FlashMsg = "Остальные сессии завершены."
	}
	h.render(w, r, "settings", data)
}

func (h *Handler) handleSettingsPassword(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	current := r.FormValue("current_password")
	password := r.FormValue("password")

	u, err := h.cfg.Users.GetUser(r.Context(), p.UserID)
	if err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	// A user who already has a password must prove they know it; this stops a
	// hijacked session (or an unattended browser) from locking the owner out.
	if u.PasswordHash != "" && !auth.CheckPassword(u.PasswordHash, current) {
		h.renderSettingsError(w, r, "Текущий пароль неверный")
		return
	}
	if err := auth.ValidateNewPassword(password); err != nil {
		h.renderSettingsError(w, r, "Пароль должен быть от 8 символов до 72 байт")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.renderSettingsError(w, r, err.Error())
		return
	}
	_, err = h.cfg.Users.UpdateUser(r.Context(), storage.UpdateUserParams{
		ID:            p.UserID,
		PasswordHash:  &hash,
		PlainPassword: password,
	})
	if err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	// Revoke every other session: a password change must kick out whoever else
	// holds a cookie for this account.
	if h.cfg.Sessions != nil {
		if err := h.cfg.Sessions.DeleteUserSessionsExcept(r.Context(), p.UserID, auth.SessionIDFromRequest(r)); err != nil {
			h.cfg.Logger.Warn("revoke other sessions after password change failed", "user_id", p.UserID, "err", err)
		}
	}
	h.cfg.Audit.Record(r, storage.AuditPasswordChange, "user", p.UserID, nil)
	http.Redirect(w, r, "/ui/settings?pw=changed", http.StatusFound)
}

// handleSettingsLogoutOthers revokes every other session of the current
// user; the current one stays so the page can confirm the action.
func (h *Handler) handleSettingsLogoutOthers(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	if err := h.cfg.Sessions.DeleteUserSessionsExcept(r.Context(), p.UserID, auth.SessionIDFromRequest(r)); err != nil {
		http.Error(w, "revoke failed", http.StatusInternalServerError)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditLogoutOthers, "user", p.UserID, nil)
	http.Redirect(w, r, "/ui/settings?pw=sessions", http.StatusFound)
}

func (h *Handler) renderSettingsError(w http.ResponseWriter, r *http.Request, msg string) {
	p, _ := principal(r)
	data := h.baseData(r, "settings")
	data.SettingsSection = "profile"
	data.Title = "Настройки"
	data.FlashErr = msg
	if keys, err := h.cfg.Users.ListAPIKeys(r.Context(), p.UserID); err == nil {
		data.APIKeys = keys
	}
	w.WriteHeader(http.StatusBadRequest)
	h.render(w, r, "settings", data)
}

// newTokenCookie carries a freshly created API token from the POST to the
// following GET exactly once. A redirect URL would leak it into browser
// history, proxy access logs and Referer headers.
const newTokenCookie = "rssam_new_token"

func (h *Handler) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	name := strings.TrimSpace(r.FormValue("name"))
	scope := r.FormValue("scope")
	if scope == "" {
		scope = auth.ScopeAdmin
	}
	if !auth.IsValidScope(scope) {
		http.Error(w, "invalid scope", http.StatusBadRequest)
		return
	}
	var expiresAt *time.Time
	if v := r.FormValue("expires_in_days"); v != "" && v != "0" {
		days, err := strconvAtoi(v)
		if err != nil || days > 3650 {
			http.Error(w, "invalid expiry", http.StatusBadRequest)
			return
		}
		t := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		expiresAt = &t
	}
	raw, hash, err := auth.NewAPIToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	k, err := h.cfg.Users.CreateAPIKey(r.Context(), storage.CreateAPIKeyParams{
		UserID:    p.UserID,
		Name:      name,
		TokenHash: hash,
		Scope:     scope,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditAPIKeyCreate, "api_key", k.ID, map[string]any{"name": name, "scope": scope, "expires_at": expiresAt})
	http.SetCookie(w, &http.Cookie{
		Name:     newTokenCookie,
		Value:    raw,
		Path:     "/ui/settings",
		HttpOnly: true,
		Secure:   middleware.RequestIsSecure(r, h.cfg.HSTSEnabled),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   60,
	})
	http.Redirect(w, r, "/ui/settings", http.StatusFound)
}

func (h *Handler) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := h.cfg.Users.DeleteAPIKey(r.Context(), p.UserID, id); err != nil {
		http.NotFound(w, r)
		return
	}
	h.cfg.Audit.Record(r, storage.AuditAPIKeyDelete, "api_key", id, nil)
	http.Redirect(w, r, "/ui/settings", http.StatusFound)
}

func formatSessionAge(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d дн", d/(24*time.Hour))
	}
	return formatDuration(d)
}
