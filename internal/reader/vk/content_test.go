package vk

import (
	"strings"
	"testing"

	"rssam/internal/model"
)

func TestDedupKeyHash(t *testing.T) {
	key := DedupKey(-12345, 42)
	if key != "-12345_42" {
		t.Fatalf("key=%q", key)
	}
	h := model.DedupHashFromString(key)
	if len(h) != 64 {
		t.Fatalf("hash len=%d", len(h))
	}
	h2 := model.DedupHashFromURL("https://vk.com/wall-12345_42")
	if h == h2 {
		t.Fatal("url hash should differ from owner_post key hash")
	}
}

func TestBuildContentHTML_Attachment(t *testing.T) {
	post := wallPost{
		Text:    "caption",
		OwnerID: -1,
		Attachments: []attachment{{
			Type: "photo",
			Photo: &photoAttach{
				Sizes: []photoSize{{URL: "https://example.com/p.jpg", Width: 500}},
			},
		}},
	}
	html := BuildContentHTML(post, nil)
	if !strings.Contains(html, "example.com/p.jpg") {
		t.Fatalf("missing img url: %s", html)
	}
	if !strings.Contains(html, "caption") {
		t.Fatalf("missing text: %s", html)
	}
}
