package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/reader"
	"rssam/internal/storage"
)

type discoverFeedsStore struct {
	uiMemFeeds
	created []storage.CreateFeedParams
}

func (m *discoverFeedsStore) CreateFeed(_ context.Context, _ int64, p storage.CreateFeedParams) (storage.Feed, error) {
	m.created = append(m.created, p)
	return storage.Feed{ID: int64(len(m.created)), FeedURL: p.FeedURL, Title: p.Title}, nil
}

func TestUI_FeedDetectAndCreateUseDiscoveredURL(t *testing.T) {
	feeds := &discoverFeedsStore{}
	discover := func(_ context.Context, feedURL, feedType string, _ bool) (reader.Discovery, error) {
		switch feedURL {
		case "https://site.example/":
			return reader.Discovery{FeedURL: "https://site.example/feed.xml", Title: "Site"}, nil
		case "https://site.example/feed.xml":
			return reader.Discovery{FeedURL: feedURL, Title: "Site"}, nil
		case "https://nofeed.example/":
			return reader.Discovery{}, reader.ErrNoFeedFound
		}
		return reader.Discovery{}, errors.New("unexpected " + feedURL)
	}
	h, err := NewHandler(Config{
		Users:        &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true}},
		Sessions:     &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:      uiMemEntries{},
		Feeds:        feeds,
		Categories:   &uiMemCategories{},
		DiscoverFeed: discover,
		CSRFSecret:   "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	get := func(u string) feedDetectResponse {
		req := httptest.NewRequest(http.MethodGet, "/ui/feeds/detect-type?url="+url.QueryEscape(u), nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var out feedDetectResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, rec.Body.String())
		}
		return out
	}
	if d := get("https://site.example/"); !d.Valid || d.FeedURL != "https://site.example/feed.xml" || d.Title != "Site" {
		t.Fatalf("page: %+v", d)
	}
	if d := get("https://site.example/feed.xml"); !d.Valid || d.FeedURL != "" {
		t.Fatalf("direct feed must not echo feed_url: %+v", d)
	}
	if d := get("https://nofeed.example/"); d.Valid || !strings.Contains(d.Error, "не найдено") {
		t.Fatalf("no feed: %+v", d)
	}

	form := url.Values{"csrf_token": {token}, "feed_url": {"https://site.example/"}, "interval_minutes": {"60"}}
	req := httptest.NewRequest(http.MethodPost, "/ui/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if len(feeds.created) != 1 || feeds.created[0].FeedURL != "https://site.example/feed.xml" || feeds.created[0].Title != "Site" {
		t.Fatalf("created %+v", feeds.created)
	}

	form.Set("feed_url", "https://nofeed.example/")
	req = httptest.NewRequest(http.MethodPost, "/ui/feeds", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "не найдено ссылок") || len(feeds.created) != 1 {
		t.Fatalf("no-feed page must re-render the form: %d created=%d", rec.Code, len(feeds.created))
	}
}
