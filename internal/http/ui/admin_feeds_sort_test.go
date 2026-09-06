package ui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSortAdminFeedRows(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	rows := []adminFeedRowView{
		{ID: 1, Title: "Beta", EntryCount: 10, UnreadCount: 2, Status: "ok"},
		{ID: 2, Title: "Alpha", ParsingErrorCount: 3, LastCheckedAt: &past, EntryCount: 5, UnreadCount: 1, Status: "errors"},
		{ID: 3, Title: "Gamma", NextCheckAt: &future, EntryCount: 20, UnreadCount: 4, Status: "ok"},
	}

	sortAdminFeedRows(rows, "name", "asc")
	if rows[0].Title != "Alpha" || rows[2].Title != "Gamma" {
		t.Fatalf("name asc: got %q, %q, %q", rows[0].Title, rows[1].Title, rows[2].Title)
	}

	sortAdminFeedRows(rows, "entries", "desc")
	if rows[0].EntryCount != 20 || rows[2].EntryCount != 5 {
		t.Fatalf("entries desc: got %d, %d, %d", rows[0].EntryCount, rows[1].EntryCount, rows[2].EntryCount)
	}

	sortAdminFeedRows(rows, "errors", "desc")
	if rows[0].ParsingErrorCount != 3 {
		t.Fatalf("errors desc: first=%d", rows[0].ParsingErrorCount)
	}

	sortAdminFeedRows(rows, "id", "desc")
	if rows[0].ID != 3 || rows[2].ID != 1 {
		t.Fatalf("id desc: got %d, %d, %d", rows[0].ID, rows[1].ID, rows[2].ID)
	}
}

func TestFilterAdminFeedRows(t *testing.T) {
	rows := []adminFeedRowView{
		{Status: "ok"},
		{Status: "errors"},
		{Status: "paused"},
	}
	filtered := filterAdminFeedRows(rows, "errors")
	if len(filtered) != 1 || filtered[0].Status != "errors" {
		t.Fatalf("filter errors: %+v", filtered)
	}
	if len(filterAdminFeedRows(rows, "all")) != 3 {
		t.Fatal("filter all should keep all rows")
	}
}

func TestAdminFeedsRedirectFromForm(t *testing.T) {
	form := url.Values{
		"status": {"errors"},
		"sort":   {"id"},
		"order":  {"desc"},
		"page":   {"2"},
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/admin/feeds/1/refresh", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	got := adminFeedsRedirect(req)
	want := "/ui/admin/feeds?status=errors&sort=id&order=desc&page=2"
	if got != want {
		t.Fatalf("redirect=%q want %q", got, want)
	}
}

// Referer is reduced to a local UI path: the origin is always dropped, and
// anything outside /ui/ falls back to the list.
func TestAdminFeedsRedirectFromReferer(t *testing.T) {
	cases := map[string]string{
		"": "/ui/admin/feeds",
		"https://rss.example/ui/admin/feeds?page=3":         "/ui/admin/feeds?page=3",
		"http://127.0.0.1:8080/ui/admin/feeds/7":            "/ui/admin/feeds/7",
		"https://evil.example/ui/settings":                  "/ui/settings",
		"https://rss.example/ui/unread":                     "/ui/unread",
		"//evil.example/ui/admin/feeds":                     "/ui/admin/feeds",
		"https://evil.example\\@rss.example/ui/admin/feeds": "/ui/admin/feeds",
		"::not a url::": "/ui/admin/feeds",
	}
	for ref, want := range cases {
		req := httptest.NewRequest(http.MethodPost, "/ui/admin/feeds/1/refresh", nil)
		if ref != "" {
			req.Header.Set("Referer", ref)
		}
		if got := adminFeedsRedirect(req); got != want {
			t.Errorf("referer %q: redirect=%q want %q", ref, got, want)
		}
	}
}

func TestParseAdminFeedsSort(t *testing.T) {
	req := httptestNewRequest(t, "/ui/admin/feeds?sort=entries&order=desc")
	sortKey, order := parseAdminFeedsSort(req)
	if sortKey != "entries" || order != "desc" {
		t.Fatalf("got sort=%q order=%q", sortKey, order)
	}

	req = httptestNewRequest(t, "/ui/admin/feeds?sort=invalid&order=up")
	sortKey, order = parseAdminFeedsSort(req)
	if sortKey != "name" || order != "asc" {
		t.Fatalf("invalid sort defaults: sort=%q order=%q", sortKey, order)
	}
}

func httptestNewRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
