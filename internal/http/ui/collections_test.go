package ui

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"rssam/internal/audit"
	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemCollections struct {
	cols      map[int64]*storage.Collection
	feeds     map[int64][]int64
	followers map[int64]map[int64]*int64
	calls     []string
	catalog   *uiMemSubscriptions
}

func newUIMemCollections(catalog *uiMemSubscriptions) *uiMemCollections {
	return &uiMemCollections{cols: map[int64]*storage.Collection{}, feeds: map[int64][]int64{}, followers: map[int64]map[int64]*int64{}, catalog: catalog}
}

func (m *uiMemCollections) view(userID int64, c *storage.Collection) storage.Collection {
	out := *c
	out.FeedCount = len(m.feeds[c.ID])
	out.FollowerCount = len(m.followers[c.ID])
	if cat, ok := m.followers[c.ID][userID]; ok {
		out.Followed, out.CategoryID = true, cat
	}
	return out
}

func (m *uiMemCollections) CreateCollection(_ context.Context, ownerID int64, p storage.CollectionParams) (storage.Collection, error) {
	id := int64(len(m.cols) + 1)
	m.cols[id] = &storage.Collection{ID: id, OwnerID: ownerID, OwnerName: "eve", Title: p.Title, Description: p.Description}
	m.followers[id] = map[int64]*int64{}
	m.calls = append(m.calls, "create")
	return m.view(ownerID, m.cols[id]), nil
}

func (m *uiMemCollections) UpdateCollection(_ context.Context, id int64, p storage.CollectionParams) error {
	c, ok := m.cols[id]
	if !ok {
		return storage.ErrNotFound
	}
	c.Title, c.Description = p.Title, p.Description
	m.calls = append(m.calls, "update")
	return nil
}

func (m *uiMemCollections) DeleteCollection(_ context.Context, id int64) error {
	if _, ok := m.cols[id]; !ok {
		return storage.ErrNotFound
	}
	delete(m.cols, id)
	m.calls = append(m.calls, "delete")
	return nil
}

func (m *uiMemCollections) GetCollection(_ context.Context, userID, id int64) (storage.Collection, error) {
	c, ok := m.cols[id]
	if !ok {
		return storage.Collection{}, storage.ErrNotFound
	}
	return m.view(userID, c), nil
}

func (m *uiMemCollections) ListCollections(_ context.Context, userID int64) ([]storage.Collection, error) {
	out := make([]storage.Collection, 0, len(m.cols))
	for _, c := range m.cols {
		out = append(out, m.view(userID, c))
	}
	slices.SortFunc(out, func(a, b storage.Collection) int { return int(a.ID - b.ID) })
	return out, nil
}

