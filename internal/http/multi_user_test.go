package httpserver

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type memUserStore struct {
	mu      sync.Mutex
	users   map[int64]storage.User
	byName  map[string]int64
	keys    map[string]storage.APIKey
	nextUID int64
	nextKID int64
}

func newMemUserStore() *memUserStore {
	return &memUserStore{
		users:   map[int64]storage.User{1: {ID: 1, Username: "default", IsAdmin: true, CreatedAt: time.Now().UTC()}},
		byName:  map[string]int64{"default": 1},
		keys:    make(map[string]storage.APIKey),
		nextUID: 2,
		nextKID: 1,
	}
}

func (m *memUserStore) CountUsers(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users), nil
}

func (m *memUserStore) CountLoginCapableUsers(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, u := range m.users {
		if u.PasswordHash != "" {
			n++
		}
	}
	return n, nil
}

func (m *memUserStore) ListUsers(_ context.Context, _, _ int) ([]storage.User, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]storage.User, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, u)
	}
	return out, len(out), nil
}

func (m *memUserStore) GetUser(_ context.Context, id int64) (storage.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return storage.User{}, storage.ErrNotFound
	}
	return u, nil
}

func (m *memUserStore) GetUserByUsername(_ context.Context, username string) (storage.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byName[username]
	if !ok {
		return storage.User{}, storage.ErrNotFound
	}
	return m.users[id], nil
}

func (m *memUserStore) GetUserByFeverAPIKey(_ context.Context, key string) (storage.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.FeverAPIKey == key {
			return u, nil
		}
	}
	return storage.User{}, storage.ErrNotFound
}

func (m *memUserStore) CreateUser(_ context.Context, p storage.CreateUserParams) (storage.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.byName[p.Username]; dup {
		return storage.User{}, storage.ErrDuplicateUsername
	}
	u := storage.User{
		ID:           m.nextUID,
		Username:     p.Username,
		PasswordHash: p.PasswordHash,
		FeverAPIKey:  p.FeverAPIKey,
		IsAdmin:      p.IsAdmin,
		CreatedAt:    time.Now().UTC(),
	}
	if u.FeverAPIKey == "" && p.PlainPassword != "" {
		u.FeverAPIKey = storageFeverKey(p.Username, p.PlainPassword)
	}
	m.nextUID++
	m.users[u.ID] = u
	m.byName[u.Username] = u.ID
	return u, nil
}

func (m *memUserStore) UpdateUser(_ context.Context, p storage.UpdateUserParams) (storage.User, error) {
	return m.GetUser(context.Background(), p.ID)
}

func (m *memUserStore) DeleteUser(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return storage.ErrNotFound
	}
	delete(m.users, id)
	delete(m.byName, u.Username)
	return nil
}

func (m *memUserStore) LookupAPIKey(_ context.Context, hash string) (storage.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[hash]
	if !ok {
		return storage.APIKey{}, storage.ErrNotFound
	}
	return k, nil
}

func (m *memUserStore) TouchAPIKeyUsed(_ context.Context, _ int64) error { return nil }

