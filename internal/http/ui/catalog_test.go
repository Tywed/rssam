package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/audit"
	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemSubscriptions struct {
	feeds      *uiMemFeeds
	subscribed map[int64]bool
	calls      []string
}

func (m *uiMemSubscriptions) Subscribe(_ context.Context, _ int64, feedID int64, p storage.SubscriptionParams) (storage.Subscription, error) {
	if m.subscribed[feedID] {
		return storage.Subscription{}, storage.ErrAlreadySubscribed
	}
	if p.CategoryID != nil && *p.CategoryID != 10 {
		return storage.Subscription{}, storage.ErrInvalidReference
	}
	m.subscribed[feedID] = true
	m.calls = append(m.calls, "subscribe")
	return storage.Subscription{FeedID: feedID, CategoryID: p.CategoryID}, nil
}

func (m *uiMemSubscriptions) SubscribeByURL(ctx context.Context, userID int64, feedURL string, p storage.SubscriptionParams) (storage.Feed, error) {
	for _, f := range m.feeds.feeds {
		if f.FeedURL == feedURL {
			_, err := m.Subscribe(ctx, userID, f.ID, p)
			return f, err
		}
	}
	return storage.Feed{}, storage.ErrNotFound
}

func (m *uiMemSubscriptions) UpdateSubscription(context.Context, int64, int64, storage.SubscriptionParams) (storage.Subscription, error) {
	return storage.Subscription{}, storage.ErrNotFound
}

func (m *uiMemSubscriptions) Unsubscribe(_ context.Context, _ int64, feedID int64) error {
	if !m.subscribed[feedID] {
		return storage.ErrNotFound
	}
	delete(m.subscribed, feedID)
	m.calls = append(m.calls, "unsubscribe")
	return nil
}

func (m *uiMemSubscriptions) ListFeedSubscribers(context.Context, int64) ([]storage.Subscription, error) {
	return nil, nil
}

func (m *uiMemSubscriptions) ListSubscriptions(context.Context, int64) ([]storage.Subscription, error) {
	return nil, nil
}

func (m *uiMemSubscriptions) ListCatalog(_ context.Context, _ int64, f storage.CatalogFilter) ([]storage.CatalogFeed, int, error) {
	var out []storage.CatalogFeed
	for _, feed := range m.feeds.feeds {
		if f.Query != "" && !strings.Contains(feed.Title, f.Query) {
			continue
		}
		sub := m.subscribed[feed.ID]
		if f.Subscribed != nil && *f.Subscribed != sub {
			continue
		}
		n := 1
		if sub {
			n = 2
		}
		out = append(out, storage.CatalogFeed{ID: feed.ID, Title: feed.Title, FeedURL: feed.FeedURL, FeedType: "rss", OwnerName: "bob", SubscriberCount: n, Subscribed: sub})
	}
	return out, len(out), nil
}

func (m *uiMemSubscriptions) ListFeedSubscriberNames(_ context.Context, feedID int64) ([]storage.FeedSubscriber, error) {
	out := []storage.FeedSubscriber{{UserID: 2, Username: "bob"}}
	if m.subscribed[feedID] {
		out = append(out, storage.FeedSubscriber{UserID: 1, Username: "alice"})
	}
	return out, nil
}

