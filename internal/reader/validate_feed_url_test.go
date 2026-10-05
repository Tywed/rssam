package reader

import (
	"testing"

	"rssam/internal/ssrf"
)

func TestValidateFeedURL_BridgeSchemes(t *testing.T) {
	g, err := ssrf.New(ssrf.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{
		"dzen-news://краснодар",
		"dzen-search://golang",
		"vk-search://golang",
		"rutube-person://26119699",
		"dzen-channel://tass",
		"dzen-channel://id/5f9abb2e66afb7042d59a0af?types=article",
	}
	for _, url := range cases {
		if err := ValidateFeedURL(url, g); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
	}
}

func TestValidateFeedURL_DzenChannelScheme(t *testing.T) {
	if err := ValidateFeedURL("dzen-channel://a/slug", nil); err == nil {
		t.Fatal("article path must not pass as a channel")
	}
	if err := ValidateFeedURL("dzen-channel://tass?types=podcast", nil); err == nil {
		t.Fatal("unknown content type must be rejected")
	}
}

func TestValidateFeedURL_BlocksNonHTTPSchemes(t *testing.T) {
	g, err := ssrf.New(ssrf.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFeedURL("file:///etc/passwd", g); err == nil {
		t.Fatal("expected error for file scheme")
	}
}

func TestIsBridgeSchemeURL(t *testing.T) {
	if !IsBridgeSchemeURL("dzen-news://x") {
		t.Fatal("expected bridge scheme")
	}
	if IsBridgeSchemeURL("https://dzen.ru/news/search?text=x") {
		t.Fatal("https should not be bridge scheme")
	}
}