func (m *uiMemCollections) ListCollectionFeeds(ctx context.Context, userID, id int64) ([]storage.CatalogFeed, error) {
	all, _, _ := m.catalog.ListCatalog(ctx, userID, storage.CatalogFilter{})
	var out []storage.CatalogFeed
	for _, f := range all {
		if slices.Contains(m.feeds[id], f.ID) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (m *uiMemCollections) AddCollectionFeeds(_ context.Context, id int64, feedIDs []int64) (int, error) {
	if _, ok := m.cols[id]; !ok {
		return 0, storage.ErrNotFound
	}
	added := 0
	for _, fid := range feedIDs {
		found := false
		for _, f := range m.catalog.feeds.feeds {
			found = found || f.ID == fid
		}
		if !found {
			return 0, storage.ErrInvalidReference
		}
		if !slices.Contains(m.feeds[id], fid) {
			m.feeds[id] = append(m.feeds[id], fid)
			added++
		}
	}
	m.calls = append(m.calls, "add")
	return added, nil
}

func (m *uiMemCollections) RemoveCollectionFeed(_ context.Context, id, feedID int64) error {
	if !slices.Contains(m.feeds[id], feedID) {
		return storage.ErrNotFound
	}
	m.feeds[id] = slices.DeleteFunc(m.feeds[id], func(f int64) bool { return f == feedID })
	m.calls = append(m.calls, "remove")
	return nil
}

func (m *uiMemCollections) FollowCollection(ctx context.Context, userID, id int64, categoryID *int64) (storage.FollowResult, error) {
	if _, ok := m.cols[id]; !ok {
		return storage.FollowResult{}, storage.ErrNotFound
	}
	if _, dup := m.followers[id][userID]; dup {
		return storage.FollowResult{}, storage.ErrAlreadyFollowing
	}
	if categoryID != nil && *categoryID != 10 {
		return storage.FollowResult{}, storage.ErrInvalidReference
	}
	m.followers[id][userID] = categoryID
	n := 0
	for _, fid := range m.feeds[id] {
		if _, err := m.catalog.Subscribe(ctx, userID, fid, storage.SubscriptionParams{CategoryID: categoryID}); err == nil {
			n++
		}
	}
	m.calls = append(m.calls, "follow")
	return storage.FollowResult{Subscribed: n, CategoryID: 10}, nil
}

func (m *uiMemCollections) UnfollowCollection(ctx context.Context, userID, id int64) (int, error) {
	if _, ok := m.followers[id][userID]; !ok {
		return 0, storage.ErrNotFound
	}
	delete(m.followers[id], userID)
	n := 0
	for _, fid := range m.feeds[id] {
		if err := m.catalog.Unsubscribe(ctx, userID, fid); err == nil {
			n++
		}
	}
	m.calls = append(m.calls, "unfollow")
	return n, nil
}

func (m *uiMemCollections) CountCollections(context.Context) (int, error) { return len(m.cols), nil }

func newCollectionsUI(t *testing.T, role string, cols *uiMemCollections, auditStore *memAuditStore) (http.Handler, string, string) {
	t.Helper()
	h, err := NewHandler(Config{
		Users:         &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: role}},
		Sessions:      &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:       uiMemEntries{},
		Feeds:         cols.catalog.feeds,
		Subscriptions: cols.catalog,
		Collections:   cols,
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
	return mux, sid, auth.CSRFToken("csrf-test", sid)
}

// A reader sees the collection list and page, follows into an own
// category (foreign → 400), lands on the page with the count, unfollows;
// editor-only forms and routes are hidden/forbidden for them.
func TestUI_CollectionsReaderFollowUnfollow(t *testing.T) {
	feeds := &uiMemFeeds{feeds: []storage.Feed{
		{ID: 1, Title: "Shared News", FeedURL: "https://example.com/news.xml"},
		{ID: 2, Title: "Other", FeedURL: "https://example.com/other.xml"},
	}}
	subs := &uiMemSubscriptions{feeds: feeds, subscribed: map[int64]bool{}}
	cols := newUIMemCollections(subs)
	cols.cols[1] = &storage.Collection{ID: 1, OwnerID: 5, OwnerName: "eve", Title: "Новости", Description: "Главное"}
	cols.followers[1] = map[int64]*int64{}
	cols.feeds[1] = []int64{1, 2}
	auditStore := &memAuditStore{}
	mux, sid, token := newCollectionsUI(t, auth.RoleReader, cols, auditStore)

	body := getPage(t, mux, sid, "/ui/collections").Body.String()
	for _, want := range []string{"Новости", "Главное", "/ui/collections/1/follow", `<option value="">Категория «Новости»</option>`, `<option value="10">News</option>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("list missing %q", want)
		}
	}
	if strings.Contains(body, `action="/ui/collections" class="card"`) {
		t.Fatal("reader must not see the create form")
	}
	body = getPage(t, mux, sid, "/ui/collections/1").Body.String()
	if !strings.Contains(body, "Shared News") || !strings.Contains(body, "Лент: 2") || strings.Contains(body, "/feeds/1/remove") || strings.Contains(body, "Удалить подборку") {
		t.Fatalf("collection page for reader: %s", body)
	}
	if rec := getPage(t, mux, sid, "/ui/collections/77"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown collection: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections", url.Values{"csrf_token": {token}, "title": {"x"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("reader create: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/1/feeds", url.Values{"csrf_token": {token}, "feed_id": {"1"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("reader add feeds: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/1/follow", url.Values{}); rec.Code != http.StatusForbidden {
		t.Fatalf("follow without csrf: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/1/follow", url.Values{"csrf_token": {token}, "category_id": {"99"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign category: %d", rec.Code)
	}
	rec := postForm(t, mux, sid, "/ui/collections/1/follow", url.Values{"csrf_token": {token}, "category_id": {"10"}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/collections/1?followed=2" {
		t.Fatalf("follow: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := postForm(t, mux, sid, "/ui/collections/1/follow", url.Values{"csrf_token": {token}}); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/collections/1" {
		t.Fatalf("repeat follow: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	body = getPage(t, mux, sid, "/ui/collections/1?followed=2").Body.String()
	if !strings.Contains(body, "новых лент: 2") || !strings.Contains(body, "/ui/collections/1/unfollow") || !strings.Contains(body, "Вы подписаны") || !strings.Contains(body, `href="/ui/feeds/1"`) {
		t.Fatalf("page after follow: %s", body)
	}
	rec = postForm(t, mux, sid, "/ui/collections/1/unfollow", url.Values{"csrf_token": {token}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/collections?unfollowed=2" {
		t.Fatalf("unfollow: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := postForm(t, mux, sid, "/ui/collections/1/unfollow", url.Values{"csrf_token": {token}}); rec.Code != http.StatusNotFound {
		t.Fatalf("repeat unfollow: %d", rec.Code)
	}
	if strings.Join(cols.calls, ",") != "follow,unfollow" || len(subs.subscribed) != 0 {
		t.Fatalf("calls=%v subscribed=%v", cols.calls, subs.subscribed)
	}
	if len(auditStore.rows) != 2 || auditStore.rows[0].Action != storage.AuditSubscriptionCreate || auditStore.rows[0].TargetType != "collection" || auditStore.rows[1].Action != storage.AuditSubscriptionDelete {
		t.Fatalf("audit = %+v", auditStore.rows)
	}
}

// An editor creates a collection, adds and removes feeds, renames and
// deletes it; a collection of another editor is read-only for them.
func TestUI_CollectionsEditorCurates(t *testing.T) {
	feeds := &uiMemFeeds{feeds: []storage.Feed{
		{ID: 1, Title: "Shared News", FeedURL: "https://example.com/news.xml"},
		{ID: 2, Title: "Other", FeedURL: "https://example.com/other.xml"},
	}}
	subs := &uiMemSubscriptions{feeds: feeds, subscribed: map[int64]bool{}}
	cols := newUIMemCollections(subs)
	cols.cols[1] = &storage.Collection{ID: 1, OwnerID: 5, OwnerName: "eve", Title: "Чужая"}
	cols.followers[1] = map[int64]*int64{}
	cols.feeds[1] = []int64{2}
	auditStore := &memAuditStore{}
	mux, sid, token := newCollectionsUI(t, auth.RoleEditor, cols, auditStore)

	if body := getPage(t, mux, sid, "/ui/collections").Body.String(); !strings.Contains(body, `action="/ui/collections" class="card"`) {
		t.Fatal("editor must see the create form")
	}
	if body := getPage(t, mux, sid, "/ui/collections/1").Body.String(); strings.Contains(body, "/feeds/2/remove") || strings.Contains(body, "Удалить подборку") {
		t.Fatal("foreign collection must be read-only")
	}
	if rec := postForm(t, mux, sid, "/ui/collections/1", url.Values{"csrf_token": {token}, "title": {"x"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("edit foreign: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/1/delete", url.Values{"csrf_token": {token}}); rec.Code != http.StatusForbidden {
		t.Fatalf("delete foreign: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections", url.Values{"csrf_token": {token}, "title": {"  "}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty title: %d", rec.Code)
	}
	rec := postForm(t, mux, sid, "/ui/collections", url.Values{"csrf_token": {token}, "title": {" Моя "}, "description": {"d"}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/collections/2" {
		t.Fatalf("create: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	body := getPage(t, mux, sid, "/ui/collections/2").Body.String()
	if !strings.Contains(body, `value="Моя"`) || !strings.Contains(body, `<option value="1">Shared News — RSS/Atom</option>`) || !strings.Contains(body, "Удалить подборку") {
		t.Fatalf("own collection page: %s", body)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/2/feeds", url.Values{"csrf_token": {token}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("add nothing: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/2/feeds", url.Values{"csrf_token": {token}, "feed_id": {"77"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("add unknown feed: %d", rec.Code)
	}
	rec = postForm(t, mux, sid, "/ui/collections/2/feeds", url.Values{"csrf_token": {token}, "feed_id": {"1", "2"}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/collections/2?added=2" {
		t.Fatalf("add feeds: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	body = getPage(t, mux, sid, "/ui/collections/2?added=2").Body.String()
	if !strings.Contains(body, "Лент добавлено: 2") || !strings.Contains(body, "/ui/collections/2/feeds/1/remove") || !strings.Contains(body, "Все ленты каталога уже в подборке") {
		t.Fatalf("page after add: %s", body)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/2/feeds/1/remove", url.Values{"csrf_token": {token}}); rec.Code != http.StatusFound {
		t.Fatalf("remove: %d", rec.Code)
	}
	if rec := postForm(t, mux, sid, "/ui/collections/2/feeds/1/remove", url.Values{"csrf_token": {token}}); rec.Code != http.StatusNotFound {
		t.Fatalf("remove twice: %d", rec.Code)
	}
	rec = postForm(t, mux, sid, "/ui/collections/2", url.Values{"csrf_token": {token}, "title": {"Моя 2"}, "description": {""}})
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/collections/2?saved=1" {
		t.Fatalf("update: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := postForm(t, mux, sid, "/ui/collections/2/delete", url.Values{"csrf_token": {token}}); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/ui/collections" {
		t.Fatalf("delete: %d", rec.Code)
	}
	if strings.Join(cols.calls, ",") != "create,add,remove,update,delete" {
		t.Fatalf("calls = %v", cols.calls)
	}
	got := make([]string, 0, len(auditStore.rows))
	for _, row := range auditStore.rows {
		got = append(got, row.Action)
	}
	if strings.Join(got, ",") != "collection.create,collection.update,collection.update,collection.update,collection.delete" {
		t.Fatalf("audit = %v", got)
	}
}
