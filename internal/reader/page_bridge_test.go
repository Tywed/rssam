package reader

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/reader/page"
)

func TestPageBridge_RegistryRoundTrip(t *testing.T) {
	body := `<html><head><title>T</title></head><body><div id="c"><p onclick="x()">v1</p><script>evil()</script></div></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
	defer ts.Close()
	reg := NewHandlerRegistry(newPageHandler(page.NewHandler(ts.Client(), nil, "t")))
	feedURL := "page+" + ts.URL + "/##c"
	if DetectFeedTypeFromURL(feedURL) != FeedTypePage || !IsBridgeSchemeURL(feedURL) || FeedTypeLabel(FeedTypePage) != "Страница" {
		t.Fatal("page url must be detected as a bridge scheme")
	}
	if err := ValidateFeedURL(feedURL, nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateFeedURL("page+https://", nil); err == nil {
		t.Fatal("hostless page url must be rejected")
	}

	res, err := reg.Fetch(context.Background(), FetchRequest{FeedURL: feedURL, FeedType: FeedTypePage})
	if err != nil || res.NotModified || len(res.Entries) != 1 || res.BridgeState.Page == nil || res.BridgeState.Page.Hash == "" {
		t.Fatalf("first fetch: %+v err=%v", res, err)
	}
	if c := res.Entries[0].Content; strings.Contains(c, "onclick") || strings.Contains(c, "evil") || !strings.Contains(c, "v1") {
		t.Fatalf("content must be sanitized: %s", c)
	}
	st := res.BridgeState
	if b := st.Marshal(); !strings.Contains(string(b), `"page":{"hash":`) {
		t.Fatalf("bridge state json: %s", b)
	}
	if got := ParseBridgeState(st.Marshal()); got.Page == nil || got.Page.Hash != st.Page.Hash {
		t.Fatal("bridge state must round-trip through JSON")
	}

	again, err := reg.Fetch(context.Background(), FetchRequest{FeedURL: feedURL, FeedType: FeedTypePage, BridgeState: st})
	if err != nil || !again.NotModified || len(again.Entries) != 0 {
		t.Fatalf("unchanged page must be NotModified: %+v err=%v", again, err)
	}
	body = strings.Replace(body, "v1", "v2", 1)
	changed, err := reg.Fetch(context.Background(), FetchRequest{FeedURL: feedURL, FeedType: FeedTypePage, BridgeState: st})
	if err != nil || changed.NotModified || len(changed.Entries) != 1 || changed.BridgeState.Page.Hash == st.Page.Hash {
		t.Fatalf("changed page: %+v err=%v", changed, err)
	}
}
