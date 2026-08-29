package reader

import "testing"

func TestDetectFeedTypeFromURLSmotrim(t *testing.T) {
	if got := DetectFeedTypeFromURL("smotrim://67725"); got != FeedTypeSmotrim {
		t.Fatalf("got %q want %q", got, FeedTypeSmotrim)
	}
	if got := DetectFeedTypeFromURL("https://smotrim.ru/brand/67725"); got != FeedTypeSmotrim {
		t.Fatalf("got %q want %q", got, FeedTypeSmotrim)
	}
}

func TestValidateBridgeFeedURLSmotrim(t *testing.T) {
	if err := ValidateBridgeFeedURL("smotrim://67725", FeedTypeSmotrim); err != nil {
		t.Fatal(err)
	}
	if err := ValidateBridgeFeedURL("https://example.com/feed", FeedTypeSmotrim); err == nil {
		t.Fatal("expected error for invalid smotrim url")
	}
}
