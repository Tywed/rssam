package storage

import (
	"errors"
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