func (m *memUserStore) ListAPIKeys(_ context.Context, userID int64) ([]storage.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []storage.APIKey
	for _, k := range m.keys {
		if k.UserID == userID {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *memUserStore) CreateAPIKey(_ context.Context, p storage.CreateAPIKeyParams) (storage.APIKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := storage.APIKey{
		ID:        m.nextKID,
		UserID:    p.UserID,
		Name:      p.Name,
		TokenHash: p.TokenHash,
		CreatedAt: time.Now().UTC(),
	}
	m.nextKID++
	m.keys[p.TokenHash] = k
	return k, nil
}

func (m *memUserStore) DeleteAPIKey(_ context.Context, userID, keyID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, k := range m.keys {
		if k.ID == keyID && k.UserID == userID {
			delete(m.keys, hash)
			return nil
		}
	}
	return storage.ErrNotFound
}

func storageFeverKey(username, password string) string {
	sum := md5.Sum([]byte(username + ":" + password))
	return hex.EncodeToString(sum[:])
}

type tenantFeedStore struct {
	noopFeedStore
	mu    sync.Mutex
	feeds map[int64]map[string]storage.Feed // userID -> feedURL -> feed
	next  int64
}

func newTenantFeedStore() *tenantFeedStore {
	return &tenantFeedStore{feeds: make(map[int64]map[string]storage.Feed), next: 1}
}

func (s *tenantFeedStore) CreateFeed(_ context.Context, userID int64, p storage.CreateFeedParams) (storage.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.feeds[userID] == nil {
		s.feeds[userID] = make(map[string]storage.Feed)
	}
	if _, ok := s.feeds[userID][p.FeedURL]; ok {
		return storage.Feed{}, storage.ErrDuplicateFeedURL
	}
	f := storage.Feed{ID: s.next, UserID: userID, FeedURL: p.FeedURL, Title: p.Title, IntervalMinutes: 60}
	s.next++
	s.feeds[userID][p.FeedURL] = f
	return f, nil
}

func (s *tenantFeedStore) GetFeed(_ context.Context, userID, id int64) (storage.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.feeds[userID] {
		if f.ID == id {
			return f, nil
		}
	}
	return storage.Feed{}, storage.ErrNotFound
}

func (s *tenantFeedStore) GetFeedByID(_ context.Context, id int64) (storage.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, byURL := range s.feeds {
		for _, f := range byURL {
			if f.ID == id {
				return f, nil
			}
		}
	}
	return storage.Feed{}, storage.ErrNotFound
}

func (s *tenantFeedStore) ListAllFeeds(_ context.Context, _ int) ([]storage.Feed, error) {
	return nil, nil
}
func (s *tenantFeedStore) ListFeeds(_ context.Context, userID int64, _, _ int) ([]storage.Feed, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.Feed
	for _, f := range s.feeds[userID] {
		out = append(out, f)
	}
	return out, len(out), nil
}

func (s *tenantFeedStore) UpdateFeed(_ context.Context, _ int64, _ storage.UpdateFeedParams) (storage.Feed, error) {
	return storage.Feed{}, nil
}
func (s *tenantFeedStore) UpdateFeedRefreshMeta(_ context.Context, _ storage.UpdateFeedRefreshMetaParams) error {
	return nil
}
func (s *tenantFeedStore) SetFeedNextCheckAt(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (s *tenantFeedStore) DeleteFeed(_ context.Context, _ int64, _ int64) error { return nil }

func TestMultiUser_CreateUserAndAPIKeyAuth(t *testing.T) {
	users := newMemUserStore()
	feeds := newTenantFeedStore()
	s := New(Dependencies{
		UserStore: users,
		FeedStore: feeds,
		SSRFGuard: testSSRFGuard(t),
	})
	admin := auth.Principal{UserID: 1, IsAdmin: true}

	// Create user bob as admin (handler only; wrapAPI would require admin token)
	body := `{"username":"bob","password":"bobpass","is_admin":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/users", bytes.NewBufferString(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), admin))
	rec := httptest.NewRecorder()
	s.handleCreateUser(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body.String())
	}

	u, err := users.GetUserByUsername(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}

	raw, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateAPIKey(context.Background(), storage.CreateAPIKeyParams{
		UserID: u.ID, Name: "cli", TokenHash: hash,
	}); err != nil {
		t.Fatal(err)
	}

	// Non-admin API key authenticates but cannot create feeds.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/feeds", bytes.NewBufferString(`{"feed_url":"https://example.com/bob.xml"}`))
	req2.Header.Set("X-Auth-Token", raw)
	rec2 := httptest.NewRecorder()
	s.wrapAPI(http.HandlerFunc(s.handleCreateFeed)).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("bob create feed: %d %s", rec2.Code, rec2.Body.String())
	}

	req3 := httptest.NewRequest(http.MethodGet, "/v1/feeds", nil)
	req3.Header.Set("X-Auth-Token", raw)
	rec3 := httptest.NewRecorder()
	s.wrapAPI(http.HandlerFunc(s.handleListFeeds)).ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("bob list feeds: %d %s", rec3.Code, rec3.Body.String())
	}
}

func TestMultiUser_FeedIsolation(t *testing.T) {
	users := newMemUserStore()
	feeds := newTenantFeedStore()

	uA, _ := users.CreateUser(context.Background(), storage.CreateUserParams{Username: "alice", IsAdmin: false})
	uB, _ := users.CreateUser(context.Background(), storage.CreateUserParams{Username: "bill", IsAdmin: false})

	_, _ = feeds.CreateFeed(context.Background(), uA.ID, storage.CreateFeedParams{FeedURL: "https://example.com/shared.xml", Title: "A"})
	fB, _ := feeds.CreateFeed(context.Background(), uB.ID, storage.CreateFeedParams{FeedURL: "https://example.com/shared.xml", Title: "B"})

	s := New(Dependencies{UserStore: users, FeedStore: feeds})

	tokA, hashA, _ := auth.NewAPIToken()
	_, _ = users.CreateAPIKey(context.Background(), storage.CreateAPIKeyParams{UserID: uA.ID, TokenHash: hashA, Name: "a"})
	tokB, hashB, _ := auth.NewAPIToken()
	_, _ = users.CreateAPIKey(context.Background(), storage.CreateAPIKeyParams{UserID: uB.ID, TokenHash: hashB, Name: "b"})

	listFor := func(token string) []storage.Feed {
		req := httptest.NewRequest(http.MethodGet, "/v1/feeds", nil)
		req.Header.Set("X-Auth-Token", token)
		rec := httptest.NewRecorder()
		s.wrapAPI(http.HandlerFunc(s.handleListFeeds)).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("list feeds status=%d body=%s token=%s", rec.Code, rec.Body.String(), token)
		}
		var resp listResponse[[]feedDTO]
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		out := make([]storage.Feed, 0, len(resp.Data))
		for _, d := range resp.Data {
			out = append(out, storage.Feed{ID: d.ID, FeedURL: d.FeedURL, Title: d.Title})
		}
		return out
	}

	listA := listFor(tokA)
	if len(listA) != 1 || listA[0].Title != "A" {
		t.Fatalf("alice feeds: %+v", listA)
	}
	listB := listFor(tokB)
	if len(listB) != 1 || listB[0].ID != fB.ID || listB[0].Title != "B" {
		t.Fatalf("bill feeds: %+v", listB)
	}

	// Alice cannot GET bill's feed by id
	req := httptest.NewRequest(http.MethodGet, "/v1/feeds/"+strconv.FormatInt(fB.ID, 10), nil)
	req.SetPathValue("id", strconv.FormatInt(fB.ID, 10))
	req.Header.Set("X-Auth-Token", tokA)
	rec := httptest.NewRecorder()
	s.wrapAPI(http.HandlerFunc(s.handleGetFeed)).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant get, got %d", rec.Code)
	}
}
