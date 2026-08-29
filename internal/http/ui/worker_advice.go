package ui

import (
	"context"
	"fmt"
	"time"

	"rssam/internal/capacity"
	"rssam/internal/storage"
)

type workerAdviceView struct {
	Action               string
	Tone                 string
	Title                string
	Message              string
	PollWorkers          int
	SuggestedPollWorkers int
	WebhookWorkers       int
	OfferedPerMin        string
	AvgDuration          string
	Overdue              int
	Stale                int
	MaxLag               string
	Utilization          string
	WebhookDue           int
	WebhookHint          string
	ApplyHint            string
}

func (h *Handler) loadWorkerAdvice(ctx context.Context, summary storage.AdminFeedSummary, jobs storage.PollFeedJobCounts) workerAdviceView {
	pollN := h.cfg.WorkerPoolSize
	if pollN <= 0 {
		pollN = 10
	}
	webhookN := h.cfg.WebhookWorkerPoolSize
	if webhookN <= 0 {
		webhookN = pollN
	}
	fetchTimeout := time.Duration(h.cfg.FetchTimeoutSeconds) * time.Second
	if fetchTimeout <= 0 {
		fetchTimeout = 15 * time.Second
	}

	avg, samples := capacity.PollDurationSnapshot()
	offered := 0.0
	webhookDue := 0
	if h.cfg.AdminFeeds != nil {
		if v, err := h.cfg.AdminFeeds.OfferedPollsPerMin(ctx); err == nil {
			offered = v
		}
		if v, err := h.cfg.AdminFeeds.DueWebhookLogCount(ctx); err == nil {
			webhookDue = v
		}
	}

	errFrac := 0.0
	if summary.TotalFeeds > 0 {
		errFrac = float64(summary.ErrorCount) / float64(summary.TotalFeeds)
	}

	advice := capacity.Recommend(capacity.Input{
		Workers:       pollN,
		OfferedPerMin: offered,
		AvgDuration:   avg,
		Samples:       samples,
		FetchTimeout:  fetchTimeout,
		Overdue:       jobs.Overdue,
		MaxLag:        time.Duration(jobs.MaxLagSeconds * float64(time.Second)),
		Waiting:       summary.WaitingCount,
		ErrorFraction: errFrac,
	})

	view := workerAdviceView{
		Action:               advice.Action,
		PollWorkers:          pollN,
		SuggestedPollWorkers: advice.SuggestedWorkers,
		WebhookWorkers:       webhookN,
		OfferedPerMin:        fmt.Sprintf("%.1f", offered),
		AvgDuration:          formatDurationSec(advice.AvgDuration),
		Overdue:              jobs.Overdue,
		Stale:                jobs.Stale,
		MaxLag:               formatDurationSec(time.Duration(jobs.MaxLagSeconds * float64(time.Second))),
		Utilization:          fmt.Sprintf("%.0f%%", advice.Utilization*100),
		WebhookDue:           webhookDue,
		Message:              advice.Message,
		ApplyHint:            "Чтобы применить: задайте WORKER_POOL_SIZE (и при необходимости WEBHOOK_WORKER_POOL_SIZE) в /opt/rssam/.env и выполните systemctl restart rssam. Из UI размер пула не меняется.",
	}

	switch advice.Action {
	case capacity.ActionIncrease, capacity.ActionFixFeeds, capacity.ActionSchedulerLimit:
		view.Tone = "warn"
	case capacity.ActionDecrease:
		view.Tone = "ok"
	default:
		view.Tone = "info"
	}

	switch advice.Action {
	case capacity.ActionIncrease:
		view.Title = "Имеет смысл увеличить WORKER_POOL_SIZE"
	case capacity.ActionDecrease:
		view.Title = "Пул опроса можно уменьшить"
	case capacity.ActionFixFeeds:
		view.Title = "Сначала разберитесь с ошибками лент"
	case capacity.ActionSchedulerLimit:
		view.Title = "Упираетесь в лимит планировщика, не в пул"
	default:
		view.Title = "Пул опроса в норме"
	}

	if jobs.Stale > 0 {
		view.Tone = "warn"
		view.Title = "Зависшие задачи опроса"
		view.Message = fmt.Sprintf("%d задач poll_feed держат lock дольше 2 минут (часто после рестарта). Они не входят в «просрочено», поэтому счётчик может быть 0, пока сотни лент ждут обновления. Диспетчер снимает такие lock автоматически.", jobs.Stale)
	}

	if webhookDue > webhookN*2 {
		view.WebhookHint = fmt.Sprintf("Очередь webhook_logs (%d due) заметно больше пула (%d). Имеет смысл поднять WEBHOOK_WORKER_POOL_SIZE.", webhookDue, webhookN)
	}
	return view
}

func formatDurationSec(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	sec := d.Seconds()
	if sec < 10 {
		return fmt.Sprintf("%.1f с", sec)
	}
	return fmt.Sprintf("%.0f с", sec)
}
