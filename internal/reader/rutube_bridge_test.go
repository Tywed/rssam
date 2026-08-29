package reader

import "testing"

func TestDetectFeedTypeFromURL_Rutube(t *testing.T) {
	if got := DetectFeedTypeFromURL("https://rutube.ru/video/person/26119699/"); got != FeedTypeRutube {
		t.Fatalf("got %q", got)
	}
	if got := DetectFeedTypeFromURL("rutube-person://42"); got != FeedTypeRutube {
		t.Fatalf("got %q", got)
	}
}
