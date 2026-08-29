package dzen

import (
	"testing"
)

const fixtureNeoHTML = `<html><script>window.Ya.Neo=window.Ya.Neo||{};window.Ya.Neo={"dataSource":{"news":{"stories":[{"docs":[{"title":[{"text":"Test headline"}],"text":[{"text":"Lead text about "},{"text":"Go"}],"time":"10 августа в 17:54","url":"https://example.com/news/1?utm_source=yxnews","sourceName":"Example Media","docId":"doc-1","studioDocumentStats":{"docUrl":"https://example.com/news/1","docPubDate":1786373691}}]}]}}};main()</script></html>`

func TestParseSearchHTML(t *testing.T) {
	items, err := ParseSearchHTML([]byte(fixtureNeoHTML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d", len(items))
	}
	item := items[0]
	if item.Title != "Test headline" {
		t.Fatalf("title=%q", item.Title)
	}
	if item.URL != "https://example.com/news/1" {
		t.Fatalf("url=%q", item.URL)
	}
	if item.PubDateUnix != 1786373691 {
		t.Fatalf("pubDate=%d", item.PubDateUnix)
	}
	if item.Author != "Example Media" {
		t.Fatalf("author=%q", item.Author)
	}
	if item.DocID != "doc-1" {
		t.Fatalf("docID=%q", item.DocID)
	}
	if item.Summary != "Lead text about Go" {
		t.Fatalf("summary=%q", item.Summary)
	}
}

func TestParseSearchHTML_MissingPayload(t *testing.T) {
	_, err := ParseSearchHTML([]byte("<html></html>"))
	if err != errNeoPayloadNotFound {
		t.Fatalf("err=%v", err)
	}
}
