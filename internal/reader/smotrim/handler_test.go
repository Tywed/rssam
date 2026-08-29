package smotrim_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/reader/smotrim"
)

func TestHandlerFetchFromBrandPage(t *testing.T) {
	const payload = `<script type="application/json">[["ShallowReactive",1],{"data":2},["Ref",3],{"publicId":4,"title":5,"videoType":6,"date":7,"brand":8,"description":9},6059963,"Тестовый сюжет","plot","2 часа назад",{"id":67725,"title":10},"Описание","Вести. Кубань"]</script>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/brand/67725" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("<!DOCTYPE html><html><body>" + payload + "</body></html>"))
	}))
	defer srv.Close()

	client, err := smotrim.NewClient(srv.Client(), nil, smotrim.Config{
		BrandBaseURL: srv.URL + "/brand",
		GraphQLURL:   srv.URL + "/graphql",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := smotrim.NewHandler(client, smotrim.Config{BrandBaseURL: srv.URL + "/brand"})

	res, err := h.Fetch(context.Background(), "smotrim://67725", smotrim.FetchState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	e := res.Entries[0]
	if e.Title != "Тестовый сюжет" {
		t.Fatalf("title=%q", e.Title)
	}
	if !strings.Contains(e.URL, "/video/6059963") {
		t.Fatalf("url=%q", e.URL)
	}
	if e.Author == nil || *e.Author != "Вести. Кубань" {
		t.Fatalf("author=%v", e.Author)
	}
	if e.PublishedAt == nil || e.PublishedAt.IsZero() {
		t.Fatal("expected published_at")
	}
	if time.Since(*e.PublishedAt) > 3*time.Hour {
		t.Fatalf("published_at too old: %v", e.PublishedAt)
	}
	if !strings.Contains(e.Content, "Смотреть на СМОТРИМ") {
		t.Fatalf("content=%q", e.Content)
	}
	if res.FeedTitle != "Smotrim: Вести. Кубань" {
		t.Fatalf("feed title=%q", res.FeedTitle)
	}
}
