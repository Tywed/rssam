package dzen

import "testing"

func TestParseQueryFromFeedURL(t *testing.T) {
	cases := map[string]string{
		"dzen-news://golang":             "golang",
		"dzen-search://machine learning": "machine learning",
		"https://dzen.ru/news/search?issue_tld=ru&sortby=date&text=python": "python",
		"https://www.dzen.ru/news/search?text=Go%20lang":                   "Go lang",
	}
	for url, want := range cases {
		got, ok := ParseQueryFromFeedURL(url)
		if !ok || got != want {
			t.Fatalf("%s: got %q ok=%v want %q", url, got, ok, want)
		}
	}
}

func TestDetectFeedURL(t *testing.T) {
	if !DetectFeedURL("dzen-news://news") {
		t.Fatal("expected dzen url")
	}
	if DetectFeedURL("https://example.com/feed.xml") {
		t.Fatal("expected non-dzen url")
	}
}
