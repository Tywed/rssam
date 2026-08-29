package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func newAdminPauseTestHandler(t *testing.T) (http.Handler, *uiMemFeeds) {
	t.Helper()
	feedStore := &uiMemFeeds{feeds: []storage.Feed{
		{ID: 1, Title: "News", FeedType: "rss"},
		{ID: 2, Title: "Broken", FeedType: "rss", LastError: "timeout", ParsingErrorCount: 3, PollPaused: true},
	}}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      feedStore,
		Categories: &uiMemCategories{},
		AdminFeeds: &uiMemAdminFeeds{},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux, feedStore
}

func TestAdminFeedManualPauseUnpause(t *testing.T) {
	mux, feedStore := newAdminPauseTestHandler(t)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	post := func(path string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {token}}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := post("/ui/admin/feeds/1/pause")
	if rec.Code != http.StatusFound {
		t.Fatalf("pause: status=%d", rec.Code)
	}
	if !feedStore.feeds[0].ManualPaused {
		t.Fatal("expected manual_paused after pause")
	}

	rec = post("/ui/admin/feeds/2/pause")
	if rec.Code != http.StatusFound {
		t.Fatalf("pause circuit feed: status=%d", rec.Code)
	}
	if !feedStore.feeds[1].ManualPaused || !feedStore.feeds[1].PollPaused || feedStore.feeds[1].ParsingErrorCount != 3 {
		t.Fatalf("pause must preserve circuit state: %+v", feedStore.feeds[1])
	}

	rec = post("/ui/admin/feeds/2/unpause")
	if rec.Code != http.StatusFound {
		t.Fatalf("unpause: status=%d", rec.Code)
	}
	if feedStore.feeds[1].ManualPaused {
		t.Fatal("expected manual_paused cleared")
	}
	if !feedStore.feeds[1].PollPaused || feedStore.feeds[1].ParsingErrorCount != 3 {
		t.Fatalf("unpause must not reset circuit: %+v", feedStore.feeds[1])
	}

	rec = post("/ui/admin/feeds/2/reset-circuit")
	if rec.Code != http.StatusFound {
		t.Fatalf("reset-circuit: status=%d", rec.Code)
	}
	if feedStore.feeds[1].PollPaused || feedStore.feeds[1].ParsingErrorCount != 0 {
		t.Fatalf("reset-circuit should clear circuit state: %+v", feedStore.feeds[1])
	}
}

func TestClassifyAdminFeedStatusManualPause(t *testing.T) {
	now := time.Now()
	if got := classifyAdminFeedStatus(storage.AdminFeedRow{Feed: storage.Feed{ManualPaused: true}}, now); got != "paused" {
		t.Fatalf("manual pause status: got %q", got)
	}
}

func TestFeedParamsStoreHashOnly(t *testing.T) {
	form := url.Values{
		"feed_url":        {"https://example.com/feed.xml"},
		"store_hash_only": {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	params, err := feedParamsFromForm(req)
	if err != nil {
		t.Fatal(err)
	}
	if !params.StoreHashOnly {
		t.Fatal("expected store_hash_only=true")
	}
}
