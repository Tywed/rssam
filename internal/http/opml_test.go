package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rssam/internal/opml"
	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type opmlCategoryStore struct {
	mu         sync.Mutex
	nextID     int64
	categories map[string]storage.Category
}

func newOpmlCategoryStore() *opmlCategoryStore {
	return &opmlCategoryStore{nextID: 1, categories: make(map[string]storage.Category)}
}

func (s *opmlCategoryStore) CreateCategory(_ context.Context, _ int64, title, color string) (storage.Category, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(title))
	if c, ok := s.categories[key]; ok {
		return c, nil
	}
	c := storage.Category{ID: s.nextID, Title: strings.TrimSpace(title), Color: strings.TrimSpace(color)}
	s.nextID++
	s.categories[key] = c
	return c, nil
}

func (s *opmlCategoryStore) ListCategories(_ context.Context, _ int64, _, _ int) ([]storage.Category, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storage.Category, 0, len(s.categories))
	for _, c := range s.categories {
		out = append(out, c)
	}
	return out, len(out), nil
}

func (s *opmlCategoryStore) UpdateCategory(_ context.Context, _ int64, id int64, title, color string) (storage.Category, error) {
	return storage.Category{ID: id, Title: title, Color: color}, nil
}

func (s *opmlCategoryStore) DeleteCategory(_ context.Context, _ int64, _ int64) error {
	return nil
}

func (s *opmlCategoryStore) ReorderCategories(_ context.Context, _ int64, _ []int64) error {
	return nil
}

type opmlFeedStore struct {
	noopFeedStore
	mu    sync.Mutex
	feeds map[string]storage.Feed
	next  int64
}

func newOpmlFeedStore() *opmlFeedStore {
	return &opmlFeedStore{feeds: make(map[string]storage.Feed), next: 1}
}

func (s *opmlFeedStore) CreateFeed(_ context.Context, _ int64, params storage.CreateFeedParams) (storage.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(strings.TrimSpace(params.FeedURL))
	if _, ok := s.feeds[key]; ok {
		return storage.Feed{}, storage.ErrDuplicateFeedURL
	}
	f := storage.Feed{
		ID:                 s.next,
		FeedURL:            params.FeedURL,
		FeedType:           params.FeedType,
		Title:              params.Title,
		CategoryID:         params.CategoryID,
		IntervalMinutes:    params.IntervalMinutes,
		ScraperRules:       params.ScraperRules,
		RewriteRules:       params.RewriteRules,
		BlockedRules:       params.BlockedRules,
		KeepRules:          params.KeepRules,
		FetchViaProxy:      params.FetchViaProxy,
		TLSInsecure:        params.TLSInsecure,
		Crawler:            params.Crawler,
		UserAgent:          params.UserAgent,
		WebhookID:          params.WebhookID,
		StoreHashOnly:      params.StoreHashOnly,
		EntryRetentionDays: params.EntryRetentionDays,
	}
	s.next++
	s.feeds[key] = f
	return f, nil
}

func (s *opmlFeedStore) GetFeed(_ context.Context, _ int64, id int64) (storage.Feed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.feeds {
		if f.ID == id {
			return f, nil
		}
	}
	return storage.Feed{}, storage.ErrNotFound
}

func (s *opmlFeedStore) GetFeedByID(ctx context.Context, id int64) (storage.Feed, error) {
	return s.GetFeed(ctx, 0, id)
}
func (s *opmlFeedStore) ListAllFeeds(_ context.Context, _ int) ([]storage.Feed, error) {
	return nil, nil
}

// ListFeeds mirrors the PostgreSQL projection (id/url/type/title/category/
// interval only) so tests exercise the GetFeed round-trip the export needs.
func (s *opmlFeedStore) ListFeeds(_ context.Context, _ int64, _, _ int) ([]storage.Feed, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storage.Feed, 0, len(s.feeds))
	for _, f := range s.feeds {
		out = append(out, storage.Feed{ID: f.ID, UserID: f.UserID, FeedURL: f.FeedURL, FeedType: f.FeedType, Title: f.Title, CategoryID: f.CategoryID, IntervalMinutes: f.IntervalMinutes})
	}
	return out, len(out), nil
}

func (s *opmlFeedStore) UpdateFeed(_ context.Context, _ int64, params storage.UpdateFeedParams) (storage.Feed, error) {
	return storage.Feed{ID: params.ID}, nil
}

func (s *opmlFeedStore) UpdateFeedRefreshMeta(_ context.Context, _ storage.UpdateFeedRefreshMetaParams) error {
	return nil
}

func (s *opmlFeedStore) SetFeedNextCheckAt(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *opmlFeedStore) DeleteFeed(_ context.Context, _ int64, _ int64) error { return nil }

