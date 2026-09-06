package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/filter"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

// These tests exercise the handlers that previously had no coverage. They go
// through Server.Handler(), i.e. the real router, wrapAPI auth and the
// middleware chain, so route patterns and path-value names are verified too.

// ---- fakes -----------------------------------------------------------------

// tenantEntryStore keeps entries per (user, feed) so cross-tenant isolation
// can be asserted for the entry handlers.
type tenantEntryStore struct {
	noopEntryStore
	mu      sync.Mutex
	entries map[int64]storage.Entry // id -> entry
	owner   map[int64]int64         // entry id -> user id
	encs    map[int64][]storage.Enclosure
}

func newTenantEntryStore() *tenantEntryStore {
	return &tenantEntryStore{
		entries: map[int64]storage.Entry{},
		owner:   map[int64]int64{},
		encs:    map[int64][]storage.Enclosure{},
	}
}

func (s *tenantEntryStore) add(userID int64, e storage.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[e.ID] = e
	s.owner[e.ID] = userID
}

func (s *tenantEntryStore) GetEntry(_ context.Context, userID, id int64) (storage.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok || s.owner[id] != userID {
		return storage.Entry{}, storage.ErrNotFound
	}
	return e, nil
}

func (s *tenantEntryStore) GetFeedEntry(ctx context.Context, userID, feedID, entryID int64) (storage.Entry, error) {
	e, err := s.GetEntry(ctx, userID, entryID)
	if err != nil || e.FeedID != feedID {
		return storage.Entry{}, storage.ErrNotFound
	}
	return e, nil
}

