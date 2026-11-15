package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWebhookTemplate_JSONFunc(t *testing.T) {
	entry := Entry{Title: `He said "hi"` + "\nline2 <b>"}
	out, err := RenderWebhookBodyTemplate(`{"text": {{json .entry.Title}}, "feed": {{json .feed.Title}}}`, WebhookFeed{Title: "F"}, entry, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not JSON: %v: %s", err, out)
	}
	if got["text"] != entry.Title || got["feed"] != "F" {
		t.Fatalf("got %v", got)
	}
}

func TestWebhookTemplate_OutputLimit(t *testing.T) {
	entry := Entry{Content: strings.Repeat("x", 300<<10)}
	_, err := RenderWebhookBodyTemplate(`{{.entry.Content}}{{.entry.Content}}{{.entry.Content}}{{.entry.Content}}`, WebhookFeed{}, entry, nil, nil)
	if !errors.Is(err, errWebhookTemplateTooLarge) {
		t.Fatalf("expected size error, got %v", err)
	}
	if _, err := RenderWebhookBodyTemplate(`{{.entry.Content}}{{.entry.Content}}{{.entry.Content}}`, WebhookFeed{}, entry, nil, nil); err != nil {
		t.Fatalf("900 KiB must pass: %v", err)
	}
}

func TestWebhookTemplate_RejectsNested(t *testing.T) {
	for _, tpl := range []string{
		`{{define "a"}}xx{{end}}{{template "a"}}`,
		`{{template "missing"}}`,
		`{{if true}}{{template "x"}}{{end}}`,
		`{{block "b" .}}x{{end}}`,
	} {
		if err := ValidateWebhookBodyTemplate(tpl); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("%q: expected rejection, got %v", tpl, err)
		}
	}
}

func TestValidateWebhookBodyTemplate(t *testing.T) {
	ok := []string{
		``,
		`{{.entry.Title}} — {{.feed.Title}} {{.filter.Name}} {{.payload}}`,
		`{{range .items}}{{.entry.Title}}{{end}} {{.count}}`,
		`{{html .entry.Title}} {{urlquery .entry.URL}} {{json .entry}}`,
	}
	for _, tpl := range ok {
		if err := ValidateWebhookBodyTemplate(tpl); err != nil {
			t.Fatalf("%q: %v", tpl, err)
		}
	}
	bad := map[string]string{
		`{{.entry.Titel}}`: "Titel",
		`{{.entry.Title}`:  "invalid body_template",
		`{{.nope}}`:        "map has no entry",
		`{{unknownfn 1}}`:  "not defined",
	}
	for tpl, want := range bad {
		err := ValidateWebhookBodyTemplate(tpl)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: want error containing %q, got %v", tpl, want, err)
		}
	}
}

func TestWebhookTemplate_Cached(t *testing.T) {
	a, err := parseBodyTemplate(`cache {{.entry.Title}}`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := parseBodyTemplate(`cache {{.entry.Title}}`)
	if a != b {
		t.Fatal("expected the parsed template to be reused")
	}
}
