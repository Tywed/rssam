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
	}
	for _, url := range cases {
		if err := ValidateFeedURL(url, g); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
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
