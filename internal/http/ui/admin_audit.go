package ui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rssam/internal/storage"
)

const adminAuditPageSize = 100

type adminAuditRow struct {
	storage.AuditEntry
	DetailsJSON string
}

type adminAuditView struct {
	Rows      []adminAuditRow
	Actions   []string
	Actor     string
	Action    string
	Page      int
	PageCount int
	Total     int
}

func (v adminAuditView) PageLink(page int) string {
	return auditLink(v.Actor, v.Action, page)
}

// ActorLink keeps the action filter and narrows the list to one actor.
func (v adminAuditView) ActorLink(actor string) string {
	return auditLink(actor, v.Action, 1)
}

func auditLink(actor, action string, page int) string {
	q := url.Values{}
	if actor != "" {
		q.Set("actor", actor)
	}
	if action != "" {
		q.Set("action", action)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/ui/admin/audit"
	}
	return "/ui/admin/audit?" + q.Encode()
}

func (h *Handler) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Audit == nil || h.cfg.Audit.Store == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	view := adminAuditView{
		Actions: storage.AuditActions,
		Actor:   strings.TrimSpace(q.Get("actor")),
		Action:  strings.TrimSpace(q.Get("action")),
		Page:    1,
	}
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 1 {
		view.Page = p
	}
	rows, total, err := h.cfg.Audit.Store.ListAuditLog(r.Context(), storage.AuditListParams{
		Actor:  view.Actor,
		Action: view.Action,
		Limit:  adminAuditPageSize,
		Offset: (view.Page - 1) * adminAuditPageSize,
	})
	if err != nil {
		h.log.Error("list audit log failed", "err", err)
		http.Error(w, "audit log unavailable", http.StatusInternalServerError)
		return
	}
	view.Total = total
	view.PageCount = max((total+adminAuditPageSize-1)/adminAuditPageSize, 1)
	view.Rows = make([]adminAuditRow, 0, len(rows))
	for _, e := range rows {
		row := adminAuditRow{AuditEntry: e}
		if len(e.Details) > 0 {
			if b, err := json.Marshal(e.Details); err == nil {
				row.DetailsJSON = string(b)
			}
		}
		view.Rows = append(view.Rows, row)
	}

	data := h.baseData(r, "settings")
	data.SettingsSection = "audit"
	data.Title = "Журнал действий"
	data.Audit = view
	h.render(w, r, "admin_audit", data)
}