// A reader browses the catalog, subscribes with a category, sees the feed
// under «Мои подписки» and unsubscribes; both actions are CSRF-protected
// and audited. The feed card lists subscribers only to admins.
func TestUI_CatalogSubscribeUnsubscribe(t *testing.T) {
	feeds := &uiMemFeeds{feeds: []storage.Feed{
		{ID: 1, Title: "Shared News", FeedURL: "https://example.com/news.xml"},
		{ID: 2, Title: "Other", FeedURL: "https://example.com/other.xml"},
	}}
	subs := &uiMemSubscriptions{feeds: feeds, subscribed: map[int64]bool{2: true}}
	auditStore := &memAuditStore{}
	h, err := NewHandler(Config{
		Users:         &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleReader}},
		Sessions:      &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:       uiMemEntries{},
		Feeds:         feeds,
		Subscriptions: subs,
		Categories:    &uiMemCategories{cats: []storage.Category{{ID: 10, Title: "News"}}},
		CSRFSecret:    "csrf-test",
		Audit:         &audit.Recorder{Store: auditStore},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	token := auth.CSRFToken("csrf-test", sid)

	rec := getPage(t, mux, sid, "/ui/catalog")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog: %d", rec.Code)
	}
	for _, want := range []string{"Shared News", "/ui/catalog/1/subscribe", "Подписаться", "/ui/feeds/2/unsubscribe", "Новые ленты в каталог добавляют редакторы", `<option value="10">News</option>`, `href="/ui/catalog"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("catalog body missing %q", want)
		}
	}
	if strings.Contains(body, "/ui/feeds/new") {
		t.Fatal("reader must not see the add-feed button")
	}
	if body := getPage(t, mux, sid, "/ui/catalog?view=new&q=Shared").Body.String(); !strings.Contains(body, "Shared News") || strings.Contains(body, "other.xml") {
		t.Fatal("view=new&q filter should leave only Shared News")
	}

	if rec := postForm(t, mux, sid, "/ui/catalog/1/subscribe", url.Values{}); rec.Code != http.StatusForbidden {
		t.Fatalf("subscribe without csrf: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/catalog/1/subscribe", url.Values{"csrf_token": {token}, "category_id": {"99"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign category: %d %s", rec.Code, rec.Body.String())
	}
	rec = postForm(t, mux, sid, "/ui/catalog/1/subscribe", url.Values{"csrf_token": {token}, "category_id": {"10"}})
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "subscribed=Shared+News") {
		t.Fatalf("subscribe: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := postForm(t, mux, sid, "/ui/catalog/1/subscribe", url.Values{"csrf_token": {token}}); rec.Code != http.StatusFound {
		t.Fatalf("repeat subscribe should redirect, got %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/catalog/77/subscribe", url.Values{"csrf_token": {token}}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown feed: %d", rec.Code)
	}
	if body := getPage(t, mux, sid, "/ui/catalog?view=mine").Body.String(); !strings.Contains(body, "Shared News") || !strings.Contains(body, "/ui/feeds/1/unsubscribe") {
		t.Fatal("subscribed feed should be under «Мои подписки» with an unsubscribe button")
	}

	body = getPage(t, mux, sid, "/ui/feeds/1").Body.String()
	if !strings.Contains(body, "Подписчиков: 2") || strings.Contains(body, "bob, alice") || !strings.Contains(body, "/ui/feeds/1/unsubscribe") {
		t.Fatalf("feed card for reader: %s", body)
	}

	if rec := postForm(t, mux, sid, "/ui/feeds/1/unsubscribe", url.Values{"csrf_token": {token}}); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/feeds" {
		t.Fatalf("unsubscribe: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/feeds/1/unsubscribe", url.Values{"csrf_token": {token}}); rec.Code != http.StatusNotFound {
		t.Fatalf("repeat unsubscribe: %d", rec.Code)
	}
	if strings.Join(subs.calls, ",") != "subscribe,unsubscribe" {
		t.Fatalf("calls = %v", subs.calls)
	}
	if len(auditStore.rows) != 2 || auditStore.rows[0].Action != storage.AuditSubscriptionCreate || auditStore.rows[1].Action != storage.AuditSubscriptionDelete || *auditStore.rows[1].TargetID != 1 {
		t.Fatalf("audit = %+v", auditStore.rows)
	}
}

// Admins see subscriber names on the feed card and the delete button
// spells out how many users lose the feed.
func TestUI_FeedCardSubscribersForAdmin(t *testing.T) {
	feeds := &uiMemFeeds{feeds: []storage.Feed{{ID: 1, Title: "Shared News", FeedURL: "https://example.com/news.xml"}}}
	subs := &uiMemSubscriptions{feeds: feeds, subscribed: map[int64]bool{1: true}}
	h, err := NewHandler(Config{
		Users:         &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin}},
		Sessions:      &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:       uiMemEntries{},
		Feeds:         feeds,
		Subscriptions: subs,
		Categories:    &uiMemCategories{},
		CSRFSecret:    "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	if body := getPage(t, mux, sid, "/ui/feeds/1").Body.String(); !strings.Contains(body, "Подписчиков: 2 — bob, alice") {
		t.Fatalf("admin feed card: %s", body)
	}
	body := getPage(t, mux, sid, "/ui/feeds/1/edit").Body.String()
	if !strings.Contains(body, "Удалить из каталога (2 подписчиков)") || !strings.Contains(body, "/ui/feeds/1/unsubscribe") {
		t.Fatalf("admin feed form: %s", body)
	}
	if body := getPage(t, mux, sid, "/ui/catalog").Body.String(); !strings.Contains(body, "/ui/feeds/new") {
		t.Fatal("admin should see the add-feed button in the catalog")
	}
}

// uiDupFeeds refuses every CreateFeed as a duplicate URL, standing in for a
// catalog that already has the address.
type uiDupFeeds struct{ *uiMemFeeds }

func (uiDupFeeds) CreateFeed(context.Context, int64, storage.CreateFeedParams) (storage.Feed, error) {
	return storage.Feed{}, storage.ErrDuplicateFeedURL
}

// The feed form with a URL that is already in the catalog subscribes the
// editor instead of failing; a second attempt reports the subscription.
func TestUI_FeedFormDuplicateURLSubscribes(t *testing.T) {
	feeds := &uiMemFeeds{feeds: []storage.Feed{{ID: 1, Title: "Shared News", FeedURL: "https://example.com/news.xml"}}}
	subs := &uiMemSubscriptions{feeds: feeds, subscribed: map[int64]bool{}}
	auditStore := &memAuditStore{}
	h, err := NewHandler(Config{
		Users:         &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleEditor}},
		Sessions:      &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:       uiMemEntries{},
		Feeds:         uiDupFeeds{feeds},
		Subscriptions: subs,
		Categories:    &uiMemCategories{cats: []storage.Category{{ID: 10, Title: "News"}}},
		CSRFSecret:    "csrf-test",
		Audit:         &audit.Recorder{Store: auditStore},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	token := auth.CSRFToken("csrf-test", sid)

	form := url.Values{"csrf_token": {token}, "feed_url": {"https://example.com/news.xml"}, "title": {"x"}, "interval_minutes": {"60"}, "category_id": {"10"}}
	rec := postForm(t, mux, sid, "/ui/feeds", form)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/catalog?subscribed=Shared+News" {
		t.Fatalf("duplicate URL: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if !subs.subscribed[1] || len(auditStore.rows) != 1 || auditStore.rows[0].Action != storage.AuditSubscriptionCreate {
		t.Fatalf("subscribed=%v audit=%+v", subs.subscribed, auditStore.rows)
	}
	rec = postForm(t, mux, sid, "/ui/feeds", form)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Вы уже подписаны на эту ленту") {
		t.Fatalf("second attempt: %d", rec.Code)
	}
}
