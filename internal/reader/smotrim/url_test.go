package smotrim_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"rssam/internal/reader/smotrim"
)

func TestParseOptionsFromFeedURL(t *testing.T) {
	tests := map[string]smotrim.FeedOptions{
		"smotrim://67725": {
			BrandID: "67725",
		},
		"smotrim://67725?limit=30&type=plot": {
			BrandID:   "67725",
			Limit:     30,
			VideoType: "plot",
		},
		"https://smotrim.ru/brand/67725": {
			BrandID: "67725",
		},
		"https://smotrim.ru/brand/67725/": {
			BrandID: "67725",
		},
	}
	for in, want := range tests {
		got, ok := smotrim.ParseOptionsFromFeedURL(in)
		if !ok {
			t.Fatalf("%q: expected ok", in)
		}
		if got.BrandID != want.BrandID || got.Limit != want.Limit || got.VideoType != want.VideoType {
			t.Fatalf("%q: got %+v want %+v", in, got, want)
		}
	}
}

func TestParsePublishedTime(t *testing.T) {
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, smotrim.MoscowLocation)
	cases := map[string]bool{
		"6 часов назад":            true,
		"30 минут назад":           true,
		"2026-08-12T11:30:38+0300": true,
	}
	for raw, wantOK := range cases {
		got := smotrim.ParsePublishedTime(raw, now)
		if wantOK && got.IsZero() {
			t.Fatalf("%q: zero time", raw)
		}
	}
}

func TestExtractVideosFromBrandHTML(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "brand_page.html"))
	if err != nil {
		t.Skip("testdata not available:", err)
	}
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, smotrim.MoscowLocation)
	items, brandTitle, err := smotrim.ExtractVideosFromBrandHTML(body, now, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if brandTitle == "" {
		t.Fatal("expected brand title")
	}
	if len(items) == 0 {
		t.Fatal("expected videos")
	}
	if items[0].PublicID <= 0 || items[0].Title == "" {
		t.Fatalf("unexpected first item: %+v", items[0])
	}
}
