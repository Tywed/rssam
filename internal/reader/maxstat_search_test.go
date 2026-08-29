package reader

import "testing"

func TestDetectFeedTypeFromURL_Maxstat(t *testing.T) {
	if got := DetectFeedTypeFromURL("https://maxstat.ru/posts?search=test"); got != FeedTypeMaxstat {
		t.Fatalf("got %q want %q", got, FeedTypeMaxstat)
	}
	if got := DetectFeedTypeFromURL("maxstat-search://myquery"); got != FeedTypeMaxstat {
		t.Fatalf("got %q want %q", got, FeedTypeMaxstat)
	}
}
