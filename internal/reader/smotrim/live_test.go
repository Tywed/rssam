package smotrim_test

import (
	"context"
	"os"
	"testing"
	"time"

	"rssam/internal/reader/smotrim"
)

func TestLiveSmotrimBrandFetch(t *testing.T) {
	if os.Getenv("SMOTRIM_LIVE") != "1" {
		t.Skip("set SMOTRIM_LIVE=1 to run")
	}
	client, err := smotrim.NewClient(nil, nil, smotrim.Config{})
	if err != nil {
		t.Fatal(err)
	}
	h := smotrim.NewHandler(client, smotrim.Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := h.Fetch(ctx, "smotrim://67725?limit=5&type=plot", smotrim.FetchState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) == 0 {
		t.Fatal("expected entries")
	}
	if res.FeedTitle == "" {
		t.Fatal("expected feed title")
	}
}
