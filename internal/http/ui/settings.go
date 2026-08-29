package ui

import (
	"net/http"
	"strings"

	"rssam/internal/auth"
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
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		data.NewToken = token
	}
	h.render(w, r, "settings", data)
}

func (h *Handler) handleSettingsPassword(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	password := r.FormValue("password")
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	http.Redirect(w, r, "/ui/settings", http.StatusFound)
}

func (h *Handler) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	if !h.validateCSRF(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := principal(r)
	name := strings.TrimSpace(r.FormValue("name"))
	raw, hash, err := auth.NewAPIToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_, err = h.cfg.Users.CreateAPIKey(r.Context(), storage.CreateAPIKeyParams{
		UserID:    p.UserID,
		Name:      name,
		TokenHash: hash,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/ui/settings?token="+raw, http.StatusFound)
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
	http.Redirect(w, r, "/ui/settings", http.StatusFound)
}
