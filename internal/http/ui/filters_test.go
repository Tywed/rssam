package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"rssam/internal/auth"
	"rssam/internal/filter"
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
		FilterEngine: filter.New(filter.Config{}),
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
	if !strings.Contains(body, `value="label:1"`) {
		t.Fatal("label option values must be prefixed so they do not collide with webhook IDs")
	}
}

func TestParseFilterActionParam(t *testing.T) {
	cases := []struct {
		typ, raw, want string
	}{
		{"webhook", "webhook:1", "1"},
		{"label", "label:1", "1"},
		{"webhook", "label:1", ""},
		{"webhook", "1", "1"},
		{"delete", "webhook:1", ""},
		{"webhook", "", ""},
	}
	for _, tc := range cases {
		if got := parseFilterActionParam(tc.typ, tc.raw); got != tc.want {
			t.Fatalf("parseFilterActionParam(%q, %q)=%q want %q", tc.typ, tc.raw, got, tc.want)
		}
	}
}

func TestParseActionsFromFormPrefixed(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("action_type=webhook&action_param=webhook:1&action_type=label&action_param=label:2"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	got := parseActionsFromForm(r)
	if len(got) != 2 {
		t.Fatalf("got %d actions", len(got))
	}
	if got[0].ActionType != "webhook" || got[0].ActionParam != "1" {
		t.Fatalf("webhook action: %+v", got[0])
	}
	if got[1].ActionType != "label" || got[1].ActionParam != "2" {
		t.Fatalf("label action: %+v", got[1])
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

func TestUI_FilterCreateRejectsInvalidRegex(t *testing.T) {
	mux := newAdminUIHandler(t)
	sid := uiSessionCookie(t, nil, mux)
	token := auth.CSRFToken("csrf-test", sid)

	post := func(pattern string) *httptest.ResponseRecorder {
		form := url.Values{
			"csrf_token":   {token},
			"name":         {"broken"},
			"rule_field":   {"title"},
			"rule_pattern": {pattern},
		}
		req := httptest.NewRequest(http.MethodPost, "/ui/filters", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sid})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("("); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "compile regex") {
		t.Fatalf("invalid regex: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if rec := post("foo"); rec.Code != http.StatusFound {
		t.Fatalf("valid regex: status=%d body=%q", rec.Code, rec.Body.String())
	}
}
