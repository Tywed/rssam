package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"rssam/internal/storage"
)

// catalogFeedStore turns tenantFeedStore into a shared catalog: feed_url is
// unique across users and the creator becomes the first subscriber.
type catalogFeedStore struct {
	*tenantFeedStore
	subs *memSubscriptionStore
}

func newCatalogFeedStore() *catalogFeedStore {
	s := &catalogFeedStore{tenantFeedStore: newTenantFeedStore()}
	s.subs = newMemSubscriptionStore(s)
	return s
}

func (s *catalogFeedStore) CreateFeed(ctx context.Context, userID int64, p storage.CreateFeedParams) (storage.Feed, error) {
	all, _ := s.ListAllFeeds(ctx, 0)
	for _, f := range all {
		if f.FeedURL == p.FeedURL {
			return storage.Feed{}, storage.ErrDuplicateFeedURL
		}
	}
	feed, err := s.tenantFeedStore.CreateFeed(ctx, userID, p)
	if err != nil {
		return storage.Feed{}, err
	}
	_, err = s.subs.Subscribe(ctx, userID, feed.ID, storage.SubscriptionParams{CategoryID: p.CategoryID, WebhookID: p.WebhookID})
	return feed, err
}

func (s *catalogFeedStore) ListAllFeeds(context.Context, int) ([]storage.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.Feed
	for _, byURL := range s.feeds {
		for _, f := range byURL {
			out = append(out, f)
		}
	}
	return out, nil
}

type memSubscriptionStore struct {
	mu    sync.Mutex
	feeds *catalogFeedStore
	subs  map[int64]map[int64]storage.Subscription // userID -> feedID
}

func newMemSubscriptionStore(feeds *catalogFeedStore) *memSubscriptionStore {
	return &memSubscriptionStore{feeds: feeds, subs: make(map[int64]map[int64]storage.Subscription)}
}

