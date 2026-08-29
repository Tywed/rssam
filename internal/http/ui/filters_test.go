package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

type uiMemLabels struct {
	labels []storage.Label
}

func (m *uiMemLabels) ListLabels(_ context.Context, _ int64, limit, offset int) ([]storage.Label, int, error) {
	if offset >= len(m.labels) {
		return nil, len(m.labels), nil
	}
	end := offset + limit
	if end > len(m.labels) {
		end = len(m.labels)
	}
	return m.labels[offset:end], len(m.labels), nil
}
func (m *uiMemLabels) CreateLabel(context.Context, storage.CreateLabelParams) (storage.Label, error) {
	return storage.Label{}, nil
}
func (m *uiMemLabels) GetLabel(_ context.Context, userID, id int64) (storage.Label, error) {
	for _, l := range m.labels {
		if l.ID == id && l.UserID == userID {
			return l, nil
		}
	}
	return storage.Label{}, storage.ErrNotFound
}
func (m *uiMemLabels) UpdateLabel(context.Context, storage.UpdateLabelParams) (storage.Label, error) {
	return storage.Label{}, nil
}
func (m *uiMemLabels) DeleteLabel(context.Context, int64, int64) error { return nil }
func (m *uiMemLabels) AssignEntryLabel(context.Context, int64, int64) error {
	return nil
}
func (m *uiMemLabels) EntryCountsByLabel(context.Context, int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}
func (m *uiMemLabels) UnreadCountsByLabel(context.Context, int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}

type uiMemFilters struct{}

func (uiMemFilters) ListFilters(context.Context, int64, int, int) ([]storage.Filter, int, error) {
	return nil, 0, nil
}
func (uiMemFilters) CreateFilter(context.Context, storage.CreateFilterParams) (storage.Filter, error) {
	return storage.Filter{}, nil
}
func (uiMemFilters) GetFilter(context.Context, int64, int64) (storage.Filter, error) {
	return storage.Filter{}, storage.ErrNotFound
}
func (uiMemFilters) UpdateFilter(context.Context, storage.UpdateFilterParams) (storage.Filter, error) {
	return storage.Filter{}, nil
}
func (uiMemFilters) DeleteFilter(context.Context, int64, int64) error { return nil }
func (uiMemFilters) ListEnabledFilters(context.Context, int64, int) ([]storage.Filter, error) {
	return nil, nil
}

func newAdminUIHandler(t *testing.T) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions:   &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:    uiMemEntries{},
		Feeds:      &uiMemFeeds{},
		Categories: &uiMemCategories{},
		Filters:    uiMemFilters{},
		Labels: &uiMemLabels{labels: []storage.Label{
			{ID: 1, UserID: 1, Caption: "Important", BgColor: "#2980b9", FgColor: "#ffffff"},
		}},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestUI_FilterFormPage(t *testing.T) {
	mux := newAdminUIHandler(t)
	sid := uiSessionCookie(t, nil, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/filters/new", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("filter form: status=%d body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`id="filter-form"`,
		`data-tab="actions"`,
		`id="add-rule"`,
		`id="add-action"`,
		`id="filter-action-row-tpl"`,
		`id="filter-feed-search"`,
		`id="filter-scope-pickers"`,
		`data-suggest="/ui/feeds/suggest"`,
		"Important",
		"/ui/labels",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in filter form body", want)
		}
	}
	if strings.Contains(body, `<select name="scope_category_id"`) {
		t.Fatal("filter form should not use a native multi-select for categories")
	}
	if strings.Contains(body, "Создать метку") {
		t.Fatal("label create form should not be on filter page")
	}
	if strings.Contains(body, "document.querySelectorAll('.filter-tab')") {
		t.Fatal("filter form inline script should be removed (CSP)")
	}
}

func TestUI_LabelsListPage(t *testing.T) {
	mux := newAdminUIHandler(t)
	sid := uiSessionCookie(t, nil, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/labels", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("labels list: status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Метки", "Important", "Новая метка"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in labels list body", want)
		}
	}
	if !strings.Contains(body, `href="/ui/labels"`) || !strings.Contains(body, "active") {
		t.Fatal("labels nav item should be active")
	}
}

func TestUI_FeedSuggest(t *testing.T) {
	catID := int64(3)
	h, err := NewHandler(Config{
		Users: &uiMemUsers{user: storage.User{
			ID: 1, Username: "alice", PasswordHash: mustHash(t, "secret"), IsAdmin: true,
		}},
		Sessions: &uiMemSessions{sessions: map[string]storage.Session{}},
		Entries:  uiMemEntries{},
		Feeds: &uiMemFeeds{feeds: []storage.Feed{
			{ID: 11, Title: "Кубань 24", FeedURL: "https://example.com/kuban.xml", CategoryID: &catID},
			{ID: 12, Title: "Other news", FeedURL: "https://example.com/other.xml"},
		}},
		Categories: &uiMemCategories{cats: []storage.Category{{ID: catID, Title: "СМИ"}}},
		Filters:    uiMemFilters{},
		CSRFSecret: "csrf-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	sid := uiSessionCookie(t, h, mux)

	req := httptest.NewRequest(http.MethodGet, "/ui/feeds/suggest?q=a", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("short query: status=%d", rec.Code)
	}
	var empty struct {
		Data []feedSuggestDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.Data) != 0 {
		t.Fatalf("short query should return no rows, got %d", len(empty.Data))
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/feeds/suggest?q=%D0%BA%D1%83%D0%B1", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("suggest: status=%d body=%q", rec.Code, rec.Body.String())
	}
	var got struct {
		Data []feedSuggestDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].ID != 11 || got.Data[0].CategoryTitle != "СМИ" {
		t.Fatalf("unexpected suggest payload: %+v", got.Data)
	}

	req = httptest.NewRequest(http.MethodGet, "/ui/feeds/suggest?category_id=3", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("category browse: status=%d", rec.Code)
	}
	got = struct {
		Data []feedSuggestDTO `json:"data"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].ID != 11 {
		t.Fatalf("category browse: %+v", got.Data)
	}
}
