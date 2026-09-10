package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type pagingEntries struct {
	uiMemEntries
	filters []storage.ListEntriesFilter
}

func (p *pagingEntries) ListEntries(_ context.Context, _ int64, f storage.ListEntriesFilter) ([]storage.Entry, int, error) {
	p.filters = append(p.filters, f)
	out := make([]storage.Entry, f.Limit)
	for i := range out {
		out[i] = storage.Entry{ID: int64(f.Offset + i + 1), FeedID: 1, Title: "t", Status: storage.EntryStatusUnread}
	}
	return out, f.Offset + f.Limit + 1, nil
}

// html/template percent-encodes "&" inside href unless the value is a
// template.URL, which turned "?offset=50&feed_id=1&sort=oldest" into a single
// bogus offset parameter: page two of a feed silently showed page two of all
// entries with the default sort.
func TestUI_EntryListLinksKeepQueryParameters(t *testing.T) {
	entries := &pagingEntries{}
	catID := int64(3)
	h, err := NewHandler(Config{
		Users:      &uiMemUsers{user: storage.User{ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret")}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    entries,
		Feeds:      &uiMemFeeds{feeds: []storage.Feed{{ID: 1, Title: "f", CategoryID: &catID}}},
		Categories: &uiMemCategories{cats: []storage.Category{{ID: catID, Title: "c"}}},
		Labels:     &uiMemLabels{labels: []storage.Label{{ID: 5, UserID: 1, Caption: "l"}}},
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

	body := get("/ui/unread?feed_id=1&sort=oldest")
	for _, want := range []string{
		`href="?offset=50&amp;feed_id=1&amp;sort=oldest"`,
		`href="/ui/unread?entry_id=1&amp;feed_id=1&amp;sort=oldest"`,
		`href="/ui/unread?sort=oldest"`,
		`href="/ui/labels/5?sort=oldest"`,
		`href="/ui/unread?category_id=3&amp;sort=oldest"`,
		`href="/ui/unread?feed_id=1&amp;sort=oldest"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "%26") {
		t.Error("query separators were percent-encoded")
	}

	get("/ui/unread?offset=50&feed_id=1&sort=oldest")
	last := entries.filters[len(entries.filters)-1]
	if last.Offset != 50 || last.FeedID == nil || *last.FeedID != 1 || last.Sort != storage.EntrySortOldest {
		t.Fatalf("page two filter = %+v", last)
	}
}
