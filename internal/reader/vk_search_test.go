package reader

import "testing"

func TestDetectFeedTypeFromURL_VK(t *testing.T) {
	for _, u := range []string{
		"https://vk.com/feed?section=search&q=test", "vk-search://myquery",
		"https://vk.com/tass", "https://vk.com/public36441046", "vk-group://durov",
	} {
		if got := DetectFeedTypeFromURL(u); got != FeedTypeVK {
			t.Fatalf("%s: got %q", u, got)
		}
	}
	if err := ValidateBridgeFeedURL("https://vk.com/wall-1_2", FeedTypeVK); err == nil {
		t.Fatal("post link must not validate as a VK feed")
	}
	if err := ValidateBridgeFeedURL("vk-search://golang", FeedTypeVK); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"vk_search", "vk_group", "vk"} {
		if got := NormalizeFeedType(legacy); got != FeedTypeVK {
			t.Fatalf("%s: normalized to %q", legacy, got)
		}
	}
}
