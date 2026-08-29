package telegram

import (
	_ "embed"
	"os"
	"strings"
	"testing"
)

//go:embed testdata/message.html
var fixtureMessageHTML []byte

//go:embed testdata/grouped_album.html
var fixtureGroupedAlbumHTML []byte

//go:embed testdata/grouped_videos.html
var fixtureGroupedVideosHTML []byte

func TestParseHTML_groupedAlbum(t *testing.T) {
	msgs, err := ParseHTML(fixtureGroupedAlbumHTML, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages=%d want 1 merged album", len(msgs))
	}
	m := msgs[0]
	if m.URI != "https://t.me/demo/102" {
		t.Fatalf("uri=%q want last album message uri", m.URI)
	}
	if m.Title != "Album caption here" {
		t.Fatalf("title=%q", m.Title)
	}
	if len(m.Enclosures) != 3 {
		t.Fatalf("enclosures=%d want 3", len(m.Enclosures))
	}
}

func TestParseHTML_groupedVideos(t *testing.T) {
	msgs, err := ParseHTML(fixtureGroupedVideosHTML, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages=%d want 1 grouped video album", len(msgs))
	}
	m := msgs[0]
	if !m.IsGroupedAlbum {
		t.Fatal("expected IsGroupedAlbum")
	}
	if len(m.Enclosures) < 4 {
		t.Fatalf("enclosures=%d want at least 4 (2 posters + 2 videos)", len(m.Enclosures))
	}
	if !strings.Contains(m.Content, "v1.mp4") || !strings.Contains(m.Content, "v2.mp4") {
		t.Fatalf("content missing videos: %q", m.Content)
	}
}

func TestParseHTML_oneMessage(t *testing.T) {
	body := fixtureMessageHTML
	if len(body) == 0 {
		b, err := os.ReadFile("testdata/message.html")
		if err != nil {
			t.Fatal(err)
		}
		body = b
	}
	msgs, err := ParseHTML(body, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages=%d want 1", len(msgs))
	}
	m := msgs[0]
	if m.URI != "https://t.me/demo/42" {
		t.Fatalf("uri=%q", m.URI)
	}
	if m.Title == "" {
		t.Fatal("expected title")
	}
	if m.Content == "" {
		t.Fatal("expected content")
	}
	if m.Timestamp.IsZero() {
		t.Fatal("expected timestamp")
	}
	if !m.HasViews {
		t.Fatal("single message should have views")
	}
}
