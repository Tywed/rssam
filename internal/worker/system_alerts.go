package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rssam/internal/storage"
)

// systemAlertsTick bounds alert latency; every pass is three cheap reads of
// feeds/webhooks and a write only when something changed.
const systemAlertsTick = time.Minute

// AppSettingSystemAlertState persists the last alerted state, so a restart
// does not re-announce problems already reported.
const AppSettingSystemAlertState = "system_alert_state"

// SystemAlertsConfig is what the alerter needs beyond the runner config.
type SystemAlertsConfig struct {
	// SilentAfter mirrors FEED_SILENT_DAYS (0 = no silent-feed alert).
	SilentAfter time.Duration
	// BackupDir is checked for a fresh dump ("" = no backup alert);
	// BackupMaxAge is how old the newest dump may be before an alert.
	BackupDir    string
	BackupMaxAge time.Duration
	// LatestRelease returns the newest release tag ("" = unknown / disabled).
	LatestRelease func() string
	// CurrentVersion is compared to LatestRelease.
	CurrentVersion string
	// Compare orders two version strings (version.CompareSemver).
	Compare func(a, b string) int
}

type systemAlertState struct {
	Paused   map[int64]bool `json:"paused,omitempty"`
	Silent   map[int64]bool `json:"silent,omitempty"`
	Webhooks map[int64]bool `json:"webhooks,omitempty"`
	Release  string         `json:"release,omitempty"`
	Backup   bool           `json:"backup_stale,omitempty"`
}

type systemAlertsStore interface {
	storage.SystemAlertStore
	GetAppSetting(ctx context.Context, key string) (json.RawMessage, error)
	SetAppSetting(ctx context.Context, key string, value json.RawMessage) error
}

func (r *Runner) systemAlertsLoop(ctx context.Context) {
	if r.Store == nil {
		return
	}
	a := &systemAlerter{r: r, store: r.Store, cfg: r.SystemAlerts}
	a.load(ctx)
	t := time.NewTicker(systemAlertsTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if r.Paused() {
				continue
			}
			a.pass(ctx)
		}
	}
}

type systemAlerter struct {
	r     *Runner
	store systemAlertsStore
	cfg   SystemAlertsConfig
	state systemAlertState
	// seeded: the first pass after a start with no saved state only records
	// what is already broken instead of alerting about all of it.
	seeded bool
}

func (a *systemAlerter) load(ctx context.Context) {
	raw, err := a.store.GetAppSetting(ctx, AppSettingSystemAlertState)
	if err == nil && len(raw) > 0 && json.Unmarshal(raw, &a.state) == nil {
		a.seeded = true
	}
}

func (a *systemAlerter) save(ctx context.Context) {
	b, err := json.Marshal(a.state)
	if err != nil {
		return
	}
	if err := a.store.SetAppSetting(ctx, AppSettingSystemAlertState, b); err != nil {
		a.r.Log.Error("system alerts: save state failed", "err", err)
	}
}

// alertMessage is one notification in plain and HTML form.
type alertMessage struct {
	Kind  string
	Plain string
	HTML  string
	// Details go to the audit row and the HTTP payload.
	Details map[string]any
}

// pass compares the current problem sets with the last alerted state and
// sends one message per new problem group.
func (a *systemAlerter) pass(ctx context.Context) {
	hooks, err := a.store.ListSystemAlertWebhooks(ctx)
	if err != nil {
		a.r.Log.Error("system alerts: list webhooks failed", "err", err)
		return
	}
	if len(hooks) == 0 {
		// Nothing subscribed: forget the state so a later subscriber starts
		// from a clean baseline rather than from stale sets.
		if a.seeded {
			a.state = systemAlertState{}
			a.seeded = false
			a.save(ctx)
		}
		return
	}
	msgs, next, err := a.diff(ctx)
	if err != nil {
		a.r.Log.Error("system alerts: collect failed", "err", err)
		return
	}
	changed := !a.seeded || len(msgs) > 0 || stateChanged(a.state, next)
	if !a.seeded {
		msgs = nil
	}
	a.state = next
	a.seeded = true
	for _, m := range msgs {
		for _, w := range hooks {
			a.send(ctx, w, m)
		}
		_ = a.store.RecordAudit(ctx, storage.AuditEvent{
			ActorName:  "worker",
			Action:     storage.AuditSystemAlert,
			TargetType: m.Kind,
			Details:    m.Details,
		})
	}
	if changed {
		a.save(ctx)
	}
}

