package reader

import "testing"

func TestDetectFeedTypeFromURL_DzenNews(t *testing.T) {
	if got := DetectFeedTypeFromURL("dzen-news://python"); got != FeedTypeDzenNews {
		t.Fatalf("got %q want %q", got, FeedTypeDzenNews)
	}
	if got := DetectFeedTypeFromURL("https://dzen.ru/news/search?text=go"); got != FeedTypeDzenNews {
		t.Fatalf("got %q want %q", got, FeedTypeDzenNews)
	}
}