func (m *memSubscriptionStore) Subscribe(_ context.Context, userID, feedID int64, p storage.SubscriptionParams) (storage.Subscription, error) {
	if _, err := m.feeds.GetFeedByID(context.Background(), feedID); err != nil {
		return storage.Subscription{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subs[userID] == nil {
		m.subs[userID] = make(map[int64]storage.Subscription)
	}
	if _, ok := m.subs[userID][feedID]; ok {
		return storage.Subscription{}, storage.ErrAlreadySubscribed
	}
	s := storage.Subscription{UserID: userID, FeedID: feedID, CategoryID: p.CategoryID, WebhookID: p.WebhookID}
	m.subs[userID][feedID] = s
	return s, nil
}

func (m *memSubscriptionStore) SubscribeByURL(ctx context.Context, userID int64, feedURL string, p storage.SubscriptionParams) (storage.Feed, error) {
	feeds, _ := m.feeds.ListAllFeeds(ctx, 0)
	for _, f := range feeds {
		if f.FeedURL == feedURL {
			if _, err := m.Subscribe(ctx, userID, f.ID, p); err != nil {
				return storage.Feed{}, err
			}
			f.CategoryID = p.CategoryID
			return f, nil
		}
	}
	return storage.Feed{}, storage.ErrNotFound
}

func (m *memSubscriptionStore) UpdateSubscription(_ context.Context, userID, feedID int64, p storage.SubscriptionParams) (storage.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[userID][feedID]
	if !ok {
		return storage.Subscription{}, storage.ErrNotFound
	}
	s.CategoryID, s.WebhookID = p.CategoryID, p.WebhookID
	m.subs[userID][feedID] = s
	return s, nil
}

func (m *memSubscriptionStore) Unsubscribe(_ context.Context, userID, feedID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.subs[userID][feedID]; !ok {
		return storage.ErrNotFound
	}
	delete(m.subs[userID], feedID)
	return nil
}

func (m *memSubscriptionStore) ListFeedSubscribers(_ context.Context, feedID int64) ([]storage.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.Subscription
	for _, byFeed := range m.subs {
		if s, ok := byFeed[feedID]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memSubscriptionStore) ListSubscriptions(_ context.Context, userID int64) ([]storage.Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]storage.Subscription, 0, len(m.subs[userID]))
	for _, s := range m.subs[userID] {
		out = append(out, s)
	}
	return out, nil
}

func (m *memSubscriptionStore) ListCatalog(ctx context.Context, userID int64, f storage.CatalogFilter) ([]storage.CatalogFeed, int, error) {
	feeds, _ := m.feeds.ListAllFeeds(ctx, 0)
	var out []storage.CatalogFeed
	for _, feed := range feeds {
		if f.Query != "" && !strings.Contains(feed.FeedURL, f.Query) && !strings.Contains(feed.Title, f.Query) {
			continue
		}
		subs, _ := m.ListFeedSubscribers(ctx, feed.ID)
		row := storage.CatalogFeed{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, OwnerID: feed.OwnerID, SubscriberCount: len(subs)}
		for _, s := range subs {
			if s.UserID == userID {
				row.Subscribed = true
			}
		}
		if f.Subscribed != nil && *f.Subscribed != row.Subscribed {
			continue
		}
		out = append(out, row)
	}
	return out, len(out), nil
}

func (m *memSubscriptionStore) ListFeedSubscriberNames(ctx context.Context, feedID int64) ([]storage.FeedSubscriber, error) {
	subs, _ := m.ListFeedSubscribers(ctx, feedID)
	out := make([]storage.FeedSubscriber, 0, len(subs))
	for _, s := range subs {
		out = append(out, storage.FeedSubscriber{UserID: s.UserID, CategoryID: s.CategoryID})
	}
	return out, nil
}

// A reader subscribes to a catalog feed created by an editor, a second
// editor posting the same URL is subscribed rather than refused, and
// both subscribe/unsubscribe land in the audit log.
func TestRouter_SubscriptionsLifecycle(t *testing.T) {
	log := &memAuditLog{}
	feeds := newCatalogFeedStore()
	env := newRouterEnv(t, func(d *Dependencies) {
		d.AuditLogStore = log
		d.FeedStore = feeds
		d.SubscriptionStore = feeds.subs
		d.CategoryStore = &fakeCategoryStore{}
	})

	rec := env.want(env.do(http.MethodPost, "/v1/feeds", env.editorKey, `{"feed_url":"https://example.com/a.xml","title":"A"}`), http.StatusCreated)
	feed, _ := decodeData[feedDTO](t, rec)

	// Reader: catalog shows the feed unsubscribed, subscribe works once.
	rec = env.want(env.do(http.MethodGet, "/v1/catalog", env.bobKey, ""), http.StatusOK)
	catalog, total := decodeData[[]catalogFeedDTO](t, rec)
	if total != 1 || len(catalog) != 1 || catalog[0].ID != feed.ID || catalog[0].Subscribed || catalog[0].SubscriberCount != 1 {
		t.Fatalf("catalog = %+v total=%d", catalog, total)
	}
	env.want(env.do(http.MethodPost, "/v1/subscriptions", env.bobKey, `{"feed_id":`+itoa(feed.ID)+`}`), http.StatusCreated)
	env.want(env.do(http.MethodPost, "/v1/subscriptions", env.bobKey, `{"feed_id":`+itoa(feed.ID)+`}`), http.StatusConflict)
	env.want(env.do(http.MethodPost, "/v1/subscriptions", env.bobKey, `{"feed_id":999}`), http.StatusNotFound)
	env.want(env.do(http.MethodPost, "/v1/subscriptions", env.bobKey, `{}`), http.StatusBadRequest)
	rec = env.want(env.do(http.MethodGet, "/v1/catalog?subscribed=true", env.bobKey, ""), http.StatusOK)
	if catalog, _ = decodeData[[]catalogFeedDTO](t, rec); len(catalog) != 1 || !catalog[0].Subscribed || catalog[0].SubscriberCount != 2 {
		t.Fatalf("catalog after subscribe = %+v", catalog)
	}
	env.want(env.do(http.MethodGet, "/v1/catalog?subscribed=maybe", env.bobKey, ""), http.StatusBadRequest)
	rec = env.want(env.do(http.MethodGet, "/v1/subscriptions", env.bobKey, ""), http.StatusOK)
	if subs, _ := decodeData[[]subscriptionDTO](t, rec); len(subs) != 1 || subs[0].FeedID != feed.ID {
		t.Fatalf("subscriptions = %+v", subs)
	}
	env.want(env.do(http.MethodPut, "/v1/subscriptions/"+itoa(feed.ID), env.bobKey, `{"category_id":null}`), http.StatusOK)
	env.want(env.do(http.MethodPut, "/v1/subscriptions/999", env.bobKey, `{}`), http.StatusNotFound)

	// Admin posting the same URL is subscribed (200), not refused (409).
	rec = env.want(env.do(http.MethodPost, "/v1/feeds", env.adminKey, `{"feed_url":"https://example.com/a.xml"}`), http.StatusOK)
	if again, _ := decodeData[feedDTO](t, rec); again.ID != feed.ID {
		t.Fatalf("subscribe-by-url returned feed %d, want %d", again.ID, feed.ID)
	}
	env.want(env.do(http.MethodPost, "/v1/feeds", env.adminKey, `{"feed_url":"https://example.com/a.xml"}`), http.StatusConflict)

	env.want(env.do(http.MethodDelete, "/v1/subscriptions/"+itoa(feed.ID), env.bobKey, ""), http.StatusOK)
	env.want(env.do(http.MethodDelete, "/v1/subscriptions/"+itoa(feed.ID), env.bobKey, ""), http.StatusNotFound)
	rec = env.want(env.do(http.MethodGet, "/v1/catalog?subscribed=false", env.bobKey, ""), http.StatusOK)
	if _, total = decodeData[[]catalogFeedDTO](t, rec); total != 1 {
		t.Fatalf("unsubscribed catalog total = %d", total)
	}

	got := make([]string, 0, len(log.rows))
	for _, ev := range log.rows {
		got = append(got, ev.Action)
	}
	if strings.Join(got, ",") != "feed.create,subscription.create,subscription.create,subscription.delete" {
		t.Fatalf("actions = %v", got)
	}
	if ev := log.rows[1]; ev.ActorID != env.bobID || ev.TargetType != "feed" || ev.TargetID != feed.ID {
		t.Fatalf("subscription.create = %+v", ev)
	}
	if ev := log.rows[2]; ev.ActorID != 1 || ev.Details["url"] != "https://example.com/a.xml" {
		t.Fatalf("subscription.create via POST /v1/feeds = %+v", ev)
	}
}

// OPML import of a URL that is already in the catalog subscribes the
// importer instead of skipping the line.
func TestRouter_OPMLImportSubscribesToCatalogFeed(t *testing.T) {
	feeds := newCatalogFeedStore()
	env := newRouterEnv(t, func(d *Dependencies) {
		d.FeedStore = feeds
		d.SubscriptionStore = feeds.subs
		d.CategoryStore = &fakeCategoryStore{}
		d.MaxImportFeeds = 10
	})
	env.want(env.do(http.MethodPost, "/v1/feeds", env.editorKey, `{"feed_url":"https://example.com/a.xml","title":"A"}`), http.StatusCreated)

	opml := `<?xml version="1.0"?><opml version="2.0"><body><outline type="rss" xmlUrl="https://example.com/a.xml" text="A"/><outline type="rss" xmlUrl="https://example.com/b.xml" text="B"/></body></opml>`
	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/import", strings.NewReader(opml))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("X-Auth-Token", env.adminKey)
	rec := httptest.NewRecorder()
	env.h.ServeHTTP(rec, req)
	report, _ := decodeData[importReportDTO](t, env.want(rec, http.StatusOK))
	if report.FeedsCreated != 1 || report.FeedsSubscribed != 1 || report.FeedsSkipped != 0 || len(report.Errors) != 0 {
		t.Fatalf("report = %+v", report)
	}
	rec = env.want(env.do(http.MethodGet, "/v1/catalog?subscribed=true", env.adminKey, ""), http.StatusOK)
	if _, total := decodeData[[]catalogFeedDTO](t, rec); total != 2 {
		t.Fatalf("admin subscribed to %d feeds, want 2", total)
	}
}