func stateChanged(old, cur systemAlertState) bool {
	return !sameSet(old.Paused, cur.Paused) || !sameSet(old.Silent, cur.Silent) || !sameSet(old.Webhooks, cur.Webhooks) ||
		old.Release != cur.Release || old.Backup != cur.Backup
}

func sameSet(a, b map[int64]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func (a *systemAlerter) diff(ctx context.Context) ([]alertMessage, systemAlertState, error) {
	next := systemAlertState{Release: a.state.Release}
	var msgs []alertMessage

	paused, err := a.store.ListPausedFeeds(ctx)
	if err != nil {
		return nil, next, err
	}
	next.Paused = map[int64]bool{}
	var newPaused []storage.SystemAlertFeed
	for _, f := range paused {
		next.Paused[f.ID] = true
		if !a.state.Paused[f.ID] {
			newPaused = append(newPaused, f)
		}
	}
	if len(newPaused) > 0 {
		msgs = append(msgs, feedsAlert("feeds_paused", "Ленты остановлены (ошибки)", newPaused, true))
	}

	if a.cfg.SilentAfter > 0 {
		silent, err := a.store.ListSilentFeeds(ctx, a.cfg.SilentAfter)
		if err != nil {
			return nil, next, err
		}
		next.Silent = map[int64]bool{}
		var newSilent []storage.SystemAlertFeed
		for _, f := range silent {
			next.Silent[f.ID] = true
			if !a.state.Silent[f.ID] {
				newSilent = append(newSilent, f)
			}
		}
		if len(newSilent) > 0 {
			days := int(a.cfg.SilentAfter.Hours() / 24)
			msgs = append(msgs, feedsAlert("feeds_silent", fmt.Sprintf("Ленты молчат дольше %d дн.", days), newSilent, false))
		}
	}

	failing, err := a.store.ListFailingWebhooks(ctx)
	if err != nil {
		return nil, next, err
	}
	next.Webhooks = map[int64]bool{}
	var newFailing []storage.SystemAlertWebhook
	for _, w := range failing {
		next.Webhooks[w.ID] = true
		if !a.state.Webhooks[w.ID] {
			newFailing = append(newFailing, w)
		}
	}
	if len(newFailing) > 0 {
		msgs = append(msgs, webhooksAlert(newFailing))
	}

	if a.cfg.LatestRelease != nil && a.cfg.Compare != nil && a.cfg.CurrentVersion != "" && a.cfg.CurrentVersion != "dev" {
		if latest := a.cfg.LatestRelease(); latest != "" && latest != a.state.Release && a.cfg.Compare(a.cfg.CurrentVersion, latest) < 0 {
			next.Release = latest
			msgs = append(msgs, alertMessage{
				Kind:    "update",
				Plain:   fmt.Sprintf("Доступно обновление rssam %s (установлено %s)", latest, a.cfg.CurrentVersion),
				HTML:    fmt.Sprintf("<b>Доступно обновление rssam %s</b>\n(установлено %s)", html.EscapeString(latest), html.EscapeString(a.cfg.CurrentVersion)),
				Details: map[string]any{"latest": latest, "current": a.cfg.CurrentVersion},
			})
		}
	}

	if a.cfg.BackupDir != "" && a.cfg.BackupMaxAge > 0 {
		age, ok := newestBackupAge(a.cfg.BackupDir)
		next.Backup = ok && age > a.cfg.BackupMaxAge
		if next.Backup && !a.state.Backup {
			msgs = append(msgs, alertMessage{
				Kind:    "backup_stale",
				Plain:   fmt.Sprintf("Бэкап устарел: последний дамп в %s сделан %d ч назад", a.cfg.BackupDir, int(age.Hours())),
				HTML:    fmt.Sprintf("<b>Бэкап устарел</b>\nпоследний дамп в %s сделан %d ч назад", html.EscapeString(a.cfg.BackupDir), int(age.Hours())),
				Details: map[string]any{"dir": a.cfg.BackupDir, "age_hours": int(age.Hours())},
			})
		}
	}
	return msgs, next, nil
}

// newestBackupAge returns the age of the newest rssam-*.dump in dir; ok is
// false when the directory cannot be read (no alert: nothing to judge) and
// true with a huge age when it exists but holds no dump.
func newestBackupAge(dir string) (time.Duration, bool) {
	matches, err := filepath.Glob(filepath.Join(dir, "rssam-*.dump"))
	if err != nil {
		return 0, false
	}
	if _, err := os.Stat(dir); err != nil {
		return 0, false
	}
	var newest time.Time
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	if newest.IsZero() {
		return 100 * 365 * 24 * time.Hour, true
	}
	return time.Since(newest), true
}

const alertListMax = 20

func feedsAlert(kind, title string, feeds []storage.SystemAlertFeed, withError bool) alertMessage {
	sort.Slice(feeds, func(i, j int) bool { return feeds[i].ID < feeds[j].ID })
	var plain, htm strings.Builder
	plain.WriteString(title)
	htm.WriteString("<b>" + html.EscapeString(title) + "</b>")
	ids := make([]int64, 0, len(feeds))
	for i, f := range feeds {
		ids = append(ids, f.ID)
		if i >= alertListMax {
			continue
		}
		line := fmt.Sprintf("#%d %s", f.ID, strings.TrimSpace(f.Title))
		if withError && f.Error != "" {
			line += " — " + truncateRunes(f.Error, 120)
		}
		plain.WriteString("\n• " + line)
		htm.WriteString("\n• " + html.EscapeString(line))
	}
	if len(feeds) > alertListMax {
		more := fmt.Sprintf("\n… ещё %d", len(feeds)-alertListMax)
		plain.WriteString(more)
		htm.WriteString(more)
	}
	return alertMessage{Kind: kind, Plain: plain.String(), HTML: htm.String(), Details: map[string]any{"feed_ids": ids}}
}

func webhooksAlert(hooks []storage.SystemAlertWebhook) alertMessage {
	var plain, htm strings.Builder
	plain.WriteString("Вебхуки не доставляются")
	htm.WriteString("<b>Вебхуки не доставляются</b>")
	ids := make([]int64, 0, len(hooks))
	for _, w := range hooks {
		ids = append(ids, w.ID)
		line := fmt.Sprintf("#%d %s", w.ID, strings.TrimSpace(w.Name))
		if w.LastError != "" {
			line += " — " + truncateRunes(w.LastError, 120)
		}
		plain.WriteString("\n• " + line)
		htm.WriteString("\n• " + html.EscapeString(line))
	}
	return alertMessage{Kind: "webhooks_failing", Plain: plain.String(), HTML: htm.String(), Details: map[string]any{"webhook_ids": ids}}
}

func truncateRunes(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}

// send delivers one alert through one webhook directly (no webhook_logs row:
// there is no entry behind it). Failures are logged only.
func (a *systemAlerter) send(ctx context.Context, w storage.Webhook, m alertMessage) {
	payload, err := json.Marshal(map[string]any{
		"event_version": 1,
		"event_type":    "system_alert",
		"alert":         m.Kind,
		"text":          m.Plain,
		"details":       m.Details,
		"sent_at":       time.Now().UTC(),
	})
	if err != nil {
		return
	}
	out, err := storage.BuildWebhookTextOutbound(w, m.Plain, m.HTML, payload)
	if err != nil {
		a.r.Log.Warn("system alert: build failed", "webhook_id", w.ID, "err", err)
		return
	}
	if a.r.SSRFGuard != nil {
		if err := a.r.SSRFGuard.ValidateURL(out.URL); err != nil {
			a.r.Log.Warn("system alert: url rejected", "webhook_id", w.ID, "err", err)
			return
		}
	}
	timeout := a.r.Cfg.WebhookTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, out.Method, out.URL, bytes.NewReader(out.Body))
	if err != nil {
		return
	}
	if out.Kind == storage.WebhookKindHTTP {
		setWebhookHeaders(req, w.Headers)
		if strings.TrimSpace(w.Secret) != "" {
			req.Header.Set("X-RSSAM-Signature", "sha256="+signWebhookPayload(w.Secret, out.Body))
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.r.httpClient().Do(req)
	if err != nil {
		a.r.Log.Warn("system alert: send failed", "webhook_id", w.ID, "alert", m.Kind, "err", err)
		return
	}
	defer resp.Body.Close()
	snippet := readSnippet(resp.Body, webhookResponseSnippetMaxBytes)
	if ok, msg := storage.ProviderDeliveryOK(out.Kind, resp.StatusCode, snippet); !ok {
		a.r.Log.Warn("system alert: rejected", "webhook_id", w.ID, "alert", m.Kind, "status", resp.StatusCode, "err", msg)
	}
}