func TestOPMLImportExportRoundtrip(t *testing.T) {
	guard, err := ssrf.New(ssrf.Config{LookupHost: publicExampleLookup})
	if err != nil {
		t.Fatal(err)
	}

	cats := newOpmlCategoryStore()
	feeds := newOpmlFeedStore()
	s := New(Dependencies{
		AuthToken:      "secret",
		CategoryStore:  cats,
		FeedStore:      feeds,
		SSRFGuard:      guard,
		MaxImportFeeds: 500,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/feeds/import", s.handleImportFeeds)
	mux.HandleFunc("GET /v1/feeds/export", s.handleExportFeeds)
	h := s.wrapAPI(mux)

	fixture, err := os.ReadFile(filepath.Join("..", "opml", "testdata", "sample.opml"))
	if err != nil {
		t.Fatal(err)
	}
	// sample has example.com and news.example.com — both resolve via publicExampleLookup
	// go.dev is not in lookup; replace with example.com for import test
	fixture = bytes.ReplaceAll(fixture, []byte("https://go.dev/blog/.atom"), []byte("https://example.com/go.atom"))

	importReq := httptest.NewRequest(http.MethodPost, "/v1/feeds/import", bytes.NewReader(fixture))
	importReq.Header.Set("Content-Type", "application/xml")
	importReq.Header.Set("X-Auth-Token", "secret")
	importRec := httptest.NewRecorder()
	h.ServeHTTP(importRec, importReq)
	if importRec.Code != http.StatusOK {
		t.Fatalf("import status %d body=%s", importRec.Code, importRec.Body.String())
	}
	if !strings.Contains(importRec.Body.String(), `"feeds_created"`) {
		t.Fatalf("unexpected import body: %s", importRec.Body.String())
	}

	exportReq := httptest.NewRequest(http.MethodGet, "/v1/feeds/export", nil)
	exportReq.Header.Set("X-Auth-Token", "secret")
	exportRec := httptest.NewRecorder()
	h.ServeHTTP(exportRec, exportReq)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export status %d", exportRec.Code)
	}
	if ct := exportRec.Header().Get("Content-Type"); !strings.Contains(ct, "application/xml") {
		t.Fatalf("content-type: %s", ct)
	}

	doc, err := opml.ParseBytes(exportRec.Body.Bytes())
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	exported := doc.CollectFeeds()
	if len(exported) < 2 {
		t.Fatalf("expected exported feeds, got %d", len(exported))
	}
}

func TestOPMLImportMultipart(t *testing.T) {
	guard, err := ssrf.New(ssrf.Config{LookupHost: publicExampleLookup})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Dependencies{
		AuthToken:      "secret",
		CategoryStore:  newOpmlCategoryStore(),
		FeedStore:      newOpmlFeedStore(),
		SSRFGuard:      guard,
		MaxImportFeeds: 500,
	})
	h := s.wrapAPI(http.HandlerFunc(s.handleImportFeeds))

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "subs.opml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, `<?xml version="1.0"?><opml version="2.0"><body><outline type="rss" xmlUrl="https://example.com/feed.xml" text="X"/></body></opml>`)
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/import", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOPMLImportDuplicateSkipped(t *testing.T) {
	guard, err := ssrf.New(ssrf.Config{LookupHost: publicExampleLookup})
	if err != nil {
		t.Fatal(err)
	}
	fs := newOpmlFeedStore()
	_, _ = fs.CreateFeed(context.Background(), 1, storage.CreateFeedParams{
		FeedURL:         "https://example.com/feed.xml",
		IntervalMinutes: 60,
	})
	s := New(Dependencies{
		AuthToken:      "secret",
		CategoryStore:  newOpmlCategoryStore(),
		FeedStore:      fs,
		SSRFGuard:      guard,
		MaxImportFeeds: 500,
	})
	h := s.wrapAPI(http.HandlerFunc(s.handleImportFeeds))

	opmlXML := `<?xml version="1.0"?><opml version="2.0"><body><outline type="rss" xmlUrl="https://example.com/feed.xml" text="Dup"/></body></opml>`
	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/import", strings.NewReader(opmlXML))
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 not 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"feeds_skipped"`) {
		t.Fatalf("expected skipped count: %s", rec.Body.String())
	}
}

// TestOPMLSettingsRoundtrip: per-feed settings (interval, TLS, hash-only,
// feed type, webhook binding, rules) survive export → import via the rssam
// namespace; invalid values and unknown webhooks are reported per feed
// without blocking the import.
func TestOPMLSettingsRoundtrip(t *testing.T) {
	guard, err := ssrf.New(ssrf.Config{LookupHost: publicExampleLookup})
	if err != nil {
		t.Fatal(err)
	}
	webhooks := newMemWebhookStore()
	webhooks.webhooks[7] = storage.Webhook{ID: 7, UserID: 1, Name: "Alerts", Enabled: true}
	webhooks.webhooks[8] = storage.Webhook{ID: 8, UserID: 1, Name: "dup", Enabled: true}
	webhooks.webhooks[9] = storage.Webhook{ID: 9, UserID: 1, Name: "DUP", Enabled: true}

	newServer := func(feeds *opmlFeedStore) http.Handler {
		s := New(Dependencies{
			AuthToken:      "secret",
			CategoryStore:  newOpmlCategoryStore(),
			FeedStore:      feeds,
			WebhookStore:   webhooks,
			SSRFGuard:      guard,
			MaxImportFeeds: 500,
		})
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/feeds/import", s.handleImportFeeds)
		mux.HandleFunc("GET /v1/feeds/export", s.handleExportFeeds)
		return s.wrapAPI(mux)
	}

	// Source instance: one fully configured feed, one default feed.
	src := newOpmlFeedStore()
	retention := 30
	webhookID := int64(7)
	if _, err := src.CreateFeed(context.Background(), 1, storage.CreateFeedParams{
		FeedURL: "https://example.com/tg", FeedType: "telegram", Title: "TG", IntervalMinutes: 5,
		TLSInsecure: true, FetchViaProxy: true, Crawler: true, StoreHashOnly: true, EntryRetentionDays: &retention,
		UserAgent: "ua/2", WebhookID: &webhookID, ScraperRules: "div.post", RewriteRules: "rw", BlockedRules: "ads", KeepRules: "go",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.CreateFeed(context.Background(), 1, storage.CreateFeedParams{FeedURL: "https://example.com/plain", FeedType: "rss", Title: "Plain", IntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	exportReq := httptest.NewRequest(http.MethodGet, "/v1/feeds/export", nil)
	exportReq.Header.Set("X-Auth-Token", "secret")
	exportRec := httptest.NewRecorder()
	newServer(src).ServeHTTP(exportRec, exportReq)
	if exportRec.Code != http.StatusOK {
		t.Fatalf("export status %d", exportRec.Code)
	}
	exported := exportRec.Body.String()
	for _, want := range []string{`rssam:feedType="telegram"`, `rssam:interval="5"`, `rssam:tlsInsecure="true"`, `rssam:hashOnly="true"`, `rssam:retentionDays="30"`, `rssam:webhook="Alerts"`, `rssam:scraperRules="div.post"`} {
		if !strings.Contains(exported, want) {
			t.Fatalf("export lacks %s:\n%s", want, exported)
		}
	}

	// Target instance: import the export plus two hand-edited outlines.
	edited := strings.Replace(exported, "</body>", `
    <outline type="rss" text="Bad" xmlUrl="https://example.com/bad" rssam:interval="999999" rssam:webhook="nope" rssam:feedType="martian" rssam:tlsInsecure="yes-please"/>
    <outline type="rss" text="Dup" xmlUrl="https://example.com/dup" rssam:webhook="dup"/>
  </body>`, 1)
	dst := newOpmlFeedStore()
	importReq := httptest.NewRequest(http.MethodPost, "/v1/feeds/import", strings.NewReader(edited))
	importReq.Header.Set("Content-Type", "application/xml")
	importReq.Header.Set("X-Auth-Token", "secret")
	importRec := httptest.NewRecorder()
	newServer(dst).ServeHTTP(importRec, importReq)
	if importRec.Code != http.StatusOK {
		t.Fatalf("import status %d body=%s", importRec.Code, importRec.Body.String())
	}
	var resp struct {
		Data importReportDTO `json:"data"`
	}
	if err := json.Unmarshal(importRec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.FeedsCreated != 4 {
		t.Fatalf("feeds_created = %d, want 4: %+v", resp.Data.FeedsCreated, resp.Data)
	}

	byURL := map[string]storage.Feed{}
	for _, f := range dst.feeds {
		byURL[f.FeedURL] = f
	}
	tg := byURL["https://example.com/tg"]
	if tg.FeedType != "telegram" || tg.IntervalMinutes != 5 || !tg.TLSInsecure || !tg.FetchViaProxy || !tg.Crawler || !tg.StoreHashOnly ||
		tg.EntryRetentionDays == nil || *tg.EntryRetentionDays != 30 || tg.UserAgent != "ua/2" ||
		tg.WebhookID == nil || *tg.WebhookID != 7 || tg.ScraperRules != "div.post" || tg.RewriteRules != "rw" || tg.BlockedRules != "ads" || tg.KeepRules != "go" {
		t.Fatalf("settings lost on import: %+v", tg)
	}
	plain := byURL["https://example.com/plain"]
	if plain.FeedType != "rss" || plain.IntervalMinutes != 60 || plain.TLSInsecure || plain.WebhookID != nil {
		t.Fatalf("plain feed changed: %+v", plain)
	}
	bad := byURL["https://example.com/bad"]
	if bad.IntervalMinutes != defaultIntervalMinutes || bad.WebhookID != nil || bad.FeedType != "rss" || bad.TLSInsecure {
		t.Fatalf("invalid attributes must fall back to defaults: %+v", bad)
	}
	if dup := byURL["https://example.com/dup"]; dup.WebhookID != nil {
		t.Fatalf("ambiguous webhook name must not bind: %+v", dup)
	}

	reasons := strings.Builder{}
	for _, e := range resp.Data.Errors {
		reasons.WriteString(e.FeedURL + ": " + e.Reason + "\n")
	}
	for _, want := range []string{"rssam:interval: 999999", `rssam:webhook: no webhook named "nope"`, `rssam:feedType: unknown type "martian"`, "rssam:tlsInsecure: expected true/false", `several webhooks are named "dup"`} {
		if !strings.Contains(reasons.String(), want) {
			t.Fatalf("report lacks %q:\n%s", want, reasons.String())
		}
	}
}
