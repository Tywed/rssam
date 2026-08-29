package worker

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"rssam/internal/metrics"
	"rssam/internal/storage"
)

func (r *Runner) dailyFeedResetLoop(ctx context.Context) {
	if !r.Cfg.FeedPollDailyResetEnabled {
		return
	}
	loc := r.feedDailyResetLocation()

	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.maybeRunDailyFeedReset(ctx, loc)
		}
	}
}

func (r *Runner) feedDailyResetLocation() *time.Location {
	tz := strings.TrimSpace(r.Cfg.FeedPollDailyResetTZ)
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		if r.Log != nil {
			r.Log.Warn("invalid FEED_POLL_DAILY_RESET_TZ, using UTC", "tz", tz, "err", err)
		}
		return time.UTC
	}
	return loc
}

func (r *Runner) maybeRunDailyFeedReset(ctx context.Context, loc *time.Location) {
	if r.Store == nil {
		return
	}
	today := time.Now().In(loc).Format("2006-01-02")

	raw, err := r.Store.GetAppSetting(ctx, storage.AppSettingFeedPollDailyResetDate)
	if err == nil && len(raw) > 0 {
		var last string
		if json.Unmarshal(raw, &last) == nil && last == today {
			return
		}
	}

	n, err := r.Store.ResetFeedPollErrorsDaily(ctx)
	if err != nil {
		if r.Log != nil {
			r.Log.Error("daily feed poll reset failed", "err", err)
		}
		return
	}

	b, _ := json.Marshal(today)
	if err := r.Store.SetAppSetting(ctx, storage.AppSettingFeedPollDailyResetDate, b); err != nil {
		if r.Log != nil {
			r.Log.Error("daily feed poll reset: save date failed", "err", err)
		}
	}

	if n > 0 {
		metrics.CleanupDeletedRows.WithLabelValues("feed_poll_reset").Add(float64(n))
		if r.Log != nil {
			r.Log.Info("daily feed poll reset completed", "feeds_reset", n, "date", today, "tz", loc.String())
		}
	}
}
