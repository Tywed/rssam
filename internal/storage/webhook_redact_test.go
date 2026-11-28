package storage

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRedactWebhookError(t *testing.T) {
	const token = "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
	ue := &url.Error{Op: "Post", URL: "http://192.168.0.112:8081/bot" + token + "/sendMessage", Err: errors.New("dial tcp 192.168.0.112:8081: connect: connection refused")}
	got := RedactWebhookError(ue)
	if strings.Contains(got, token) {
		t.Fatalf("token leaked: %s", got)
	}
	if got != "Post http://192.168.0.112:8081/bot***/sendMessage: dial tcp 192.168.0.112:8081: connect: connection refused" {
		t.Fatalf("got %q", got)
	}
	// Wrapped, and a plain error that happens to quote the URL.
	if got := RedactWebhookError(errors.New("x: " + ue.Error())); strings.Contains(got, token) {
		t.Fatalf("plain error leaked: %s", got)
	}
	wrapped := errors.New("send: " + ue.Error())
	if got := RedactWebhookError(wrapped); strings.Contains(got, token) || !strings.Contains(got, "/bot***/") {
		t.Fatalf("wrapped: %s", got)
	}
	if got := RedactWebhookURL("https://hook.example/in?key=secret&x=1"); got != "https://hook.example/in" {
		t.Fatalf("query must go: %s", got)
	}
	if got := RedactWebhookURL("https://user:pw@hook.example/in"); got != "https://hook.example/in" {
		t.Fatalf("userinfo must go: %s", got)
	}
	if RedactWebhookError(nil) != "" || RedactWebhookError(errors.New("plain")) != "plain" {
		t.Fatal("plain errors pass through")
	}
}

func TestRedactFeedError(t *testing.T) {
	inner := &url.Error{Op: "Get", URL: "https://user:***@feeds.example/private.xml?token=abc&x=1", Err: errors.New("dial tcp: connection refused")}
	err := fmt.Errorf("fetch feed: http request: %w", inner)
	got := RedactFeedError(err)
	want := `fetch feed: http request: Get "https://feeds.example/private.xml": dial tcp: connection refused`
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	plain := fmt.Errorf("fetch feed: %w", &url.Error{Op: "Get", URL: "https://feeds.example/a.xml", Err: errors.New("EOF")})
	if got := RedactFeedError(plain); got != plain.Error() {
		t.Fatalf("url without secrets changed: %q", got)
	}
	if got := RedactFeedError(errors.New("unexpected status code: 404")); got != "unexpected status code: 404" {
		t.Fatalf("plain text changed: %q", got)
	}
}