func (s *tenantEntryStore) UpdateEntry(ctx context.Context, userID, feedID, entryID int64, p storage.UpdateEntryParams) (storage.Entry, error) {
	e, err := s.GetFeedEntry(ctx, userID, feedID, entryID)
	if err != nil {
		return storage.Entry{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Status != nil {
		e.Status = *p.Status
	}
	if p.Starred != nil {
		e.Starred = *p.Starred
	}
	s.entries[entryID] = e
	return e, nil
}

func (s *tenantEntryStore) ListFeedEntries(_ context.Context, userID, feedID int64, f storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.Entry
	for id, e := range s.entries {
		if s.owner[id] != userID || e.FeedID != feedID {
			continue
		}
		if f.Status != nil && e.Status != *f.Status {
			continue
		}
		out = append(out, e)
	}
	return out, len(out), nil
}

func (s *tenantEntryStore) ListEnclosuresByEntryIDs(_ context.Context, _ int64, ids []int64) (map[int64][]storage.Enclosure, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int64][]storage.Enclosure{}
	for _, id := range ids {
		if v, ok := s.encs[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

// iconFeedStore serves one feed with optional cached icon data.
type iconFeedStore struct {
	noopFeedStore
	mu     sync.Mutex
	feed   storage.Feed
	cached []byte
	cURL   string
}

func (f *iconFeedStore) GetFeed(_ context.Context, userID, id int64) (storage.Feed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.feed.ID || userID != 1 {
		return storage.Feed{}, storage.ErrNotFound
	}
	return f.feed, nil
}

func (f *iconFeedStore) UpdateFeedIcon(_ context.Context, _, _ int64, iconURL string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cached = data
	f.cURL = iconURL
	return nil
}

// memMatchStore returns canned filter matches.
type memMatchStore struct {
	noopMatchStore
	rows map[int64][]storage.FilterMatchWithEntry
}

func (m *memMatchStore) ListFilterMatches(_ context.Context, filterID int64, _, _ int) ([]storage.FilterMatchWithEntry, int, error) {
	rows := m.rows[filterID]
	return rows, len(rows), nil
}

// memWebhookLogStore is the first in-memory WebhookLogStore for the http
// package; it enforces the same ownership rule as the Postgres implementation
// (retry only rows whose webhook belongs to the caller).
type memWebhookLogStore struct {
	mu     sync.Mutex
	logs   map[int64]storage.WebhookLog
	owners map[int64]int64 // webhook id -> user id
}

func newMemWebhookLogStore() *memWebhookLogStore {
	return &memWebhookLogStore{logs: map[int64]storage.WebhookLog{}, owners: map[int64]int64{}}
}

func (m *memWebhookLogStore) EnqueueWebhookLogs(context.Context, []int64, int64) error { return nil }
func (m *memWebhookLogStore) ClaimDueWebhookLogs(context.Context, int) ([]storage.WebhookLog, error) {
	return nil, nil
}
func (m *memWebhookLogStore) MarkWebhookLogSent(context.Context, int64, int, int, string) error {
	return nil
}
func (m *memWebhookLogStore) MarkWebhookLogFailed(context.Context, int64, *int, string, string, int, *time.Time, bool) error {
	return nil
}
func (m *memWebhookLogStore) ListWebhookLogs(_ context.Context, webhookID int64, _, _ int) ([]storage.WebhookLog, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.WebhookLog
	for _, l := range m.logs {
		if l.WebhookID == webhookID {
			out = append(out, l)
		}
	}
	return out, len(out), nil
}
func (m *memWebhookLogStore) RetryWebhookLogNow(_ context.Context, userID, logID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.logs[logID]
	if !ok || m.owners[l.WebhookID] != userID || l.Status == "sent" {
		return storage.ErrNotFound
	}
	l.Status = "pending"
	m.logs[logID] = l
	return nil
}

// ---- helpers ---------------------------------------------------------------

type routerEnv struct {
	t        *testing.T
	h        http.Handler
	users    *memUserStore
	adminKey string // API key of user 1 (admin)
	bobKey   string // API key of a non-admin user
	bobID    int64
}

// newRouterEnv builds a Server with in-memory users (1=admin, bob=non-admin)
// plus whatever extra stores the caller supplies, and returns the full
// Handler(). API keys are used for auth so that both principals go through the
// real LookupAPIKey path.
func newRouterEnv(t *testing.T, mutate func(*Dependencies)) *routerEnv {
	t.Helper()
	users := newMemUserStore()
	dep := Dependencies{
		AuthToken: "secret",
		UserStore: users,
		SSRFGuard: testSSRFGuard(t),
	}
	if mutate != nil {
		mutate(&dep)
	}
	s := New(dep)

	bob, err := users.CreateUser(context.Background(), storage.CreateUserParams{Username: "bob", IsAdmin: false})
	if err != nil {
		t.Fatal(err)
	}
	mkKey := func(uid int64) string {
		raw, hash, err := auth.NewAPIToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := users.CreateAPIKey(context.Background(), storage.CreateAPIKeyParams{UserID: uid, Name: "t", TokenHash: hash}); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	return &routerEnv{
		t:        t,
		h:        s.Handler(),
		users:    users,
		adminKey: mkKey(1),
		bobKey:   mkKey(bob.ID),
		bobID:    bob.ID,
	}
}

func (e *routerEnv) do(method, path, token, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *routerEnv) want(rec *httptest.ResponseRecorder, code int) *httptest.ResponseRecorder {
	e.t.Helper()
	if rec.Code != code {
		e.t.Fatalf("want %d, got %d: %s", code, rec.Code, rec.Body.String())
	}
	return rec
}

func decodeData[T any](t *testing.T, rec *httptest.ResponseRecorder) (T, int) {
	t.Helper()
	var resp listResponse[T]
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return resp.Data, resp.Total
}

// ---- users / me / api keys ---------------------------------------------------

func TestRouter_UsersAndMe(t *testing.T) {
	env := newRouterEnv(t, nil)

	// GET /v1/users is admin-only.
	env.want(env.do(http.MethodGet, "/v1/users", env.bobKey, ""), http.StatusForbidden)
	env.want(env.do(http.MethodGet, "/v1/users", "", ""), http.StatusUnauthorized)
	rec := env.want(env.do(http.MethodGet, "/v1/users", env.adminKey, ""), http.StatusOK)
	list, total := decodeData[[]userDTO](t, rec)
	if total != 2 || len(list) != 2 {
		t.Fatalf("users: total=%d len=%d", total, len(list))
	}
	env.want(env.do(http.MethodGet, "/v1/users?limit=abc", env.adminKey, ""), http.StatusBadRequest)

	// GET /v1/me reflects the caller, not user 1.
	rec = env.want(env.do(http.MethodGet, "/v1/me", env.bobKey, ""), http.StatusOK)
	me, _ := decodeData[userDTO](t, rec)
	if me.ID != env.bobID || me.Username != "bob" || me.IsAdmin {
		t.Fatalf("me: %+v", me)
	}
	rec = env.want(env.do(http.MethodGet, "/v1/me", "secret", ""), http.StatusOK)
	me, _ = decodeData[userDTO](t, rec)
	if me.ID != 1 || !me.IsAdmin {
		t.Fatalf("me via AUTH_TOKEN: %+v", me)
	}
}

func TestRouter_APIKeysLifecycle(t *testing.T) {
	env := newRouterEnv(t, nil)

	// Bob starts with exactly the key created by the harness.
	rec := env.want(env.do(http.MethodGet, "/v1/me/api-keys", env.bobKey, ""), http.StatusOK)
	keys, total := decodeData[[]apiKeyDTO](t, rec)
	if total != 1 || len(keys) != 1 {
		t.Fatalf("initial keys: %+v", keys)
	}

	// Create a new key; the raw token is returned once and works for auth.
	rec = env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"  laptop "}`), http.StatusCreated)
	created, _ := decodeData[apiKeyCreateResponse](t, rec)
	if created.Token == "" || created.Name != "laptop" || created.ID == 0 {
		t.Fatalf("created: %+v", created)
	}
	rec = env.want(env.do(http.MethodGet, "/v1/me", created.Token, ""), http.StatusOK)
	me, _ := decodeData[userDTO](t, rec)
	if me.ID != env.bobID {
		t.Fatalf("new token authenticates as %d, want %d", me.ID, env.bobID)
	}

	// Empty name falls back to "default"; invalid JSON is 400.
	rec = env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{}`), http.StatusCreated)
	def, _ := decodeData[apiKeyCreateResponse](t, rec)
	if def.Name != "default" {
		t.Fatalf("default name: %q", def.Name)
	}
	env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":`), http.StatusBadRequest)

	// Admin cannot delete bob's key through /v1/me (tenant-scoped).
	env.want(env.do(http.MethodDelete, "/v1/me/api-keys/"+itoa(created.ID), env.adminKey, ""), http.StatusNotFound)
	env.want(env.do(http.MethodDelete, "/v1/me/api-keys/x", env.bobKey, ""), http.StatusBadRequest)
	rec = env.want(env.do(http.MethodDelete, "/v1/me/api-keys/"+itoa(created.ID), env.bobKey, ""), http.StatusOK)
	del, _ := decodeData[deletedDTO](t, rec)
	if !del.Deleted {
		t.Fatalf("deleted flag not set")
	}
	// The deleted token no longer authenticates.
	env.want(env.do(http.MethodGet, "/v1/me", created.Token, ""), http.StatusUnauthorized)
	env.want(env.do(http.MethodDelete, "/v1/me/api-keys/"+itoa(created.ID), env.bobKey, ""), http.StatusNotFound)
}

// ---- entries -----------------------------------------------------------------

func TestRouter_EntriesByIDAndByFeed(t *testing.T) {
	es := newTenantEntryStore()
	now := time.Now().UTC()
	es.add(1, storage.Entry{ID: 10, FeedID: 5, Title: "admin-unread", Status: "unread", CreatedAt: now})
	es.add(1, storage.Entry{ID: 11, FeedID: 5, Title: "admin-read", Status: "read", CreatedAt: now})
	es.add(1, storage.Entry{ID: 12, FeedID: 6, Title: "other-feed", Status: "unread", CreatedAt: now})
	env := newRouterEnv(t, func(d *Dependencies) { d.EntryStore = es })
	bobEntry := storage.Entry{ID: 20, FeedID: 7, Title: "bob", Status: "unread", CreatedAt: now}
	es.add(env.bobID, bobEntry)
	es.encs[20] = []storage.Enclosure{{URL: "https://example.com/a.mp3", Size: 3, MIMEType: "audio/mpeg"}}

	// GET /v1/entries/{id}
	rec := env.want(env.do(http.MethodGet, "/v1/entries/10", env.adminKey, ""), http.StatusOK)
	e, _ := decodeData[entryDTO](t, rec)
	if e.ID != 10 || e.Title != "admin-unread" {
		t.Fatalf("entry: %+v", e)
	}
	env.want(env.do(http.MethodGet, "/v1/entries/10", env.bobKey, ""), http.StatusNotFound) // isolation
	env.want(env.do(http.MethodGet, "/v1/entries/0", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/entries/10", "", ""), http.StatusUnauthorized)
	rec = env.want(env.do(http.MethodGet, "/v1/entries/20", env.bobKey, ""), http.StatusOK)
	e, _ = decodeData[entryDTO](t, rec)
	if len(e.Enclosures) != 1 || e.Enclosures[0].MIMEType != "audio/mpeg" {
		t.Fatalf("enclosures not attached: %+v", e.Enclosures)
	}

	// GET /v1/feeds/{feedID}/entries (+ status filter)
	rec = env.want(env.do(http.MethodGet, "/v1/feeds/5/entries", env.adminKey, ""), http.StatusOK)
	list, total := decodeData[[]entryDTO](t, rec)
	if total != 2 || len(list) != 2 {
		t.Fatalf("feed 5 entries: total=%d len=%d", total, len(list))
	}
	rec = env.want(env.do(http.MethodGet, "/v1/feeds/5/entries?status=read", env.adminKey, ""), http.StatusOK)
	list, _ = decodeData[[]entryDTO](t, rec)
	if len(list) != 1 || list[0].ID != 11 {
		t.Fatalf("status filter: %+v", list)
	}
	env.want(env.do(http.MethodGet, "/v1/feeds/5/entries?status=bogus", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/feeds/5/entries?limit=-1", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/feeds/abc/entries", env.adminKey, ""), http.StatusBadRequest)
	rec = env.want(env.do(http.MethodGet, "/v1/feeds/5/entries", env.bobKey, ""), http.StatusOK)
	list, total = decodeData[[]entryDTO](t, rec)
	if total != 0 || len(list) != 0 {
		t.Fatalf("bob sees admin feed entries: %+v", list)
	}

	// GET /v1/feeds/{feedID}/entries/{entryID}
	env.want(env.do(http.MethodGet, "/v1/feeds/5/entries/10", env.adminKey, ""), http.StatusOK)
	env.want(env.do(http.MethodGet, "/v1/feeds/6/entries/10", env.adminKey, ""), http.StatusNotFound) // wrong feed
	env.want(env.do(http.MethodGet, "/v1/feeds/7/entries/20", env.adminKey, ""), http.StatusNotFound) // bob's
	env.want(env.do(http.MethodGet, "/v1/feeds/5/entries/x", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/feeds/x/entries/10", env.adminKey, ""), http.StatusBadRequest)

	// PUT /v1/feeds/{feedID}/entries/{entryID}
	rec = env.want(env.do(http.MethodPut, "/v1/feeds/5/entries/10", env.adminKey, `{"status":" read ","starred":true}`), http.StatusOK)
	e, _ = decodeData[entryDTO](t, rec)
	if e.Status != "read" || !e.Starred {
		t.Fatalf("update: %+v", e)
	}
	env.want(env.do(http.MethodPut, "/v1/feeds/5/entries/10", env.adminKey, `{}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/feeds/5/entries/10", env.adminKey, `{"status":"nope"}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/feeds/5/entries/10", env.adminKey, `{"status":`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/feeds/5/entries/999", env.adminKey, `{"starred":true}`), http.StatusNotFound)
	env.want(env.do(http.MethodPut, "/v1/feeds/5/entries/10", env.bobKey, `{"starred":true}`), http.StatusNotFound)
	env.want(env.do(http.MethodPut, "/v1/feeds/x/entries/10", env.adminKey, `{"starred":true}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/feeds/5/entries/x", env.adminKey, `{"starred":true}`), http.StatusBadRequest)
}

func TestRouter_EntriesWithoutStore(t *testing.T) {
	env := newRouterEnv(t, nil)
	env.want(env.do(http.MethodGet, "/v1/entries/1", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/feeds/1/entries", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/feeds/1/entries/1", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodPut, "/v1/feeds/1/entries/1", env.adminKey, `{"starred":true}`), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/feeds/1/icon", env.adminKey, ""), http.StatusServiceUnavailable)
}

// ---- feed icon ---------------------------------------------------------------

func TestRouter_FeedIcon(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0, 1, 2}
	iconSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/favicon.ico" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer iconSrv.Close()

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("cached icon is served without network", func(t *testing.T) {
		fs := &iconFeedStore{feed: storage.Feed{ID: 3, FeedURL: "http://127.0.0.1:1/feed.xml", IconData: png}}
		env := newRouterEnv(t, func(d *Dependencies) { d.FeedStore = fs; d.SSRFGuard = guard })
		rec := env.want(env.do(http.MethodGet, "/v1/feeds/3/icon", env.adminKey, ""), http.StatusOK)
		dto, _ := decodeData[feedIconDTO](t, rec)
		if dto.ContentType != "image/png" || dto.Icon != base64.StdEncoding.EncodeToString(png) {
			t.Fatalf("icon dto: %+v", dto)
		}
		env.want(env.do(http.MethodGet, "/v1/feeds/3/icon", env.bobKey, ""), http.StatusNotFound)
		env.want(env.do(http.MethodGet, "/v1/feeds/4/icon", env.adminKey, ""), http.StatusNotFound)
		env.want(env.do(http.MethodGet, "/v1/feeds/x/icon", env.adminKey, ""), http.StatusBadRequest)
		env.want(env.do(http.MethodGet, "/v1/feeds/3/icon", "", ""), http.StatusUnauthorized)
	})

	t.Run("fetches default favicon and caches it", func(t *testing.T) {
		fs := &iconFeedStore{feed: storage.Feed{ID: 3, FeedURL: iconSrv.URL + "/rss?x=1#f"}}
		env := newRouterEnv(t, func(d *Dependencies) { d.FeedStore = fs; d.SSRFGuard = guard })
		rec := env.want(env.do(http.MethodGet, "/v1/feeds/3/icon", env.adminKey, ""), http.StatusOK)
		dto, _ := decodeData[feedIconDTO](t, rec)
		if dto.ContentType != "image/png" {
			t.Fatalf("content type: %q", dto.ContentType)
		}
		fs.mu.Lock()
		defer fs.mu.Unlock()
		if string(fs.cached) != string(png) || fs.cURL != iconSrv.URL+"/favicon.ico" {
			t.Fatalf("icon not cached: url=%q len=%d", fs.cURL, len(fs.cached))
		}
	})

	t.Run("upstream failure is 502", func(t *testing.T) {
		fs := &iconFeedStore{feed: storage.Feed{ID: 3, FeedURL: iconSrv.URL + "/rss", IconURL: iconSrv.URL + "/missing.png"}}
		env := newRouterEnv(t, func(d *Dependencies) { d.FeedStore = fs; d.SSRFGuard = guard })
		env.want(env.do(http.MethodGet, "/v1/feeds/3/icon", env.adminKey, ""), http.StatusBadGateway)
	})

	t.Run("no derivable icon url is 404", func(t *testing.T) {
		fs := &iconFeedStore{feed: storage.Feed{ID: 3, FeedURL: "not a url"}}
		env := newRouterEnv(t, func(d *Dependencies) { d.FeedStore = fs; d.SSRFGuard = guard })
		env.want(env.do(http.MethodGet, "/v1/feeds/3/icon", env.adminKey, ""), http.StatusNotFound)
	})
}

// ---- filters -----------------------------------------------------------------

func TestRouter_FiltersCRUDAndMatches(t *testing.T) {
	fs := newMemFilterStore()
	ms := &memMatchStore{rows: map[int64][]storage.FilterMatchWithEntry{}}
	env := newRouterEnv(t, func(d *Dependencies) {
		d.FilterStore = fs
		d.FilterMatchStore = ms
		d.FilterEngine = filter.New(filter.Config{MaxRulesPerFilter: 50, MaxRegexLength: 2048})
	})

	// Non-admin is rejected everywhere.
	for _, tc := range []struct{ m, p string }{
		{http.MethodGet, "/v1/filters"}, {http.MethodGet, "/v1/filters/1"},
		{http.MethodPut, "/v1/filters/1"}, {http.MethodDelete, "/v1/filters/1"},
		{http.MethodGet, "/v1/filters/1/matches"},
	} {
		env.want(env.do(tc.m, tc.p, env.bobKey, `{"name":"x"}`), http.StatusForbidden)
	}

	rec := env.want(env.do(http.MethodGet, "/v1/filters", env.adminKey, ""), http.StatusOK)
	if _, total := decodeData[[]filterDTO](t, rec); total != 0 {
		t.Fatalf("expected empty list, total=%d", total)
	}
	env.want(env.do(http.MethodGet, "/v1/filters?offset=-5", env.adminKey, ""), http.StatusBadRequest)

	rec = env.want(env.do(http.MethodPost, "/v1/filters", env.adminKey,
		`{"name":"Foo","enabled":true,"rules":[{"field":"title","pattern":"foo","op":"and"}]}`), http.StatusCreated)
	created, _ := decodeData[filterDTO](t, rec)
	id := itoa(created.ID)

	rec = env.want(env.do(http.MethodGet, "/v1/filters", env.adminKey, ""), http.StatusOK)
	list, total := decodeData[[]filterDTO](t, rec)
	if total != 1 || len(list) != 1 || list[0].Name != "Foo" {
		t.Fatalf("list: %+v", list)
	}

	rec = env.want(env.do(http.MethodGet, "/v1/filters/"+id, env.adminKey, ""), http.StatusOK)
	got, _ := decodeData[filterDTO](t, rec)
	if got.ID != created.ID || len(got.Rules) != 1 || got.Rules[0].Pattern != "foo" {
		t.Fatalf("get: %+v", got)
	}
	env.want(env.do(http.MethodGet, "/v1/filters/999", env.adminKey, ""), http.StatusNotFound)
	env.want(env.do(http.MethodGet, "/v1/filters/abc", env.adminKey, ""), http.StatusBadRequest)

	// Update: rename + change rule.
	rec = env.want(env.do(http.MethodPut, "/v1/filters/"+id, env.adminKey,
		`{"name":"Bar","enabled":false,"rules":[{"field":"title","pattern":"bar","op":"and"}]}`), http.StatusOK)
	upd, _ := decodeData[filterDTO](t, rec)
	if upd.Name != "Bar" || upd.Enabled || len(upd.Rules) != 1 || upd.Rules[0].Pattern != "bar" {
		t.Fatalf("update: %+v", upd)
	}
	env.want(env.do(http.MethodPut, "/v1/filters/"+id, env.adminKey, `{"name":""}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/filters/"+id, env.adminKey, `{"name":"x","rules":[{"field":"title","pattern":"(","op":"and"}]}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/filters/"+id, env.adminKey, `not json`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/filters/999", env.adminKey, `{"name":"x"}`), http.StatusNotFound)
	env.want(env.do(http.MethodPut, "/v1/filters/abc", env.adminKey, `{"name":"x"}`), http.StatusBadRequest)

	// Matches.
	ms.rows[created.ID] = []storage.FilterMatchWithEntry{{
		Match: storage.FilterMatch{ID: 1, FilterID: created.ID, EntryID: 77, MatchedAt: time.Now()},
		Entry: storage.Entry{ID: 77, FeedID: 5, Title: "matched"},
	}}
	rec = env.want(env.do(http.MethodGet, "/v1/filters/"+id+"/matches", env.adminKey, ""), http.StatusOK)
	rows, total := decodeData[[]filterMatchRowDTO](t, rec)
	if total != 1 || len(rows) != 1 || rows[0].Entry.Title != "matched" || rows[0].EntryID != 77 {
		t.Fatalf("matches: %+v", rows)
	}
	env.want(env.do(http.MethodGet, "/v1/filters/999/matches", env.adminKey, ""), http.StatusNotFound)
	env.want(env.do(http.MethodGet, "/v1/filters/abc/matches", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/filters/"+id+"/matches?limit=x", env.adminKey, ""), http.StatusBadRequest)

	// Delete.
	rec = env.want(env.do(http.MethodDelete, "/v1/filters/"+id, env.adminKey, ""), http.StatusOK)
	if del, _ := decodeData[deletedDTO](t, rec); !del.Deleted {
		t.Fatalf("deleted flag")
	}
	env.want(env.do(http.MethodDelete, "/v1/filters/"+id, env.adminKey, ""), http.StatusNotFound)
	env.want(env.do(http.MethodDelete, "/v1/filters/abc", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/filters/"+id, env.adminKey, ""), http.StatusNotFound)
}

func TestRouter_FiltersWithoutStore(t *testing.T) {
	env := newRouterEnv(t, nil)
	env.want(env.do(http.MethodGet, "/v1/filters", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/filters/1", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodPut, "/v1/filters/1", env.adminKey, `{"name":"x"}`), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodDelete, "/v1/filters/1", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/filters/1/matches", env.adminKey, ""), http.StatusServiceUnavailable)
}

// ---- webhooks ----------------------------------------------------------------

func TestRouter_WebhooksCRUDAndLogs(t *testing.T) {
	ws := newMemWebhookStore()
	logs := newMemWebhookLogStore()
	env := newRouterEnv(t, func(d *Dependencies) {
		d.WebhookStore = ws
		d.WebhookLogStore = logs
	})

	for _, tc := range []struct{ m, p string }{
		{http.MethodGet, "/v1/webhooks"}, {http.MethodGet, "/v1/webhooks/1"},
		{http.MethodPut, "/v1/webhooks/1"}, {http.MethodDelete, "/v1/webhooks/1"},
		{http.MethodGet, "/v1/webhooks/1/logs"}, {http.MethodPost, "/v1/webhook-logs/1/retry"},
	} {
		env.want(env.do(tc.m, tc.p, env.bobKey, `{"url":"https://example.com/h"}`), http.StatusForbidden)
		env.want(env.do(tc.m, tc.p, "", `{"url":"https://example.com/h"}`), http.StatusUnauthorized)
	}

	rec := env.want(env.do(http.MethodGet, "/v1/webhooks", env.adminKey, ""), http.StatusOK)
	if _, total := decodeData[[]webhookDTO](t, rec); total != 0 {
		t.Fatalf("expected no webhooks, total=%d", total)
	}
	env.want(env.do(http.MethodGet, "/v1/webhooks?limit=x", env.adminKey, ""), http.StatusBadRequest)

	rec = env.want(env.do(http.MethodPost, "/v1/webhooks", env.adminKey,
		`{"name":"alerts","url":"https://example.com/hook","secret":"s1","enabled":true}`), http.StatusCreated)
	created, _ := decodeData[webhookDTO](t, rec)
	id := itoa(created.ID)

	rec = env.want(env.do(http.MethodGet, "/v1/webhooks", env.adminKey, ""), http.StatusOK)
	list, total := decodeData[[]webhookDTO](t, rec)
	if total != 1 || len(list) != 1 || list[0].Name != "alerts" {
		t.Fatalf("list: %+v", list)
	}
	rec = env.want(env.do(http.MethodGet, "/v1/webhooks/"+id, env.adminKey, ""), http.StatusOK)
	got, _ := decodeData[webhookDTO](t, rec)
	if got.ID != created.ID || got.URL != "https://example.com/hook" {
		t.Fatalf("get: %+v", got)
	}
	env.want(env.do(http.MethodGet, "/v1/webhooks/999", env.adminKey, ""), http.StatusNotFound)
	env.want(env.do(http.MethodGet, "/v1/webhooks/abc", env.adminKey, ""), http.StatusBadRequest)

	// Update.
	rec = env.want(env.do(http.MethodPut, "/v1/webhooks/"+id, env.adminKey,
		`{"name":"alerts2","url":"https://example.com/hook2","method":"put","enabled":false,"on_success_entry":"mark_read"}`), http.StatusOK)
	upd, _ := decodeData[webhookDTO](t, rec)
	if upd.Name != "alerts2" || upd.URL != "https://example.com/hook2" || upd.Method != "PUT" || upd.Enabled || upd.OnSuccessEntry != "mark_read" {
		t.Fatalf("update: %+v", upd)
	}
	env.want(env.do(http.MethodPut, "/v1/webhooks/"+id, env.adminKey, `{"url":"http://127.0.0.1/x"}`), http.StatusBadRequest) // SSRF
	env.want(env.do(http.MethodPut, "/v1/webhooks/"+id, env.adminKey, `{"url":"https://example.com/h","on_success_entry":"explode"}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/webhooks/"+id, env.adminKey, `{`), http.StatusBadRequest)
	env.want(env.do(http.MethodPut, "/v1/webhooks/999", env.adminKey, `{"url":"https://example.com/h"}`), http.StatusNotFound)
	env.want(env.do(http.MethodPut, "/v1/webhooks/abc", env.adminKey, `{"url":"https://example.com/h"}`), http.StatusBadRequest)

	// Logs: seeded for this webhook plus a foreign one owned by bob.
	logs.owners[created.ID] = 1
	logs.owners[42] = env.bobID
	logs.logs[100] = storage.WebhookLog{ID: 100, WebhookID: created.ID, EntryID: 7, Status: "failed", Attempt: 2}
	logs.logs[101] = storage.WebhookLog{ID: 101, WebhookID: created.ID, EntryID: 8, Status: "sent", Attempt: 1}
	logs.logs[200] = storage.WebhookLog{ID: 200, WebhookID: 42, EntryID: 9, Status: "failed", Attempt: 1}
	rec = env.want(env.do(http.MethodGet, "/v1/webhooks/"+id+"/logs", env.adminKey, ""), http.StatusOK)
	rows, total := decodeData[[]webhookLogDTO](t, rec)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("logs: total=%d rows=%+v", total, rows)
	}
	env.want(env.do(http.MethodGet, "/v1/webhooks/42/logs", env.adminKey, ""), http.StatusNotFound) // not admin's webhook
	env.want(env.do(http.MethodGet, "/v1/webhooks/abc/logs", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/webhooks/"+id+"/logs?offset=x", env.adminKey, ""), http.StatusBadRequest)

	// Retry: own failed row ok; sent row / foreign row / bad id rejected.
	rec = env.want(env.do(http.MethodPost, "/v1/webhook-logs/100/retry", env.adminKey, ""), http.StatusOK)
	res, _ := decodeData[map[string]bool](t, rec)
	if !res["retried"] {
		t.Fatalf("retry response: %+v", res)
	}
	if logs.logs[100].Status != "pending" {
		t.Fatalf("log 100 status after retry: %q", logs.logs[100].Status)
	}
	env.want(env.do(http.MethodPost, "/v1/webhook-logs/101/retry", env.adminKey, ""), http.StatusNotFound)
	env.want(env.do(http.MethodPost, "/v1/webhook-logs/200/retry", env.adminKey, ""), http.StatusNotFound)
	if logs.logs[200].Status != "failed" {
		t.Fatalf("foreign log was modified: %q", logs.logs[200].Status)
	}
	env.want(env.do(http.MethodPost, "/v1/webhook-logs/abc/retry", env.adminKey, ""), http.StatusBadRequest)

	// Delete.
	rec = env.want(env.do(http.MethodDelete, "/v1/webhooks/"+id, env.adminKey, ""), http.StatusOK)
	if del, _ := decodeData[deletedDTO](t, rec); !del.Deleted {
		t.Fatalf("deleted flag")
	}
	env.want(env.do(http.MethodDelete, "/v1/webhooks/"+id, env.adminKey, ""), http.StatusNotFound)
	env.want(env.do(http.MethodDelete, "/v1/webhooks/abc", env.adminKey, ""), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/webhooks/"+id+"/logs", env.adminKey, ""), http.StatusNotFound)
}

func TestRouter_WebhooksWithoutStore(t *testing.T) {
	env := newRouterEnv(t, nil)
	env.want(env.do(http.MethodGet, "/v1/webhooks", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/webhooks/1", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodPut, "/v1/webhooks/1", env.adminKey, `{"url":"https://example.com/h"}`), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodDelete, "/v1/webhooks/1", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/webhooks/1/logs", env.adminKey, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodPost, "/v1/webhook-logs/1/retry", env.adminKey, ""), http.StatusServiceUnavailable)
}
