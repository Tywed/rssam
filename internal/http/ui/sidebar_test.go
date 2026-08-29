package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func TestUI_SidebarLazySkipsFeedNodes(t *testing.T) {
	catTelegram := int64(10)
	feeds := make([]storage.Feed, 0, 60)
	for i := 0; i < 60; i++ {
		id := int64(i + 1)
		catID := catTelegram
		feeds = append(feeds, storage.Feed{ID: id, Title: fmt.Sprintf("TG %d", id), CategoryID: &catID})
	}

	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"),
		}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds:    &uiMemFeeds{feeds: feeds},
		Categories: &uiMemCategories{cats: []storage.Category{
			{ID: catTelegram, Title: "Telegram"},
		}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread: status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-lazy-feeds="true"`) {
		t.Fatal("expected lazy sidebar for 60 feeds")
	}
	if strings.Contains(body, `class="tree-row tree-feed`) {
		t.Fatal("lazy sidebar should not render feed links in initial HTML")
	}
	if !strings.Contains(body, "(60 каналов)") {
		t.Fatal("category feed count should remain in sidebar")
	}
}

func TestUI_SidebarCategoryFeedsPartial(t *testing.T) {
	catTelegram := int64(10)
	feeds := make([]storage.Feed, 0, 55)
	for i := 0; i < 55; i++ {
		id := int64(i + 1)
		catID := catTelegram
		feeds = append(feeds, storage.Feed{ID: id, Title: fmt.Sprintf("TG %d", id), CategoryID: &catID})
	}

	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"),
		}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds:    &uiMemFeeds{feeds: feeds},
		Categories: &uiMemCategories{cats: []storage.Category{
			{ID: catTelegram, Title: "Telegram"},
		}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/sidebar/categories/10/feeds", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sidebar feeds: status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `TG 1`) || !strings.Contains(body, `TG 50`) {
		t.Fatal("partial should list first page of category feeds")
	}
	if strings.Contains(body, `TG 55`) {
		t.Fatal("first page should not include feeds beyond page size")
	}
	feedCount := strings.Count(body, `class="tree-row tree-feed`)
	if feedCount != sidebarLazyFeedThreshold {
		t.Fatalf("partial feed count=%d, want %d", feedCount, sidebarLazyFeedThreshold)
	}
	if !strings.Contains(body, `tree-feeds-load-more`) {
		t.Fatal("expected load-more button for paginated category feeds")
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/sidebar/categories/10/feeds?offset=50", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sidebar feeds page 2: status=%d body=%s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if !strings.Contains(body, `TG 51`) || !strings.Contains(body, `TG 55`) {
		t.Fatal("second page should list remaining category feeds")
	}
	if strings.Contains(body, `tree-feeds-load-more`) {
		t.Fatal("last page should not include load-more button")
	}
}
