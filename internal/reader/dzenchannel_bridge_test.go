package reader

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"rssam/internal/bridge/v1"
	"rssam/internal/reader/dzenchannel"
)

func TestDetectFeedTypeFromURL_DzenChannel(t *testing.T) {
	for _, u := range []string{"https://dzen.ru/tass", "https://dzen.ru/id/5f9abb2e66afb7042d59a0af", "dzen-channel://tass"} {
		if got := DetectFeedTypeFromURL(u); got != FeedTypeDzenChannel {
			t.Fatalf("%s: got %q", u, got)
		}
	}
	// The news search stays with the dzen_news bridge, an article is no feed at all.
	if got := DetectFeedTypeFromURL("https://dzen.ru/news/search?text=x"); got != FeedTypeDzenNews {
		t.Fatalf("news search: got %q", got)
	}
	if got := DetectFeedTypeFromURL("https://dzen.ru/a/asPgHIhyeVtzCmCb"); got != FeedTypeRSS {
		t.Fatalf("article: got %q", got)
	}
	if ValidateBridgeFeedURL("https://dzen.ru/tass", FeedTypeDzenChannel) != nil || ValidateBridgeFeedURL("https://dzen.ru/a/x", FeedTypeDzenChannel) == nil {
		t.Fatal("ValidateBridgeFeedURL dzen_channel")
	}
	if FeedTypeLabel(FeedTypeDzenChannel) != "Dzen" || NormalizeFeedType("dzen_channel") != FeedTypeDzenChannel {
		t.Fatal("label / normalize")
	}
}

func TestRegistry_DzenChannelIsContractBridge(t *testing.T) {
	bundle, err := NewRegistry(RegistryConfig{HTTPClient: http.DefaultClient})
	if err != nil {
		t.Fatal(err)
	}
	h, err := bundle.Registry.FindHandler("https://dzen.ru/tass", "")
	if err != nil || h.Name() != FeedTypeDzenChannel {
		t.Fatalf("FindHandler: %v %v", h, err)
	}
	if _, ok := h.(adapted); !ok {
		t.Fatalf("dzen_channel must be registered through Adapt, got %T", h)
	}
	if len(bundle.Contract) != 1 || bundle.Contract[0].Name() != FeedTypeDzenChannel {
		t.Fatalf("Contract = %v", bundle.Contract)
	}
}

func TestTitleResolver_ContractDiscoverer(t *testing.T) {
	body, err := os.ReadFile("dzenchannel/testdata/tass_article.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("channel_name") != "tass" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	h := dzenchannel.NewHandler(dzenchannel.NewClient(srv.Client(), nil, dzenchannel.Config{ExportURL: srv.URL}))
	r := &TitleResolver{contract: []bridge.Handler{h}}
	d, err := r.DiscoverFeed(context.Background(), "https://dzen.ru/tass", "", false)
	if err != nil || d.Title != "ТАСС" || d.FeedURL != "https://dzen.ru/tass" {
		t.Fatalf("DiscoverFeed = %+v %v", d, err)
	}
	if _, err := r.DiscoverFeed(context.Background(), "https://dzen.ru/nobody", FeedTypeDzenChannel, false); !errors.Is(err, dzenchannel.ErrChannelNotFound) {
		t.Fatalf("missing channel: %v", err)
	}
	// A type without a contract discoverer still falls through to RSS.
	if _, err := (&TitleResolver{}).DiscoverFeed(context.Background(), "https://example.com/feed", "", false); err == nil || err.Error() != "rss fetcher not configured" {
		t.Fatalf("fallback: %v", err)
	}
}
