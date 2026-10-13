//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"rssam/internal/filter"
	httpserver "rssam/internal/http"
	"rssam/internal/storage"
)

// TestE2E_InboundWebhook_NodeToNode: a polled feed bound to an outgoing HTTP
// webhook whose address is this instance's own /hooks/{id}. The worker
// delivers the two new entries signed with the inbound feed's secret; the
// receiver stores them in the inbound feed, unread for its subscriber, and
// logs the delivery as a poll. Then the raw endpoint: a replay inserts
// nothing, a bad signature is 401, a stale sent_at 400, an unknown or
// polled feed 404, a source outside the allow-list 403, and with the
// feature off the path does not exist.
func TestE2E_InboundWebhook_NodeToNode(t *testing.T) {
	env := newEnv(t, map[string]string{
		"/src.xml": rssDocument("Source", []string{"alpha", "beta"}),
	})
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	receiver := httptest.NewServer(httpserver.New(httpserver.Dependencies{
		DB:              env.pool,
		Logger:          log,
		FilterEngine:    filter.New(filter.Config{}),
		InboundWebhooks: true,
	}).Handler())
	t.Cleanup(receiver.Close)

	inbound, secret, err := env.store.CreateInboundFeed(ctx, env.user.ID, storage.CreateInboundFeedParams{Title: "Hub"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.store.DeleteFeedByID(ctx, inbound.ID) })
	if len(secret) != 64 {
		t.Fatalf("secret = %q", secret)
	}
	if got, _ := env.store.InboundSecret(ctx, inbound.ID); got != secret {
		t.Fatalf("stored secret %q != %q", got, secret)
	}
	hookURL := fmt.Sprintf("%s/hooks/%d", receiver.URL, inbound.ID)

	wh := env.createWebhook(t, storage.CreateWebhookParams{Name: "to hub", URL: hookURL, Secret: secret})
	src := env.createFeed(t, "/src.xml", &wh.ID)
	t.Cleanup(func() { _ = env.store.DeleteFeedByID(ctx, src.ID) })

	waitFor(t, "entries to arrive in the inbound feed", func() bool {
		return len(env.feedEntries(t, inbound.ID)) == 2
	})
	got := env.feedEntries(t, inbound.ID)
	if got[0].Title == "" || got[0].URL == "" {
		t.Fatalf("delivered entry = %+v", got[0])
	}
	if n, _ := env.store.CountUnreadByFeed(ctx, env.user.ID, inbound.ID); n != 2 {
		t.Fatalf("unread in inbound feed = %d, want 2", n)
	}
	rows, err := env.store.ListFeedPollLog(ctx, inbound.ID, 10)
	if err != nil || len(rows) == 0 {
		t.Fatalf("poll log rows = %d err=%v", len(rows), err)
	}
	// A success after a success is not a new row (feeds.last_checked_at
	// moves instead), so one OK row stands for both deliveries.
	if len(rows) != 1 || !rows[0].OK || rows[0].Inserted != 1 {
		t.Fatalf("poll log rows = %+v", rows)
	}
	after, _ := env.store.GetFeedByID(ctx, inbound.ID)
	if after.LastEntryAt == nil || after.LastCheckedAt == nil || after.NextCheckAt == nil || after.NextCheckAt.Before(time.Now().Add(24*time.Hour)) {
		t.Fatalf("inbound feed meta = last_entry=%v last_checked=%v next=%v", after.LastEntryAt, after.LastCheckedAt, after.NextCheckAt)
	}
	// The sender saw 202 on every delivery.
	for _, l := range env.webhookLogs(t, wh.ID) {
		if l.Status != "sent" || l.LastStatusCode == nil || *l.LastStatusCode != http.StatusAccepted {
			t.Fatalf("sender log = %+v", l)
		}
	}
	// The inbound feed is never due and never queued.
	if due, _ := env.store.ListFeedsDue(ctx, 1000); contains(due, inbound.ID) {
		t.Fatal("inbound feed listed as due")
	}
	if _, _, err := env.store.EnqueueRefreshAllPollJobs(ctx); err != nil {
		t.Fatal(err)
	}
	if j, _ := env.store.GetPollFeedJob(ctx, inbound.ID); j != nil {
		t.Fatalf("refresh-all queued the inbound feed: %+v", j)
	}

	// Raw endpoint.
	post := func(url, sig string, body []byte) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if sig != "" {
			req.Header.Set("X-RSSAM-Signature", sig)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	payload := func(sentAt time.Time, urls ...string) []byte {
		items := make([]map[string]any, 0, len(urls))
		for _, u := range urls {
			items = append(items, map[string]any{"entry": map[string]any{"Title": "t " + u, "URL": u, "Content": "<p>x</p><script>1</script>"}})
		}
		b, _ := json.Marshal(map[string]any{"event_version": 1, "event_type": "digest", "count": len(items), "items": items, "sent_at": sentAt})
		return b
	}
	fresh := payload(time.Now(), "https://example.org/a", "https://example.org/b")
	if code, body := post(hookURL, signHMAC(secret, fresh), fresh); code != http.StatusAccepted || body != `{"inserted":2,"received":2}`+"\n" {
		t.Fatalf("digest: %d %s", code, body)
	}
	if code, body := post(hookURL, signHMAC(secret, fresh), fresh); code != http.StatusAccepted || body != `{"inserted":0,"received":2}`+"\n" {
		t.Fatalf("replay: %d %s", code, body)
	}
	if e := env.feedEntries(t, inbound.ID); len(e) != 4 {
		t.Fatalf("entries after digest+replay = %d, want 4", len(e))
	}
	for _, e := range env.feedEntries(t, inbound.ID) {
		if bytes.Contains([]byte(e.Content), []byte("<script")) {
			t.Fatalf("content not sanitized: %q", e.Content)
		}
	}
	if code, _ := post(hookURL, signHMAC("wrong", fresh), fresh); code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", code)
	}
	if code, _ := post(hookURL, "", fresh); code != http.StatusUnauthorized {
		t.Fatalf("no signature: %d", code)
	}
	stale := payload(time.Now().Add(-time.Hour), "https://example.org/c")
	if code, _ := post(hookURL, signHMAC(secret, stale), stale); code != http.StatusBadRequest {
		t.Fatalf("stale: %d", code)
	}
	if code, _ := post(fmt.Sprintf("%s/hooks/%d", receiver.URL, src.ID), signHMAC(secret, fresh), fresh); code != http.StatusNotFound {
		t.Fatalf("polled feed: %d", code)
	}
	if code, _ := post(receiver.URL+"/hooks/999999999", signHMAC(secret, fresh), fresh); code != http.StatusNotFound {
		t.Fatalf("unknown feed: %d", code)
	}

	rotated, err := env.store.RotateInboundSecret(ctx, inbound.ID)
	if err != nil || rotated == secret {
		t.Fatalf("rotate: %q %v", rotated, err)
	}
	if code, _ := post(hookURL, signHMAC(secret, fresh), fresh); code != http.StatusUnauthorized {
		t.Fatalf("old secret after rotation: %d", code)
	}
	if code, _ := post(hookURL, signHMAC(rotated, fresh), fresh); code != http.StatusAccepted {
		t.Fatalf("new secret after rotation: %d", code)
	}

	denied := httptest.NewServer(httpserver.New(httpserver.Dependencies{
		DB: env.pool, Logger: log, InboundWebhooks: true,
		InboundAllowCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	}).Handler())
	t.Cleanup(denied.Close)
	if code, _ := post(fmt.Sprintf("%s/hooks/%d", denied.URL, inbound.ID), signHMAC(rotated, fresh), fresh); code != http.StatusForbidden {
		t.Fatalf("outside allow-list: %d", code)
	}

	off := httptest.NewServer(httpserver.New(httpserver.Dependencies{DB: env.pool, Logger: log}).Handler())
	t.Cleanup(off.Close)
	if code, _ := post(fmt.Sprintf("%s/hooks/%d", off.URL, inbound.ID), signHMAC(rotated, fresh), fresh); code != http.StatusNotFound {
		t.Fatalf("feature off: %d", code)
	}
}

func contains(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
