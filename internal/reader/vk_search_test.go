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

func TestDetectFeedTypeFromURL_VKGroup(t *testing.T) {
	for _, u := range []string{"https://vk.com/tass", "https://vk.com/public36441046", "vk-group://durov"} {
		if got := DetectFeedTypeFromURL(u); got != FeedTypeVKGroup {
			t.Fatalf("%s: got %q", u, got)
		}
	}
	if got := DetectFeedTypeFromURL("https://vk.com/feed?section=search&q=test"); got != FeedTypeVKSearch {
		t.Fatalf("search url got %q", got)
	}
	if err := ValidateBridgeFeedURL("https://vk.com/wall-1_2", FeedTypeVKGroup); err == nil {
		t.Fatal("post link must not validate as a wall")
	}
}
