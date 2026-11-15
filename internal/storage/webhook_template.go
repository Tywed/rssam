package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"
	"time"
)

// WebhookTemplateMaxBytes caps one rendered body: a template cannot grow a
// message past what any receiver accepts, whatever the entry contains.
const WebhookTemplateMaxBytes = 1 << 20

var errWebhookTemplateTooLarge = fmt.Errorf("render body_template: output exceeds %d bytes", WebhookTemplateMaxBytes)

// webhookTemplateFuncs extends the text/template builtins (html, urlquery,
// js, printf, ...) with json: the argument as a JSON value, quotes
// included, so a title with a quote or newline stays valid inside a JSON
// body: {"text": {{json .entry.Title}}}.
var webhookTemplateFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	},
}

var webhookTemplates = struct {
	sync.Mutex
	m map[string]*template.Template
}{m: make(map[string]*template.Template)}

// webhookTemplateCacheMax bounds the cache: templates live in webhook rows,
// so entries beyond this only come from edits; the cache is then rebuilt.
const webhookTemplateCacheMax = 256

func parseBodyTemplate(bodyTemplate string) (*template.Template, error) {
	webhookTemplates.Lock()
	tmpl, ok := webhookTemplates.m[bodyTemplate]
	webhookTemplates.Unlock()
	if ok {
		return tmpl, nil
	}
	tmpl, err := template.New("webhook_body").Option("missingkey=error").Funcs(webhookTemplateFuncs).Parse(bodyTemplate)
	if err != nil {
		return nil, fmt.Errorf("invalid body_template: %w", err)
	}
	if err := rejectNestedTemplates(tmpl); err != nil {
		return nil, err
	}
	webhookTemplates.Lock()
	if len(webhookTemplates.m) >= webhookTemplateCacheMax {
		webhookTemplates.m = make(map[string]*template.Template)
	}
	webhookTemplates.m[bodyTemplate] = tmpl
	webhookTemplates.Unlock()
	return tmpl, nil
}

// rejectNestedTemplates refuses {{define}}/{{template}}/{{block}}: a body is
// one template, and nested invocations are the only way to make output
// exponential in the template size.
func rejectNestedTemplates(tmpl *template.Template) error {
	if len(tmpl.Templates()) > 1 {
		return errors.New("invalid body_template: define is not allowed")
	}
	if tmpl.Tree != nil && hasTemplateNode(tmpl.Root) {
		return errors.New("invalid body_template: template/block is not allowed")
	}
	return nil
}

func hasTemplateNode(n parse.Node) bool {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return false
		}
		for _, c := range n.Nodes {
			if hasTemplateNode(c) {
				return true
			}
		}
	case *parse.TemplateNode:
		return true
	case *parse.IfNode:
		return hasTemplateNode(n.List) || hasTemplateNode(n.ElseList)
	case *parse.RangeNode:
		return hasTemplateNode(n.List) || hasTemplateNode(n.ElseList)
	case *parse.WithNode:
		return hasTemplateNode(n.List) || hasTemplateNode(n.ElseList)
	}
	return false
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errWebhookTemplateTooLarge
	}
	return b.Buffer.Write(p)
}

func executeBodyTemplate(tmpl *template.Template, data any) ([]byte, error) {
	buf := &limitedBuffer{limit: WebhookTemplateMaxBytes}
	if err := tmpl.Execute(buf, data); err != nil {
		if errors.Is(err, errWebhookTemplateTooLarge) {
			return nil, errWebhookTemplateTooLarge
		}
		return nil, fmt.Errorf("render body_template: %w", err)
	}
	return buf.Bytes(), nil
}

func RenderWebhookBodyTemplate(bodyTemplate string, feed WebhookFeed, entry Entry, filter *Filter, payloadJSON []byte) ([]byte, error) {
	tmpl, err := parseBodyTemplate(bodyTemplate)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"entry":  entry,
		"feed":   feed,
		"filter": filter,
	}
	if payloadJSON != nil {
		data["payload"] = string(payloadJSON)
	}
	return executeBodyTemplate(tmpl, data)
}

// ValidateWebhookBodyTemplate parses and trial-renders a template on save,
// so a typo in a field name is reported to the author instead of killing
// every delivery of the webhook. A body may be written for single entries
// (.entry/.feed/.filter) or for an HTTP digest (.items/.count); it passes
// if either shape renders.
func ValidateWebhookBodyTemplate(bodyTemplate string) error {
	if strings.TrimSpace(bodyTemplate) == "" {
		return nil
	}
	tmpl, err := parseBodyTemplate(bodyTemplate)
	if err != nil {
		return err
	}
	author := "author"
	now := time.Now()
	entry := Entry{ID: 1, FeedID: 1, Title: "Title", URL: "https://example.com/post", Content: "<p>Content</p>", Author: &author, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}
	feed := WebhookFeed{ID: 1, Title: "Feed", StoreMode: "full"}
	filter := &Filter{ID: 1, Name: "Filter", Enabled: true}
	single := map[string]any{"entry": entry, "feed": feed, "filter": filter, "payload": "{}"}
	if _, errSingle := executeBodyTemplate(tmpl, single); errSingle == nil {
		return nil
	} else if errors.Is(errSingle, errWebhookTemplateTooLarge) {
		return errSingle
	} else {
		items := []map[string]any{{"entry": entry, "feed": feed}}
		digest := map[string]any{"items": items, "count": 1, "payload": "{}"}
		if _, errDigest := executeBodyTemplate(tmpl, digest); errDigest == nil {
			return nil
		}
		return errSingle
	}
}
