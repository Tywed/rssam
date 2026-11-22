package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"rssam/internal/storage"
)

// AppSettingDailySummaryState keeps the date of the last summary and the
// PostgreSQL counters it saw, so the next one prints per-day deltas.
const AppSettingDailySummaryState = "daily_summary_state"

type dailySummaryState struct {
	Date     string                       `json:"date"`
	At       time.Time                    `json:"at"`
	Counters storage.DailySummaryCounters `json:"counters"`
}

// dailySummaryLoop sends one message a day to the system-alert webhooks:
// the day's polls, errors, items and deliveries by the numbers that
// otherwise need a psql session. It fires at the first tick after local
// midnight (FEED_POLL_DAILY_RESET_TZ), the moment the daily reset uses.
func (r *Runner) dailySummaryLoop(ctx context.Context) {
	if !r.Cfg.DailySummaryEnabled || r.Store == nil {
		return
	}
	loc := r.feedDailyResetLocation()
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if r.Paused() {
				continue
			}
			r.maybeSendDailySummary(ctx, loc)
		}
	}
}

func (r *Runner) maybeSendDailySummary(ctx context.Context, loc *time.Location) {
	now := time.Now()
	today := now.In(loc).Format("2006-01-02")
	var prev dailySummaryState
	if raw, err := r.Store.GetAppSetting(ctx, AppSettingDailySummaryState); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &prev)
	}
	if prev.Date == today {
		return
	}
	hooks, err := r.Store.ListSystemAlertWebhooks(ctx)
	if err != nil {
		r.db.down(r.Log, "daily summary: list webhooks failed", err)
		return
	}
	since := now.Add(-24 * time.Hour)
	sum, err := r.Store.CollectDailySummary(ctx, since, now, r.SystemAlerts.SilentAfter, r.SystemAlerts.DormantAfter)
	if err != nil {
		r.db.down(r.Log, "daily summary: collect failed", err)
		return
	}
	next := dailySummaryState{Date: today, At: now.UTC(), Counters: sum.Counters}
	if b, err := json.Marshal(next); err == nil {
		if err := r.Store.SetAppSetting(ctx, AppSettingDailySummaryState, b); err != nil {
			r.Log.Error("daily summary: save state failed", "err", err)
			return
		}
	}
	// The first run after enabling only records the counters: the first
	// message goes out at the next midnight with a full day behind it.
	if prev.At.IsZero() {
		r.Log.Info("daily summary: baseline recorded, first message at next midnight", "tz", loc.String())
		return
	}
	if len(hooks) == 0 {
		return
	}
	// A state older than two days (the process was down) has nothing
	// comparable to diff against; the message says so.
	var base *storage.DailySummaryCounters
	if now.Sub(prev.At) < 48*time.Hour {
		c := prev.Counters
		base = &c
	}
	m := dailySummaryMessage(sum, base, prev.At, r.startedAt)
	a := &systemAlerter{r: r, store: r.Store, cfg: r.SystemAlerts}
	for _, w := range hooks {
		a.send(ctx, w, m)
	}
	_ = r.Store.RecordAudit(ctx, storage.AuditEvent{ActorName: "worker", Action: storage.AuditSystemAlert, TargetType: m.Kind, Details: m.Details})
	r.Log.Info("daily summary sent", "webhooks", len(hooks), "date", today)
}

