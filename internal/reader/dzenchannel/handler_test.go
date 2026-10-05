package dzenchannel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/bridge/v1"
)

// exportStub serves the trimmed real responses of dzen.ru/api/web/v1/export
// for channel tass and records the requests it saw.
func exportStub(t *testing.T) (*httptest.Server, *[]string, *atomic.Int32) {
	t.Helper()
	var seen []string
	var status atomic.Int32
	status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seen = append(seen, q.Get("channel_name")+"|"+q.Get("channel_id")+"|"+q.Get("content_type"))
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Cookie") == "" {
			t.Errorf("missing headers: %v", r.Header)
		}
		if code := int(status.Load()); code != http.StatusOK {
			if code == http.StatusTooManyRequests {
				w.Header().Set("Retry-After", "120")
			}
			w.WriteHeader(code)
			return
		}
		if q.Get("channel_name") != "tass" && q.Get("channel_id") != "5f9abb2e66afb7042d59a0af" || q.Get("sort_type") != "regular" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(mustRead(t, "not_found.json"))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(mustRead(t, "tass_"+q.Get("content_type")+".json"))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen, &status
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newHandler(srv *httptest.Server) *Handler {
	return NewHandler(NewClient(srv.Client(), nil, Config{ExportURL: srv.URL}))
}

func TestFetch_AllTabs(t *testing.T) {
	srv, seen, _ := exportStub(t)
	h := newHandler(srv)
	if h.Name() != "dzen_channel" || h.DetectFeedType("https://dzen.ru/tass") != "dzen_channel" || h.DetectFeedType("https://dzen.ru/news/search?text=x") != "" {
		t.Fatal("Name/DetectFeedType")
	}
	res, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://dzen.ru/tass"})
	if err != nil {
		t.Fatal(err)
	}
	// article first (it carries the tabs), then the other two tabs the channel has.
	if want := []string{"tass||article", "tass||long_video", "tass||short_video"}; !slices.Equal(*seen, want) {
		t.Fatalf("requests = %v, want %v", *seen, want)
	}
	// 4 articles + 4 long videos + 4 shorts; carousels and the subscribe card are not entries.
	if len(res.Entries) != 12 {
		t.Fatalf("entries = %d", len(res.Entries))
	}
	first := res.Entries[0]
	if first.Title != "Тренер сборной Нигерии Шелль назвал Россию гостеприимной" {
		t.Fatalf("title = %q", first.Title)
	}
	if first.URL != "https://dzen.ru/a/asPgHIhyeVtzCmCb" {
		t.Fatalf("url = %q (share link without tracking query)", first.URL)
	}
	if first.Author == nil || *first.Author != "ТАСС" {
		t.Fatalf("author = %v", first.Author)
	}
	if first.PublishedAt == nil || !first.PublishedAt.Equal(time.Unix(1791221749, 0)) {
		t.Fatalf("published = %v", first.PublishedAt)
	}
	if first.Hash == "" || first.Hash == res.Entries[1].Hash {
		t.Fatal("hash must be per publication")
	}
	wantImg := `<img src="https://avatars.dzeninfra.ru/get-zen_doc/271828/pub_6ac3e01c8872795b730a609b_6ac3e05f934d8121ed61c1c8/scale_1200" alt="" loading="lazy">`
	if !strings.Contains(first.Content, wantImg) || !strings.Contains(first.Content, "<p>Нигерийцы сыграют с россиянами во вторник") {
		t.Fatalf("content = %q", first.Content)
	}
	video := res.Entries[4]
	if video.URL != "https://dzen.ru/video/watch/6abcab9fa95eb9133bce49ab" || video.Title == "" {
		t.Fatalf("video entry = %+v", video)
	}
	short := res.Entries[8]
	if short.URL != "https://dzen.ru/shorts/6981fbfc1269841b377d6771" {
		t.Fatalf("short entry = %+v", short)
	}
	if res.State != nil || res.NotModified {
		t.Fatalf("stateless bridge: %+v", res)
	}
}

