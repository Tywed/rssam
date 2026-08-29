package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GetProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("target"); got != "https://t.me/s/demo" {
			t.Fatalf("target=%q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "p1", "host": "127.0.0.1", "port": 1080, "username": "u", "password": "p",
		})
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.Client())
	p, err := c.GetProxy(context.Background(), srv.URL, "https://t.me/s/demo")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "p1" || p.Host != "127.0.0.1" || p.Port != 1080 || p.Username != "u" {
		t.Fatalf("proxy=%+v", p)
	}
}

func TestClient_WorkingProxy_reusesUntilBad(t *testing.T) {
	var gets int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxy":
			gets++
			id := "p1"
			if gets > 1 {
				id = "p2"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "host": "127.0.0.1", "port": 1080,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/proxy/bad":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.Client())
	ctx := context.Background()
	a, err := c.WorkingProxy(ctx, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.WorkingProxy(ctx, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if gets != 1 || a.ID != "p1" || b.ID != "p1" {
		t.Fatalf("gets=%d a=%s b=%s", gets, a.ID, b.ID)
	}
	if err := c.ReportBadProxy(ctx, srv.URL, "p1", "timeout"); err != nil {
		t.Fatal(err)
	}
	d, err := c.WorkingProxy(ctx, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if gets != 2 || d.ID != "p2" {
		t.Fatalf("after bad: gets=%d id=%s", gets, d.ID)
	}
}

func TestClient_ReportBadProxy(t *testing.T) {
	var posted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/proxy/bad" {
			posted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.Client())
	if err := c.ReportBadProxy(context.Background(), srv.URL, "p1", "timeout"); err != nil {
		t.Fatal(err)
	}
	if !posted {
		t.Fatal("expected POST /proxy/bad")
	}
}

func TestClient_ReportBadProxy_Debounces(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/proxy/bad" {
			posts++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.Client())
	ctx := context.Background()
	if err := c.ReportBadProxy(ctx, srv.URL, "p1", "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := c.ReportBadProxy(ctx, srv.URL, "p1", "timeout"); err != nil {
		t.Fatal(err)
	}
	if posts != 1 {
		t.Fatalf("posts=%d want 1", posts)
	}
}
