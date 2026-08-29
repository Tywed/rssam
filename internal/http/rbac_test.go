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

func TestRBAC_SubscriptionWritesRequireAdmin(t *testing.T) {
	users := newMemUserStore()
	feeds := newTenantFeedStore()
	bob, err := users.CreateUser(context.Background(), storage.CreateUserParams{Username: "bob", IsAdmin: false})
	if err != nil {
		t.Fatal(err)
	}
	bobFeed, err := feeds.CreateFeed(context.Background(), bob.ID, storage.CreateFeedParams{
		FeedURL: "https://example.com/bob.xml", Title: "Bob",
	})
	if err != nil {
		t.Fatal(err)
	}
	bobTok := issueAPIToken(t, users, bob.ID)
	adminTok := issueAPIToken(t, users, 1)

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
		user   bool
		want   int
	}
	feedID := strconv.FormatInt(bobFeed.ID, 10)
	cases := []tc{
		{name: "user list feeds", method: http.MethodGet, path: "/v1/feeds", user: true, want: http.StatusOK},
		{name: "user get feed", method: http.MethodGet, path: "/v1/feeds/" + feedID, user: true, want: http.StatusOK},
		{name: "user list categories", method: http.MethodGet, path: "/v1/categories", user: true, want: http.StatusOK},
		{name: "user create feed", method: http.MethodPost, path: "/v1/feeds", body: `{"feed_url":"https://example.com/new.xml"}`, user: true, want: http.StatusForbidden},
		{name: "user update feed", method: http.MethodPut, path: "/v1/feeds/" + feedID, body: `{"feed_url":"https://example.com/bob.xml","title":"x"}`, user: true, want: http.StatusForbidden},
		{name: "user delete feed", method: http.MethodDelete, path: "/v1/feeds/" + feedID, user: true, want: http.StatusForbidden},
		{name: "user import opml", method: http.MethodPost, path: "/v1/feeds/import", body: "<opml></opml>", user: true, want: http.StatusForbidden},
		{name: "user export opml", method: http.MethodGet, path: "/v1/feeds/export", user: true, want: http.StatusForbidden},
		{name: "user import job", method: http.MethodGet, path: "/v1/feeds/import/jobs/none", user: true, want: http.StatusForbidden},
		{name: "user create category", method: http.MethodPost, path: "/v1/categories", body: `{"title":"News"}`, user: true, want: http.StatusForbidden},
		{name: "user update category", method: http.MethodPut, path: "/v1/categories/1", body: `{"title":"News"}`, user: true, want: http.StatusForbidden},
		{name: "user delete category", method: http.MethodDelete, path: "/v1/categories/1", user: true, want: http.StatusForbidden},
		{name: "user refresh feed", method: http.MethodPost, path: "/v1/feeds/" + feedID + "/refresh", user: true, want: http.StatusServiceUnavailable},
		{name: "admin create feed", method: http.MethodPost, path: "/v1/feeds", body: `{"feed_url":"https://example.com/admin.xml"}`, want: http.StatusCreated},
		{name: "admin create category", method: http.MethodPost, path: "/v1/categories", body: `{"title":"News"}`, want: http.StatusCreated},
		{name: "admin export opml", method: http.MethodGet, path: "/v1/feeds/export", want: http.StatusOK},
	}

	handlers := map[string]http.Handler{
		http.MethodGet + " /v1/feeds":                     http.HandlerFunc(s.handleListFeeds),
		http.MethodGet + " /v1/feeds/{id}":                 http.HandlerFunc(s.handleGetFeed),
		http.MethodGet + " /v1/categories":                 http.HandlerFunc(s.handleListCategories),
		http.MethodPost + " /v1/feeds":                     http.HandlerFunc(s.handleCreateFeed),
		http.MethodPut + " /v1/feeds/{id}":                 http.HandlerFunc(s.handleUpdateFeed),
		http.MethodDelete + " /v1/feeds/{id}":              http.HandlerFunc(s.handleDeleteFeed),
		http.MethodPost + " /v1/feeds/import":              http.HandlerFunc(s.handleImportFeeds),
		http.MethodGet + " /v1/feeds/export":               http.HandlerFunc(s.handleExportFeeds),
		http.MethodGet + " /v1/feeds/import/jobs/{jobID}":  http.HandlerFunc(s.handleGetImportJob),
		http.MethodPost + " /v1/categories":                http.HandlerFunc(s.handleCreateCategory),
		http.MethodPut + " /v1/categories/{id}":            http.HandlerFunc(s.handleUpdateCategory),
		http.MethodDelete + " /v1/categories/{id}":         http.HandlerFunc(s.handleDeleteCategory),
		http.MethodPost + " /v1/feeds/{feedID}/refresh":    http.HandlerFunc(s.handleRefreshFeed),
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
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
			if c.user {
				req.Header.Set("X-Auth-Token", bobTok)
			} else {
				req.Header.Set("X-Auth-Token", adminTok)
			}
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
			if rec.Code != c.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, c.want, rec.Body.String())
			}
		})
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
