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
	for i := range 60 {
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
	for i := range 55 {
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

// Label unread badges come from the same 3 s snapshot as feed/category
// badges; the per-label entry total (a full join over entry_labels) is only
// computed for the labels settings page.
func TestUI_SidebarLabelCountsCached(t *testing.T) {
	labels := &uiMemLabels{
		labels:      []storage.Label{{ID: 7, UserID: 1, Caption: "Росгвардия"}},
		unread:      map[int64]int{7: 42},
		entryCounts: map[int64]int{7: 1000},
	}
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Labels:     labels,
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)
	get := func(path string) string {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d", path, rec.Code)
		}
		return rec.Body.String()
	}

	body := get("/ui/unread")
	if !strings.Contains(body, `<span class="tree-badge">42</span>`) {
		t.Fatal("label unread badge missing from sidebar")
	}
	get("/ui/unread")
	get("/ui/search")
	if labels.unreadCalls != 1 {
		t.Fatalf("UnreadCountsByLabel called %d times across 3 page loads, want 1 (cached)", labels.unreadCalls)
	}
	if labels.entriesCalls != 0 {
		t.Fatalf("EntryCountsByLabel called %d times on reader pages, want 0", labels.entriesCalls)
	}

	h.invalidateUnread(1)
	get("/ui/unread")
	if labels.unreadCalls != 2 {
		t.Fatalf("UnreadCountsByLabel after invalidate = %d calls, want 2", labels.unreadCalls)
	}

	body = get("/ui/labels")
	if labels.entriesCalls != 1 || !strings.Contains(body, `<td class="meta">1000</td>`) {
		t.Fatalf("labels page: entriesCalls=%d, total cell present=%v", labels.entriesCalls, strings.Contains(body, "1000"))
	}
}
