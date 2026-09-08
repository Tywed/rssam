package service

import (
	"context"
	"strings"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

func (r *FeedRefresher) applyFilterActions(
	ctx context.Context,
	userID int64,
	entry storage.Entry,
	filters []storage.Filter,
	matchedFilterIDs map[int64]struct{},
) {
	if len(matchedFilterIDs) == 0 {
		return
	}
	filterByID := make(map[int64]storage.Filter, len(filters))
	for _, f := range filters {
		filterByID[f.ID] = f
	}
	for filterID := range matchedFilterIDs {
		f, ok := filterByID[filterID]
		if !ok || len(f.Actions) == 0 {
			continue
		}
		for _, action := range f.Actions {
			r.applyFilterAction(ctx, userID, entry, action)
		}
	}
}

func (r *FeedRefresher) applyFilterAction(ctx context.Context, userID int64, entry storage.Entry, action storage.FilterAction) {
	switch strings.ToLower(strings.TrimSpace(action.ActionType)) {
	case storage.FilterActionLabel:
		if r.Labels == nil {
			return
		}
		labelID, ok := filter.ParseActionParamID(action.ActionParam)
		if !ok {
			return
		}
		if _, err := r.Labels.GetLabel(ctx, userID, labelID); err != nil {
			return
		}
		_ = r.Labels.AssignEntryLabel(ctx, entry.ID, labelID)
	case storage.FilterActionWebhook:
		if r.WebhookLogs == nil {
			return
		}
		webhookID, ok := filter.ParseActionParamID(action.ActionParam)
		if !ok {
			return
		}
		_ = r.WebhookLogs.EnqueueWebhookLogs(ctx, []int64{webhookID}, entry.ID)
	case storage.FilterActionDelete:
		if r.Entries == nil {
			return
		}
		if _, err := r.Entries.MarkEntriesRemoved(ctx, userID, []int64{entry.ID}); err != nil && r.Log != nil {
			r.Log.Warn("filter action delete failed", "entry_id", entry.ID, "err", err)
		}
	}
}

func enqueueLegacyFilterWebhooks(
	ctx context.Context,
	webhooks []storage.Webhook,
	webhookLogs storage.WebhookLogStore,
	matchedFilterIDs map[int64]struct{},
	entryID int64,
) {
	if webhookLogs == nil || len(webhooks) == 0 {
		return
	}
	var ids []int64
	for _, wh := range webhooks {
		if !wh.Enabled || wh.FilterID == nil {
			continue
		}
		if _, ok := matchedFilterIDs[*wh.FilterID]; ok {
			ids = append(ids, wh.ID)
		}
	}
	if len(ids) > 0 {
		_ = webhookLogs.EnqueueWebhookLogs(ctx, ids, entryID)
	}
}