func TestFetch_TypesSubsetAndByID(t *testing.T) {
	srv, seen, _ := exportStub(t)
	h := newHandler(srv)
	res, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "dzen-channel://id/5f9abb2e66afb7042d59a0af?types=short_video"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"|5f9abb2e66afb7042d59a0af|short_video"}; !slices.Equal(*seen, want) {
		t.Fatalf("requests = %v, want %v", *seen, want)
	}
	if len(res.Entries) != 4 {
		t.Fatalf("entries = %d", len(res.Entries))
	}
	*seen = nil
	if _, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://dzen.ru/tass?types=article,long_video"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"tass||article", "tass||long_video"}; !slices.Equal(*seen, want) {
		t.Fatalf("requests = %v, want %v", *seen, want)
	}
}

func TestFetch_Errors(t *testing.T) {
	srv, _, status := exportStub(t)
	h := newHandler(srv)
	if _, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://dzen.ru/nobody"}); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("missing channel: %v", err)
	}
	if _, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://dzen.ru/a/slug"}); !errors.Is(err, errInvalidFeedURL) {
		t.Fatalf("article url: %v", err)
	}
	status.Store(http.StatusTooManyRequests)
	_, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://dzen.ru/tass"})
	at, ok := bridge.RetryAt(err)
	if !ok || time.Until(at) < 100*time.Second || time.Until(at) > 121*time.Second {
		t.Fatalf("429 must become a cooldown honouring Retry-After: %v %v", err, at)
	}
	status.Store(http.StatusInternalServerError)
	if _, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://dzen.ru/tass"}); err == nil || strings.Contains(err.Error(), "not found") {
		t.Fatalf("500: %v", err)
	}
	var nilHandler *Handler
	if _, err := nilHandler.Fetch(context.Background(), bridge.Request{FeedURL: "https://dzen.ru/tass"}); err == nil {
		t.Fatal("nil handler must fail")
	}
}

func TestDiscoverTitle(t *testing.T) {
	srv, seen, _ := exportStub(t)
	h := newHandler(srv)
	var _ bridge.TitleDiscoverer = h
	title, err := h.DiscoverTitle(context.Background(), "https://dzen.ru/tass")
	if err != nil || title != "ТАСС" {
		t.Fatalf("title = %q err %v", title, err)
	}
	if len(*seen) != 1 {
		t.Fatalf("discovery is one request, got %v", *seen)
	}
	if _, err := h.DiscoverTitle(context.Background(), "https://dzen.ru/nobody"); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestEntryFromItem_Edges(t *testing.T) {
	if _, ok := entryFromItem(exportItem{ID: "x", Link: "https://dzen.ru/tass?from=feed"}, ""); ok {
		t.Fatal("channel link is not a publication")
	}
	if _, ok := entryFromItem(exportItem{Link: "https://dzen.ru/a/slug"}, ""); ok {
		t.Fatal("item without id is skipped")
	}
	long := "Первое слово второе слово третье слово четвёртое слово пятое слово шестое слово седьмое слово восьмое слово девятое"
	e, ok := entryFromItem(exportItem{ID: "x", Type: "brief", Text: long, Link: "https://dzen.ru/b/slug?rid=1"}, "Автор")
	if !ok || e.URL != "https://dzen.ru/b/slug" || len([]rune(e.Title)) > 81 || e.Title[len(e.Title)-3:] != "…" {
		t.Fatalf("brief: %+v", e)
	}
	if e.PublishedAt != nil || !strings.Contains(e.Content, "<p>Первое слово") || strings.Contains(e.Content, "<img") {
		t.Fatalf("brief content: %+v", e)
	}
	ms := exportItem{ID: "y", Title: "t", Link: "https://dzen.ru/a/s", PublicationDate: "1791221749000"}
	e, _ = entryFromItem(ms, "")
	if e.PublishedAt == nil || e.PublishedAt.Unix() != 1791221749 || e.Author != nil {
		t.Fatalf("ms date / empty author: %+v", e)
	}
}
