package httpserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

// memCollectionStore keeps the follow bookkeeping of the real store (which
// subscriptions a follow created) on top of memSubscriptionStore.
type memCollectionStore struct {
	mu        sync.Mutex
	subs      *memSubscriptionStore
	nextID    int64
	cols      map[int64]*memCollection
	followers map[int64]map[int64]*int64 // collection -> user -> category
	via       map[int64]map[int64]int64  // user -> feed -> collection
}

type memCollection struct {
	storage.Collection
	feeds []int64
}

func newMemCollectionStore(subs *memSubscriptionStore) *memCollectionStore {
	return &memCollectionStore{subs: subs, cols: map[int64]*memCollection{}, followers: map[int64]map[int64]*int64{}, via: map[int64]map[int64]int64{}}
}

func (m *memCollectionStore) CreateCollection(ctx context.Context, ownerID int64, p storage.CollectionParams) (storage.Collection, error) {
	m.mu.Lock()
	m.nextID++
	id := m.nextID
	m.cols[id] = &memCollection{Collection: storage.Collection{ID: id, OwnerID: ownerID, OwnerName: "owner", Title: p.Title, Description: p.Description, CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	m.followers[id] = map[int64]*int64{}
	m.mu.Unlock()
	return m.GetCollection(ctx, ownerID, id)
}

func (m *memCollectionStore) UpdateCollection(_ context.Context, id int64, p storage.CollectionParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[id]
	if !ok {
		return storage.ErrNotFound
	}
	c.Title, c.Description = p.Title, p.Description
	return nil
}

func (m *memCollectionStore) DeleteCollection(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cols[id]; !ok {
		return storage.ErrNotFound
	}
	delete(m.cols, id)
	delete(m.followers, id)
	for _, feeds := range m.via {
		for f, cid := range feeds {
			if cid == id {
				delete(feeds, f)
			}
		}
	}
	return nil
}

func (m *memCollectionStore) view(userID int64, c *memCollection) storage.Collection {
	out := c.Collection
	out.FeedCount = len(c.feeds)
	out.FollowerCount = len(m.followers[c.ID])
	if cat, ok := m.followers[c.ID][userID]; ok {
		out.Followed, out.CategoryID = true, cat
	}
	return out
}

func (m *memCollectionStore) GetCollection(_ context.Context, userID, id int64) (storage.Collection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[id]
	if !ok {
		return storage.Collection{}, storage.ErrNotFound
	}
	return m.view(userID, c), nil
}

func (m *memCollectionStore) ListCollections(_ context.Context, userID int64) ([]storage.Collection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]storage.Collection, 0, len(m.cols))
	for _, c := range m.cols {
		out = append(out, m.view(userID, c))
	}
	slices.SortFunc(out, func(a, b storage.Collection) int { return int(a.ID - b.ID) })
	return out, nil
}

func (m *memCollectionStore) ListCollectionFeeds(ctx context.Context, userID, id int64) ([]storage.CatalogFeed, error) {
	m.mu.Lock()
	c, ok := m.cols[id]
	if !ok {
		m.mu.Unlock()
		return nil, storage.ErrNotFound
	}
	ids := slices.Clone(c.feeds)
	m.mu.Unlock()
	all, _, err := m.subs.ListCatalog(ctx, userID, storage.CatalogFilter{Limit: 500})
	if err != nil {
		return nil, err
	}
	out := make([]storage.CatalogFeed, 0, len(ids))
	for _, f := range all {
		if slices.Contains(ids, f.ID) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *memCollectionStore) subscribeVia(ctx context.Context, userID, feedID, collectionID int64, cat *int64) (bool, error) {
	_, err := m.subs.Subscribe(ctx, userID, feedID, storage.SubscriptionParams{CategoryID: cat})
	if err != nil {
		if errors.Is(err, storage.ErrAlreadySubscribed) {
			return false, nil
		}
		return false, err
	}
	if m.via[userID] == nil {
		m.via[userID] = map[int64]int64{}
	}
	m.via[userID][feedID] = collectionID
	return true, nil
}

func (m *memCollectionStore) AddCollectionFeeds(ctx context.Context, id int64, feedIDs []int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[id]
	if !ok {
		return 0, storage.ErrNotFound
	}
	added := 0
	for _, fid := range feedIDs {
		if _, err := m.subs.feeds.GetFeedByID(ctx, fid); err != nil {
			return 0, storage.ErrInvalidReference
		}
		if slices.Contains(c.feeds, fid) {
			continue
		}
		c.feeds = append(c.feeds, fid)
		added++
		for uid, cat := range m.followers[id] {
			if _, err := m.subscribeVia(ctx, uid, fid, id, cat); err != nil {
				return 0, err
			}
		}
	}
	return added, nil
}

func (m *memCollectionStore) RemoveCollectionFeed(ctx context.Context, id, feedID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[id]
	if !ok || !slices.Contains(c.feeds, feedID) {
		return storage.ErrNotFound
	}
	c.feeds = slices.DeleteFunc(c.feeds, func(f int64) bool { return f == feedID })
	for uid, feeds := range m.via {
		if feeds[feedID] == id {
			delete(feeds, feedID)
			_ = m.subs.Unsubscribe(ctx, uid, feedID)
		}
	}
	return nil
}

func (m *memCollectionStore) FollowCollection(ctx context.Context, userID, id int64, categoryID *int64) (storage.FollowResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cols[id]
	if !ok {
		return storage.FollowResult{}, storage.ErrNotFound
	}
	if _, dup := m.followers[id][userID]; dup {
		return storage.FollowResult{}, storage.ErrAlreadyFollowing
	}
	if categoryID == nil {
		categoryID = ptrTo[int64](77)
	}
	m.followers[id][userID] = categoryID
	res := storage.FollowResult{CategoryID: *categoryID}
	for _, fid := range c.feeds {
		added, err := m.subscribeVia(ctx, userID, fid, id, categoryID)
		if err != nil {
			return storage.FollowResult{}, err
		}
		if added {
			res.Subscribed++
		}
	}
	return res, nil
}

func (m *memCollectionStore) UnfollowCollection(ctx context.Context, userID, id int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.followers[id][userID]; !ok {
		return 0, storage.ErrNotFound
	}
	delete(m.followers[id], userID)
	n := 0
	for fid, cid := range m.via[userID] {
		if cid == id {
			delete(m.via[userID], fid)
			_ = m.subs.Unsubscribe(ctx, userID, fid)
			n++
		}
	}
	return n, nil
}

func (m *memCollectionStore) CountCollections(context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.cols), nil
}

