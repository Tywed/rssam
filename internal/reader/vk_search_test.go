package reader

import "testing"

func TestDetectFeedTypeFromURL_VKSearch(t *testing.T) {
	if got := DetectFeedTypeFromURL("https://vk.com/feed?section=search&q=test"); got != FeedTypeVKSearch {
		t.Fatalf("got %q", got)
	}
	if got := DetectFeedTypeFromURL("vk-search://myquery"); got != FeedTypeVKSearch {
		t.Fatalf("got %q", got)
	}
}
