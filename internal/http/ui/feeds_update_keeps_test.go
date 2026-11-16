package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

// The edit form has no fields for the rules, proxy, crawler and user agent
// set through the API or OPML; saving the form must not blank them.
func TestFeedUpdateForm_KeepsFieldsTheFormDoesNotHave(t *testing.T) {
	catID := int64(10)
	feeds := &uiMemFeeds{feeds: []storage.Feed{{
		ID: 1, Title: "News Feed", FeedURL: "https://example.com/f.xml", FeedType: "rss", CategoryID: &catID, Subscribed: true,
		ScraperRules: "article", RewriteRules: "a\nb", BlockedRules: "spam", KeepRules: "keep",
		FetchViaProxy: true, Crawler: true, UserAgent: "UA/1",
	}}}
	h, err := NewHandler(Config{
		Users:      &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      feeds,
		Categories: &uiMemCategories{cats: []storage.Category{{ID: catID, Title: "News"}}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, nil, mux)
	form := url.Values{
		"csrf_token":       {auth.CSRFToken("csrf-test", sid)},
		"feed_url":         {"https://example.com/f.xml"},
		"title":            {"Renamed"},
		"interval_minutes": {"30"},
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/feeds/1", strings.NewReader(form.Encode()))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	p := feeds.lastUpdate
	if p == nil {
		t.Fatal("UpdateFeed not called")
	}
	if p.Title != "Renamed" || p.IntervalMinutes != 30 {
		t.Fatalf("form fields not applied: %+v", p)
	}
	if p.ScraperRules != "article" || p.RewriteRules != "a\nb" || p.BlockedRules != "spam" || p.KeepRules != "keep" ||
		!p.FetchViaProxy || !p.Crawler || p.UserAgent != "UA/1" {
		t.Fatalf("fields the form does not have were changed: %+v", p)
	}
	if !p.SetWebhook {
		t.Fatalf("the form owns the webhook select: %+v", p)
	}
}
