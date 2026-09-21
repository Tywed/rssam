package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func issueAPIToken(t *testing.T, users *memUserStore, userID int64) string {
	t.Helper()
	raw, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := users.CreateAPIKey(context.Background(), storage.CreateAPIKeyParams{
		UserID: userID, Name: "test", TokenHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	return raw
}

// Catalog writes (feeds, OPML) need the editor role, user management the
// admin role, categories and reads are open to every role. Each case lists
// the expected status per role; a 403 for a lower role is the RBAC verdict,
// everything else is the handler's own answer.
func TestRBAC_ThreeRoles(t *testing.T) {
	users := newMemUserStore()
	feeds := newTenantFeedStore()
	bob, err := users.CreateUser(context.Background(), storage.CreateUserParams{Username: "bob", Role: auth.RoleReader})
	if err != nil {
		t.Fatal(err)
	}
	eve, err := users.CreateUser(context.Background(), storage.CreateUserParams{Username: "eve", Role: auth.RoleEditor})
	if err != nil {
		t.Fatal(err)
	}
	bobFeed, err := feeds.CreateFeed(context.Background(), bob.ID, storage.CreateFeedParams{
		FeedURL: "https://example.com/bob.xml", Title: "Bob",
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{
		auth.RoleReader: issueAPIToken(t, users, bob.ID),
		auth.RoleEditor: issueAPIToken(t, users, eve.ID),
		auth.RoleAdmin:  issueAPIToken(t, users, 1),
	}

	s := New(Dependencies{
		UserStore:     users,
		FeedStore:     feeds,
		CategoryStore: &fakeCategoryStore{},
		SSRFGuard:     testSSRFGuard(t),
	})

	type tc struct {
		name   string
		method string
		path   string
		body   string
		// want is the status per role in the order reader, editor, admin.
		want [3]int
	}
	const ok, created, forbidden, unavailable, notFound = http.StatusOK, http.StatusCreated, http.StatusForbidden, http.StatusServiceUnavailable, http.StatusNotFound
	feedID := strconv.FormatInt(bobFeed.ID, 10)
	cases := []tc{
		{name: "list feeds", method: http.MethodGet, path: "/v1/feeds", want: [3]int{ok, ok, ok}},
		{name: "get feed", method: http.MethodGet, path: "/v1/feeds/" + feedID, want: [3]int{ok, notFound, notFound}},
		{name: "list categories", method: http.MethodGet, path: "/v1/categories", want: [3]int{ok, ok, ok}},
		{name: "create category", method: http.MethodPost, path: "/v1/categories", body: `{"title":"News"}`, want: [3]int{created, created, created}},
		{name: "update category", method: http.MethodPut, path: "/v1/categories/1", body: `{"title":"News"}`, want: [3]int{ok, ok, ok}},
		{name: "create feed", method: http.MethodPost, path: "/v1/feeds", body: `{"feed_url":"https://example.com/new.xml"}`, want: [3]int{forbidden, created, created}},
		{name: "update feed", method: http.MethodPut, path: "/v1/feeds/" + feedID, body: `{"feed_url":"https://example.com/bob.xml","title":"x"}`, want: [3]int{forbidden, ok, ok}},
		{name: "delete feed", method: http.MethodDelete, path: "/v1/feeds/" + feedID, want: [3]int{forbidden, ok, ok}},
		{name: "import opml", method: http.MethodPost, path: "/v1/feeds/import", body: "<opml></opml>", want: [3]int{forbidden, http.StatusBadRequest, http.StatusBadRequest}},
		{name: "export opml", method: http.MethodGet, path: "/v1/feeds/export", want: [3]int{forbidden, ok, ok}},
		{name: "import job", method: http.MethodGet, path: "/v1/feeds/import/jobs/none", want: [3]int{forbidden, notFound, notFound}},
		{name: "refresh feed", method: http.MethodPost, path: "/v1/feeds/" + feedID + "/refresh", want: [3]int{unavailable, unavailable, unavailable}},
		{name: "refresh all", method: http.MethodPost, path: "/v1/feeds/refresh", want: [3]int{forbidden, forbidden, unavailable}},
		{name: "list users", method: http.MethodGet, path: "/v1/users", want: [3]int{forbidden, forbidden, ok}},
		{name: "system info", method: http.MethodGet, path: "/v1/system/info", want: [3]int{forbidden, forbidden, ok}},
	}

	handlers := map[string]http.Handler{
		http.MethodGet + " /v1/feeds":                     http.HandlerFunc(s.handleListFeeds),
		http.MethodGet + " /v1/feeds/{id}":                http.HandlerFunc(s.handleGetFeed),
		http.MethodGet + " /v1/categories":                http.HandlerFunc(s.handleListCategories),
		http.MethodPost + " /v1/feeds":                    http.HandlerFunc(s.handleCreateFeed),
		http.MethodPut + " /v1/feeds/{id}":                http.HandlerFunc(s.handleUpdateFeed),
		http.MethodDelete + " /v1/feeds/{id}":             http.HandlerFunc(s.handleDeleteFeed),
		http.MethodPost + " /v1/feeds/import":             http.HandlerFunc(s.handleImportFeeds),
		http.MethodGet + " /v1/feeds/export":              http.HandlerFunc(s.handleExportFeeds),
		http.MethodGet + " /v1/feeds/import/jobs/{jobID}": http.HandlerFunc(s.handleGetImportJob),
		http.MethodPost + " /v1/categories":               http.HandlerFunc(s.handleCreateCategory),
		http.MethodPut + " /v1/categories/{id}":           http.HandlerFunc(s.handleUpdateCategory),
		http.MethodPost + " /v1/feeds/{feedID}/refresh":   http.HandlerFunc(s.handleRefreshFeed),
		http.MethodPost + " /v1/feeds/refresh":            http.HandlerFunc(s.handleRefreshAllFeeds),
		http.MethodGet + " /v1/users":                     http.HandlerFunc(s.handleListUsers),
		http.MethodGet + " /v1/system/info":               http.HandlerFunc(s.handleSystemInfo),
	}
	roles := []string{auth.RoleReader, auth.RoleEditor, auth.RoleAdmin}

	for _, c := range cases {
		for i, role := range roles {
			t.Run(c.name+"/"+role, func(t *testing.T) {
				var body *bytes.Buffer
				if c.body != "" {
					body = bytes.NewBufferString(c.body)
				} else {
					body = bytes.NewBuffer(nil)
				}
				req := httptest.NewRequest(c.method, c.path, body)
				if c.body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				req.Header.Set("X-Auth-Token", tokens[role])
				req.SetPathValue("id", feedID)
				req.SetPathValue("feedID", feedID)
				req.SetPathValue("jobID", "none")

				key := c.method + " " + routeKey(c.path, feedID)
				h, ok := handlers[key]
				if !ok {
					t.Fatalf("no handler for %s", key)
				}
				rec := httptest.NewRecorder()
				s.wrapAPI(h).ServeHTTP(rec, req)
				if rec.Code != c.want[i] {
					t.Fatalf("status=%d want=%d body=%s", rec.Code, c.want[i], rec.Body.String())
				}
			})
		}
	}
}

func routeKey(path, feedID string) string {
	switch path {
	case "/v1/feeds/" + feedID:
		return "/v1/feeds/{id}"
	case "/v1/feeds/" + feedID + "/refresh":
		return "/v1/feeds/{feedID}/refresh"
	case "/v1/categories/1":
		return "/v1/categories/{id}"
	case "/v1/feeds/import/jobs/none":
		return "/v1/feeds/import/jobs/{jobID}"
	default:
		return path
	}
}
