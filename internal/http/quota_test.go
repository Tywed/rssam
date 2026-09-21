package httpserver

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"rssam/internal/quota"
)

// Editor quotas: the feed count and the interval floor stop an editor via
// REST and OPML, and never an admin.
func TestRouter_EditorQuotas(t *testing.T) {
	env := newRouterEnv(t, func(d *Dependencies) {
		d.FeedStore = newTenantFeedStore()
		d.CategoryStore = &fakeCategoryStore{}
		d.WebhookStore = newMemWebhookStore()
		d.Quotas = quota.Limits{MaxFeedsPerEditor: 2, MaxWebhooksPerEditor: 1, EditorMinPollInterval: 5 * time.Minute}
	})

	env.want(env.do(http.MethodPost, "/v1/feeds", env.editorKey, `{"feed_url":"https://example.com/1.xml","interval_minutes":1}`), http.StatusUnprocessableEntity)
	env.want(env.do(http.MethodPost, "/v1/feeds", env.editorKey, `{"feed_url":"https://example.com/1.xml","interval_minutes":5}`), http.StatusCreated)
	rec := env.want(env.do(http.MethodPost, "/v1/feeds", env.editorKey, `{"feed_url":"https://example.com/2.xml"}`), http.StatusCreated)
	feed, _ := decodeData[feedDTO](t, rec)
	rec = env.want(env.do(http.MethodPost, "/v1/feeds", env.editorKey, `{"feed_url":"https://example.com/3.xml"}`), http.StatusTooManyRequests)
	if !strings.Contains(rec.Body.String(), "feed limit reached (2 per editor)") {
		t.Fatalf("body=%s", rec.Body.String())
	}
	env.want(env.do(http.MethodPut, "/v1/feeds/"+itoa(feed.ID), env.editorKey, `{"feed_url":"https://example.com/2.xml","interval_minutes":2}`), http.StatusUnprocessableEntity)
	env.want(env.do(http.MethodPut, "/v1/feeds/"+itoa(feed.ID), env.editorKey, `{"feed_url":"https://example.com/2.xml","interval_minutes":10}`), http.StatusOK)

	rec = env.want(env.do(http.MethodPost, "/v1/feeds/import", env.editorKey, `<opml version="2.0"><body><outline type="rss" xmlUrl="https://example.com/4.xml"/><outline type="rss" xmlUrl="https://example.com/5.xml"/></body></opml>`), http.StatusOK)
	report, _ := decodeData[importReportDTO](t, rec)
	if report.FeedsCreated != 0 || len(report.Errors) != 1 || report.Errors[0].Reason != "feed limit reached (2 per editor)" {
		t.Fatalf("import report = %+v", report)
	}

	env.want(env.do(http.MethodPost, "/v1/webhooks", env.editorKey, `{"name":"a","url":"https://example.com/hook"}`), http.StatusCreated)
	env.want(env.do(http.MethodPost, "/v1/webhooks", env.editorKey, `{"name":"b","url":"https://example.com/hook2"}`), http.StatusTooManyRequests)

	// Admins are exempt from every limit.
	for i := range 3 {
		env.want(env.do(http.MethodPost, "/v1/feeds", env.adminKey, `{"feed_url":"https://example.com/a`+itoa(int64(i))+`.xml","interval_minutes":1}`), http.StatusCreated)
	}
	env.want(env.do(http.MethodPost, "/v1/webhooks", env.adminKey, `{"name":"a","url":"https://example.com/hook"}`), http.StatusCreated)
	env.want(env.do(http.MethodPost, "/v1/webhooks", env.adminKey, `{"name":"b","url":"https://example.com/hook2"}`), http.StatusCreated)
}