// An editor curates a collection, a reader follows it and gets every feed
// (plus feeds added later), unfollowing removes only what the follow
// created, and ownership gates edits: another editor gets 403, the admin
// passes.
func TestRouter_CollectionsLifecycle(t *testing.T) {
	log := &memAuditLog{}
	feeds := newCatalogFeedStore()
	cols := newMemCollectionStore(feeds.subs)
	env := newRouterEnv(t, func(d *Dependencies) {
		d.AuditLogStore = log
		d.FeedStore = feeds
		d.SubscriptionStore = feeds.subs
		d.CollectionStore = cols
		d.CategoryStore = &fakeCategoryStore{}
	})
	mkFeed := func(url string) int64 {
		rec := env.want(env.do(http.MethodPost, "/v1/feeds", env.adminKey, `{"feed_url":"`+url+`"}`), http.StatusCreated)
		f, _ := decodeData[feedDTO](t, rec)
		return f.ID
	}
	f1, f2, f3 := mkFeed("https://example.com/1.xml"), mkFeed("https://example.com/2.xml"), mkFeed("https://example.com/3.xml")

	env.want(env.do(http.MethodPost, "/v1/collections", env.bobKey, `{"title":"x"}`), http.StatusForbidden)
	env.want(env.do(http.MethodPost, "/v1/collections", env.editorKey, `{"title":"  "}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPost, "/v1/collections", env.editorKey, `{"title":"`+strings.Repeat("я", 201)+`"}`), http.StatusBadRequest)
	rec := env.want(env.do(http.MethodPost, "/v1/collections", env.editorKey, `{"title":" Новости ","description":"d"}`), http.StatusCreated)
	col, _ := decodeData[collectionDTO](t, rec)
	if col.Title != "Новости" || col.OwnerID != env.editorID || col.FeedCount != 0 || col.Followed {
		t.Fatalf("created = %+v", col)
	}
	cid := itoa(col.ID)

	env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/feeds", env.editorKey, `{"feed_ids":[]}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/feeds", env.editorKey, `{"feed_ids":[0]}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/feeds", env.editorKey, `{"feed_ids":[999]}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPost, "/v1/collections/999/feeds", env.editorKey, `{"feed_ids":[1]}`), http.StatusNotFound)
	rec = env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/feeds", env.editorKey, `{"feed_ids":[`+itoa(f1)+`,`+itoa(f2)+`,`+itoa(f1)+`]}`), http.StatusOK)
	if added, _ := decodeData[collectionFeedsAddedDTO](t, rec); added.Added != 2 {
		t.Fatalf("added = %+v", added)
	}

	// Reader: list, detail, follow.
	rec = env.want(env.do(http.MethodGet, "/v1/collections", env.bobKey, ""), http.StatusOK)
	list, total := decodeData[[]collectionDTO](t, rec)
	if total != 1 || len(list) != 1 || list[0].FeedCount != 2 || list[0].FollowerCount != 0 || list[0].Followed {
		t.Fatalf("list = %+v", list)
	}
	env.want(env.do(http.MethodGet, "/v1/collections/999", env.bobKey, ""), http.StatusNotFound)
	rec = env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/follow", env.bobKey, ""), http.StatusCreated)
	if res, _ := decodeData[collectionFollowDTO](t, rec); res.Subscribed != 2 || res.CategoryID != 77 {
		t.Fatalf("follow = %+v", res)
	}
	env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/follow", env.bobKey, `{}`), http.StatusConflict)
	env.want(env.do(http.MethodPost, "/v1/collections/999/follow", env.bobKey, ""), http.StatusNotFound)
	rec = env.want(env.do(http.MethodGet, "/v1/collections/"+cid, env.bobKey, ""), http.StatusOK)
	detail, _ := decodeData[collectionDetailDTO](t, rec)
	if !detail.Followed || detail.FollowerCount != 1 || len(detail.Feeds) != 2 || !detail.Feeds[0].Subscribed || detail.CategoryID == nil || *detail.CategoryID != 77 {
		t.Fatalf("detail = %+v", detail)
	}
	rec = env.want(env.do(http.MethodGet, "/v1/subscriptions", env.bobKey, ""), http.StatusOK)
	if subs, _ := decodeData[[]subscriptionDTO](t, rec); len(subs) != 2 {
		t.Fatalf("bob subscriptions = %+v", subs)
	}

	// Feeds added later reach the follower; removal takes them away.
	env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/feeds", env.editorKey, `{"feed_ids":[`+itoa(f3)+`]}`), http.StatusOK)
	rec = env.want(env.do(http.MethodGet, "/v1/subscriptions", env.bobKey, ""), http.StatusOK)
	if subs, _ := decodeData[[]subscriptionDTO](t, rec); len(subs) != 3 {
		t.Fatalf("bob subscriptions after add = %+v", subs)
	}
	env.want(env.do(http.MethodDelete, "/v1/collections/"+cid+"/feeds/"+itoa(f3), env.editorKey, ""), http.StatusOK)
	env.want(env.do(http.MethodDelete, "/v1/collections/"+cid+"/feeds/"+itoa(f3), env.editorKey, ""), http.StatusNotFound)
	rec = env.want(env.do(http.MethodGet, "/v1/subscriptions", env.bobKey, ""), http.StatusOK)
	if subs, _ := decodeData[[]subscriptionDTO](t, rec); len(subs) != 2 {
		t.Fatalf("bob subscriptions after remove = %+v", subs)
	}

	// Ownership: the admin may edit, another editor may not, readers never.
	env.want(env.do(http.MethodPut, "/v1/collections/"+cid, env.bobKey, `{"title":"n"}`), http.StatusForbidden)
	eve2, err := env.users.CreateUser(context.Background(), storage.CreateUserParams{Username: "eve2", Role: auth.RoleEditor})
	if err != nil {
		t.Fatal(err)
	}
	otherEditor := env.apiKey(eve2.ID)
	env.want(env.do(http.MethodPut, "/v1/collections/"+cid, otherEditor, `{"title":"n"}`), http.StatusForbidden)
	env.want(env.do(http.MethodDelete, "/v1/collections/"+cid, otherEditor, ""), http.StatusForbidden)
	env.want(env.do(http.MethodPost, "/v1/collections/"+cid+"/feeds", otherEditor, `{"feed_ids":[`+itoa(f3)+`]}`), http.StatusForbidden)
	env.want(env.do(http.MethodPut, "/v1/collections/999", env.adminKey, `{"title":"n"}`), http.StatusNotFound)
	rec = env.want(env.do(http.MethodPut, "/v1/collections/"+cid, env.adminKey, `{"title":"Новости 2","description":""}`), http.StatusOK)
	if upd, _ := decodeData[collectionDTO](t, rec); upd.Title != "Новости 2" || upd.Followed {
		t.Fatalf("updated = %+v", upd)
	}

	rec = env.want(env.do(http.MethodDelete, "/v1/collections/"+cid+"/follow", env.bobKey, ""), http.StatusOK)
	if res, _ := decodeData[collectionUnfollowDTO](t, rec); res.Unsubscribed != 2 {
		t.Fatalf("unfollow = %+v", res)
	}
	env.want(env.do(http.MethodDelete, "/v1/collections/"+cid+"/follow", env.bobKey, ""), http.StatusNotFound)
	rec = env.want(env.do(http.MethodGet, "/v1/subscriptions", env.bobKey, ""), http.StatusOK)
	if subs, _ := decodeData[[]subscriptionDTO](t, rec); len(subs) != 0 {
		t.Fatalf("bob subscriptions after unfollow = %+v", subs)
	}
	env.want(env.do(http.MethodDelete, "/v1/collections/"+cid, env.editorKey, ""), http.StatusOK)
	env.want(env.do(http.MethodDelete, "/v1/collections/"+cid, env.editorKey, ""), http.StatusNotFound)

	got := make([]string, 0, len(log.rows))
	for _, ev := range log.rows {
		if strings.HasPrefix(ev.Action, "collection.") || ev.TargetType == "collection" {
			got = append(got, ev.Action)
		}
	}
	if strings.Join(got, ",") != "collection.create,collection.update,subscription.create,collection.update,collection.update,collection.update,subscription.delete,collection.delete" {
		t.Fatalf("actions = %v", got)
	}
}
