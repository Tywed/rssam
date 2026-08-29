package githubrel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Tywed/rssam/releases/latest" {
			t.Fatalf("path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "v1.2.3",
			"html_url":     "https://github.com/Tywed/rssam/releases/tag/v1.2.3",
			"body":         "notes",
			"published_at": time.Now().UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()
	c := New("Tywed/rssam")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	rel, err := c.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v1.2.3" {
		t.Fatalf("tag %s", rel.Tag)
	}
}
