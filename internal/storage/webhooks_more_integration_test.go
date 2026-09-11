//go:build integration

package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIntegration_WebhookUpdateAndListing(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "whupd")
	other := newIntegrationUser(t, store, "whupd_other")

	http1, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "http", URL: "https://example.com/a", Method: "post", Headers: []byte(`{"X-A":"1"}`),
		Secret: "s3cret", Enabled: true, Kind: WebhookKindHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	tg, err := store.CreateWebhook(ctx, CreateWebhookParams{
		UserID: owner.ID, Name: "tg", Kind: WebhookKindTelegram, Enabled: false,
		ProviderConfig: []byte(`{"bot_token":"123:abc","chat_id":"-100"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tg.URL != TelegramAPIDefault+"/bot/sendMessage" || strings.Contains(tg.URL, "123:abc") {
		t.Fatalf("telegram display url leaks token or is wrong: %q", tg.URL)
	}
	if _, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "bad", URL: "https://x", Headers: []byte(`not-json`)}); err == nil {
		t.Fatal("invalid headers accepted")
	}
	if _, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "bad", URL: "", Kind: WebhookKindHTTP}); err == nil {
		t.Fatal("empty url accepted")
	}

	all, total, err := store.ListWebhooks(ctx, owner.ID, 1, 0)
	if err != nil || total != 2 || len(all) != 1 || all[0].ID != tg.ID {
		t.Fatalf("list newest first: n=%d total=%d first=%v err=%v", len(all), total, all, err)
	}
	enabled, err := store.ListEnabledWebhooks(ctx, owner.ID, 0)
	if err != nil || len(enabled) != 1 || enabled[0].ID != http1.ID {
		t.Fatalf("enabled: %v err=%v", enabled, err)
	}
	if foreign, _, _ := store.ListWebhooks(ctx, other.ID, 10, 0); len(foreign) != 0 {
		t.Fatalf("foreign list: %v", foreign)
	}

	// Update: secret kept when nil, replaced when set, dropped for chat kinds;
	// the Telegram token survives a config without bot_token (masked form).
	upd, err := store.UpdateWebhook(ctx, UpdateWebhookParams{
		ID: http1.ID, UserID: owner.ID, Name: ptr("renamed"), URL: "https://example.com/b", Method: "", Headers: nil,
		BodyTemplate: "{{.entry.Title}}", Enabled: false, Kind: WebhookKindHTTP,
	})
	if err != nil || upd.Name != "renamed" || upd.URL != "https://example.com/b" || upd.Method != "POST" || upd.Secret != "s3cret" || upd.Enabled || string(upd.Headers) != "{}" {
		t.Fatalf("update http: %+v err=%v", upd, err)
	}
	upd, _ = store.UpdateWebhook(ctx, UpdateWebhookParams{ID: http1.ID, UserID: owner.ID, URL: upd.URL, Secret: ptr(""), Enabled: true})
	if upd.Secret != "" || !upd.Enabled || upd.Name != "renamed" {
		t.Fatalf("secret clear / name kept: %+v", upd)
	}
	tgUpd, err := store.UpdateWebhook(ctx, UpdateWebhookParams{
		ID: tg.ID, UserID: owner.ID, Enabled: true, ProviderConfig: []byte(`{"chat_id":"-200","bot_token":""}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseTelegramProviderConfig(tgUpd.ProviderConfig)
	if err != nil || cfg.BotToken != "123:abc" || cfg.ChatID != "-200" {
		t.Fatalf("merged telegram config: %+v err=%v", cfg, err)
	}
	if _, err := store.UpdateWebhook(ctx, UpdateWebhookParams{ID: http1.ID, UserID: other.ID, URL: "https://x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign update: err=%v", err)
	}
	if _, err := store.UpdateWebhook(ctx, UpdateWebhookParams{ID: http1.ID, UserID: owner.ID, URL: "https://x", Headers: []byte(`[`)}); err == nil {
		t.Fatal("invalid headers on update accepted")
	}
	if _, err := store.UpdateWebhook(ctx, UpdateWebhookParams{ID: http1.ID, UserID: owner.ID, URL: "https://x", Kind: "carrier-pigeon"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := store.UpdateWebhook(ctx, UpdateWebhookParams{ID: http1.ID, UserID: owner.ID, URL: "https://x", OnSuccessEntry: "explode"}); err == nil {
		t.Fatal("unknown on_success accepted")
	}
}

func TestIntegration_AdminWebhookBindingsLogsAndRetryAll(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "whadm")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 3)

	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "adm", URL: "https://example.com/adm", Headers: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, IntervalMinutes: 60, WebhookID: &wh.ID}); err != nil {
		t.Fatal(err)
	}
	flt, err := store.CreateFilter(ctx, CreateFilterParams{
		UserID: owner.ID, Name: "to-hook", Enabled: true, FeedScope: FilterFeedScopeAll,
		Rules:   []CreateFilterRuleParams{{Field: "title", Pattern: "entry", Op: "and"}},
		Actions: []CreateFilterActionParams{{ActionType: "webhook", ActionParam: itoa(wh.ID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFilterMatches(ctx, []CreateFilterMatchParams{{FilterID: flt.ID, EntryID: entries[0].ID, MatchedAt: time.Now(), Details: []byte(`{"rule":"title"}`)}}); err != nil {
		t.Fatal(err)
	}

	feeds, err := store.ListWebhookBindingFeeds(ctx, owner.ID, wh.ID)
	if err != nil || len(feeds) != 1 || feeds[0].ID != feed.ID || feeds[0].Title != feed.Title {
		t.Fatalf("binding feeds: %v err=%v", feeds, err)
	}
	filters, err := store.ListWebhookBindingFilters(ctx, owner.ID, wh.ID)
	if err != nil || len(filters) != 1 || filters[0].ID != flt.ID || filters[0].Name != "to-hook" {
		t.Fatalf("binding filters: %v err=%v", filters, err)
	}
	row, _ := store.GetAdminWebhook(ctx, owner.ID, wh.ID)
	if row.FeedBindingCount != 1 || row.FilterBindingCount != 1 {
		t.Fatalf("binding counts: %+v", row)
	}

	for _, e := range entries {
		if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, e.ID); err != nil {
			t.Fatal(err)
		}
	}
	mine := claimOwnWebhookLogs(t, store, wh.ID, 3)
	byEntry := map[int64]WebhookLog{}
	for _, l := range mine {
		byEntry[l.EntryID] = l
	}
	if err := store.MarkWebhookLogSent(ctx, byEntry[entries[0].ID].ID, 1, 200, "ok"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWebhookLogFailed(ctx, byEntry[entries[1].ID].ID, ptr(500), "boom", "", 1, ptr(time.Now().Add(time.Hour)), false); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkWebhookLogFailed(ctx, byEntry[entries[2].ID].ID, nil, "dead", "", 3, nil, true); err != nil {
		t.Fatal(err)
	}

	rows, total, err := store.ListWebhookLogRows(ctx, wh.ID, "", nil, 10, 0)
	if err != nil || total != 3 || len(rows) != 3 {
		t.Fatalf("all rows: n=%d total=%d err=%v", len(rows), total, err)
	}
	for _, r := range rows {
		if r.FeedID != feed.ID || r.FeedTitle != feed.Title || !strings.HasPrefix(r.EntryTitle, "entry ") {
			t.Fatalf("row join columns: %+v", r)
		}
		switch r.EntryID {
		case entries[0].ID:
			if r.TriggerSource != "filter" || r.FilterName != "to-hook" {
				t.Fatalf("filter-triggered row: %+v", r)
			}
		default:
			if r.TriggerSource != "feed" || r.FilterName != "" {
				t.Fatalf("feed-triggered row: %+v", r)
			}
		}
	}
	for status, want := range map[string]int{"sent": 1, "failed": 1, "dead": 1, "pending": 0, "bogus": 3} {
		_, total, err := store.ListWebhookLogRows(ctx, wh.ID, status, nil, 0, -1)
		if err != nil || total != want {
			t.Fatalf("status %q: total=%d err=%v want %d", status, total, err, want)
		}
	}
	future := time.Now().Add(time.Hour)
	if _, total, _ := store.ListWebhookLogRows(ctx, wh.ID, "", &future, 10, 0); total != 0 {
		t.Fatalf("since in the future: total=%d", total)
	}
	page, total, _ := store.ListWebhookLogRows(ctx, wh.ID, "", nil, 2, 2)
	if total != 3 || len(page) != 1 {
		t.Fatalf("offset paging: n=%d total=%d", len(page), total)
	}

	if n, _ := store.DueWebhookLogCount(ctx); n != 0 {
		t.Fatalf("nothing due yet (failed row retries in an hour): %d", n)
	}
	n, err := store.RetryAllWebhookLogs(ctx, wh.ID)
	if err != nil || n != 2 {
		t.Fatalf("retry all: n=%d err=%v", n, err)
	}
	if due, _ := store.DueWebhookLogCount(ctx); due != 2 {
		t.Fatalf("due after retry-all: %d", due)
	}
	_, total, _ = store.ListWebhookLogRows(ctx, wh.ID, "pending", nil, 10, 0)
	if total != 2 {
		t.Fatalf("pending after retry-all: %d", total)
	}
}

func TestIntegration_WebhookDeliveryContext(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "whctx")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 2)
	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: owner.ID, Name: "ctx", URL: "https://example.com/ctx", Headers: []byte(`{}`), Enabled: true, BodyTemplate: "{{.feed.Title}}|{{.entry.Title}}"})
	if err != nil {
		t.Fatal(err)
	}
	flt, err := store.CreateFilter(ctx, CreateFilterParams{
		UserID: owner.ID, Name: "ctx-filter", Enabled: true, FeedScope: FilterFeedScopeAll,
		Rules: []CreateFilterRuleParams{{Field: "title", Pattern: "0", Op: "and"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFilterMatches(ctx, []CreateFilterMatchParams{{FilterID: flt.ID, EntryID: entries[0].ID, MatchedAt: time.Now(), Details: []byte(`{"m":1}`)}}); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := store.EnqueueWebhookLogs(ctx, []int64{wh.ID}, e.ID); err != nil {
			t.Fatal(err)
		}
	}
	logs := claimOwnWebhookLogs(t, store, wh.ID, 2)
	for _, l := range logs {
		dc, err := store.LoadWebhookDeliveryContext(ctx, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		if dc.Webhook.ID != wh.ID || dc.Entry.ID != l.EntryID || dc.Feed.ID != feed.ID || dc.Feed.Title != feed.Title {
			t.Fatalf("context: %+v", dc)
		}
		if l.EntryID == entries[0].ID {
			if dc.Filter == nil || dc.Filter.ID != flt.ID || dc.Filter.Name != "ctx-filter" || string(dc.MatchDetails) != `{"m": 1}` {
				t.Fatalf("matched entry context: filter=%+v details=%s", dc.Filter, dc.MatchDetails)
			}
		} else if dc.Filter != nil || dc.MatchDetails != nil {
			t.Fatalf("unmatched entry got filter context: %+v", dc)
		}

		out, err := BuildWebhookOutbound(dc.Webhook, dc.Feed, dc.Entry, dc.Filter)
		if err != nil || out.Method != "POST" || out.URL != wh.URL || out.Kind != WebhookKindHTTP || string(out.Body) != feed.Title+"|"+dc.Entry.Title {
			t.Fatalf("outbound: %+v err=%v", out, err)
		}
	}
	if _, err := store.LoadWebhookDeliveryContext(ctx, 999999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing log: err=%v", err)
	}
}
