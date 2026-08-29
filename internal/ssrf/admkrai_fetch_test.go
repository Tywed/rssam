package ssrf_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"rssam/internal/reader"
	"rssam/internal/ssrf"
)

func TestAdmkraiRSSFetch_TLSInsecure(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	guard, err := ssrf.New(ssrf.Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client := guard.HTTPClient(15 * time.Second)
	f := reader.NewRSSFetcher(client, "rssam", guard, "")
	res, err := f.Fetch(context.Background(), "https://admkrai.krasnodar.ru/rss/", "", "", false, true)
	if err != nil {
		if strings.Contains(err.Error(), "certificate") {
			t.Fatalf("TLS should succeed with per-feed tls_insecure: %v", err)
		}
		t.Fatalf("fetch: %v", err)
	}
	if len(res.Entries) == 0 {
		t.Fatal("expected RSS entries")
	}
}
