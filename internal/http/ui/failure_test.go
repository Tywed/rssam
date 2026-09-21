package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/http/middleware"
	"rssam/internal/storage"
)

type failingFeeds struct {
	uiMemFeeds
}

func (f *failingFeeds) SetFeedManualPaused(context.Context, int64, bool) error {
	return errors.New("pq: connection reset")
}

func (f *failingFeeds) ResetErrorFeedPollCircuits(context.Context) (int64, error) {
	return 0, errors.New("pq: connection reset")
}

// A mutation whose store call fails must not answer with the same redirect
// as a success: the target carries an error code and the request id, and
// the landing page shows them. The next successful action drops them.
func TestUIMutation_StoreFailureIsVisible(t *testing.T) {
	feeds := &failingFeeds{uiMemFeeds{feeds: []storage.Feed{{ID: 1, Title: "News", FeedType: "rss"}}}}
	h, err := NewHandler(Config{
		Users:      &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), Role: auth.RoleAdmin}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      feeds,
		Categories: &uiMemCategories{},
		AdminFeeds: &uiMemAdminFeeds{},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	handler := middleware.RequestID(mux)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	post := func(path string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {token}}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	for _, path := range []string{"/ui/admin/feeds/1/pause", "/ui/admin/feeds/reset-circuits"} {
		rec := post(path)
		if rec.Code != http.StatusFound {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		rid := rec.Header().Get("X-Request-Id")
		if loc.Query().Get("err") != "op" || loc.Query().Get("rid") != rid || rid == "" {
			t.Fatalf("%s: redirect %q must carry err=op and rid=%q", path, loc, rid)
		}

		req := httptest.NewRequest(http.MethodGet, loc.RequestURI(), nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		page := httptest.NewRecorder()
		handler.ServeHTTP(page, req)
		if page.Code != http.StatusOK {
			t.Fatalf("%s: landing page %d", path, page.Code)
		}
		body := page.Body.String()
		if !strings.Contains(body, "flash-err") || !strings.Contains(body, "Действие не выполнено") || !strings.Contains(body, rid) {
			t.Fatalf("%s: banner with the request id missing from landing page", path)
		}
	}

	// Referer-based redirects strip the stale banner.
	got := localUIPath("/ui/admin/feeds?status=errors&err=op&rid=abc", "/ui/x")
	if got != "/ui/admin/feeds?status=errors" {
		t.Fatalf("stale flash kept: %q", got)
	}
	if got := localUIPath("/ui/admin/feeds?status=errors", "/ui/x"); got != "/ui/admin/feeds?status=errors" {
		t.Fatalf("query rewritten without need: %q", got)
	}
}

func TestUIMutation_FetchFailureIsAWarning(t *testing.T) {
	if flashErrText(flashErrFetch, "") == "" || strings.Contains(flashErrText(flashErrFetch, "x"), "x") {
		t.Fatal("fetch failures explain themselves without a request id")
	}
	if flashErrText(flashErrInternal, "not valid!") != "Действие не выполнено: внутренняя ошибка." {
		t.Fatal("an invalid request id must not be echoed")
	}
}
