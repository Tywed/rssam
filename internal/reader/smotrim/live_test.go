package smotrim

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveSmotrimBrandFetch(t *testing.T) {
	if os.Getenv("SMOTRIM_LIVE") != "1" {
		t.Skip("set SMOTRIM_LIVE=1 to run")
	}
	client, err := NewClient(nil, nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(client, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := h.fetch(ctx, "smotrim://67725?limit=5&type=plot", FetchState{})
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