// dailySummaryMessage renders the summary. base is yesterday's counters
// (nil = no deltas yet); started is when this process came up.
func dailySummaryMessage(s storage.DailySummary, base *storage.DailySummaryCounters, baseAt time.Time, started time.Time) alertMessage {
	var lines []string
	window := s.Until.Sub(s.Since)
	title := fmt.Sprintf("Сводка за сутки (%s, %s)", s.Until.UTC().Format("2006-01-02"), s.Until.UTC().Format("15:04")+" UTC")

	details := map[string]any{"since": s.Since.UTC(), "until": s.Until.UTC()}

	// Polls: delta of the poll-state counter against the previous summary.
	if base != nil {
		polls := s.Counters.PollStateUpdates - base.PollStateUpdates
		if polls >= 0 && baseAt.After(s.Since.Add(-2*time.Hour)) {
			perMin := float64(polls) / s.Until.Sub(baseAt).Minutes()
			lines = append(lines, fmt.Sprintf("Опросов: %s (%.0f/мин)", groupThousands(polls), perMin))
			details["polls"] = polls
		} else {
			lines = append(lines, "Опросов: н/д (счётчики PostgreSQL сброшены)")
		}
	} else {
		lines = append(lines, "Опросов: н/д (нет вчерашнего снимка счётчиков)")
	}

	// Per type: only rows that say something.
	var errPolls, inserted, errFeeds int
	var typeLines []string
	for _, t := range s.ByType {
		errPolls += t.ErrorPolls
		inserted += t.Inserted
		errFeeds += t.ErrorFeeds
		if t.ErrorPolls == 0 && t.Inserted == 0 {
			continue
		}
		typeLines = append(typeLines, fmt.Sprintf("  %s: записей %s, ошибок %s у %d лент", t.FeedType, groupThousands(int64(t.Inserted)), groupThousands(int64(t.ErrorPolls)), t.ErrorFeeds))
	}
	lines = append(lines, fmt.Sprintf("Новых записей: %s; неудачных опросов: %s у %d лент", groupThousands(int64(inserted)), groupThousands(int64(errPolls)), errFeeds))
	lines = append(lines, typeLines...)
	details["inserted"], details["error_polls"], details["error_feeds"] = inserted, errPolls, errFeeds

	lines = append(lines, fmt.Sprintf("Доставки: отправлено %s, ошибок %d, отброшено %d, в очереди %d",
		groupThousands(int64(s.WebhookSent)), s.WebhookFailed, s.WebhookDead, s.WebhookPending))
	details["webhook_sent"], details["webhook_failed"], details["webhook_dead"], details["webhook_pending"] = s.WebhookSent, s.WebhookFailed, s.WebhookDead, s.WebhookPending

	f := s.Feeds
	lines = append(lines, fmt.Sprintf("Ленты: всего %d, в порядке %d, с ошибкой %d (исчезли %d, заблокированы %d, мёртвые %d), на паузе %d, тихих %d, спящих %d",
		f.TotalFeeds, f.OKCount, f.ErrorCount, f.GoneCount, f.BlockedCount, s.DeadFeeds, f.PausedCount, f.SilentCount, f.DormantCount))
	details["feeds_total"], details["feeds_error"], details["feeds_gone"], details["feeds_blocked"], details["feeds_dead"], details["feeds_paused"], details["feeds_silent"], details["feeds_dormant"] =
		f.TotalFeeds, f.ErrorCount, f.GoneCount, f.BlockedCount, s.DeadFeeds, f.PausedCount, f.SilentCount, f.DormantCount

	db := fmt.Sprintf("База: %s", humanBytes(s.Counters.DBBytes))
	if base != nil {
		if d := s.Counters.DBBytes - base.DBBytes; d > 0 {
			db += fmt.Sprintf(" (+%s за сутки)", humanBytes(d))
		} else if d < 0 {
			db += fmt.Sprintf(" (%s за сутки)", humanBytes(d))
		}
		if w := s.Counters.WALBytes - base.WALBytes; w > 0 && base.WALBytes > 0 {
			db += fmt.Sprintf(", WAL %s/сутки", humanBytes(w))
			details["wal_bytes"] = w
		}
	}
	lines = append(lines, db)
	details["db_bytes"] = s.Counters.DBBytes
	if !started.IsZero() {
		up := s.Until.Sub(started)
		if up < window {
			lines = append(lines, fmt.Sprintf("Процесс перезапускался: работает %s", humanDuration(up)))
			details["restarted"] = true
		} else {
			lines = append(lines, fmt.Sprintf("Без перезапусков: работает %s", humanDuration(up)))
		}
	}

	plain := title + "\n" + strings.Join(lines, "\n")
	var htm strings.Builder
	htm.WriteString("<b>" + html.EscapeString(title) + "</b>")
	for _, l := range lines {
		htm.WriteString("\n" + html.EscapeString(l))
	}
	return alertMessage{Kind: "daily_summary", Plain: plain, HTML: htm.String(), Details: details}
}

func groupThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

func humanBytes(b int64) string {
	sign := ""
	if b < 0 {
		sign, b = "-", -b
	}
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%s%.1f ГБ", sign, float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%s%.0f МБ", sign, float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%s%.0f КБ", sign, float64(b)/(1<<10))
	}
	return fmt.Sprintf("%s%d Б", sign, b)
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d д %d ч", days, h)
	case h > 0:
		return fmt.Sprintf("%d ч %d мин", h, m)
	}
	return fmt.Sprintf("%d мин", m)
}
