package rutube

import "testing"

func TestParseChannelIDFromFeedURL(t *testing.T) {
	cases := map[string]string{
		"https://rutube.ru/video/person/26119699/": "26119699",
		"https://rutube.ru/channel/24129077/":      "24129077",
		"https://www.rutube.ru/video/person/42":    "42",
		"https://rutube.ru/?c=26119699":            "26119699",
		"rutube-person://26119699":                 "26119699",
	}
	for url, want := range cases {
		got, ok := ParseChannelIDFromFeedURL(url)
		if !ok || got != want {
			t.Fatalf("%s: got %q ok=%v want %q", url, got, ok, want)
		}
	}
}

func TestDetectFeedURL(t *testing.T) {
	if !DetectFeedURL("https://rutube.ru/video/person/1/") {
		t.Fatal("expected rutube url")
	}
	if DetectFeedURL("https://example.com/feed.xml") {
		t.Fatal("expected non-rutube url")
	}
}
