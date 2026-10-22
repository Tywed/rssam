package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"rssam/internal/bridge/v1"
)

type cannedTransport struct {
	pages map[string]string
	got   []string
}

func (c *cannedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.got = append(c.got, r.URL.String())
	body, ok := c.pages[r.URL.String()]
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Request: r, Header: http.Header{}}, nil
}

func tgPage(ids []int, more string) string {
	var b strings.Builder
	b.WriteString(`<html><body><div class="tgme_channel_info"><div class="tgme_channel_info_header_title"><span>Chan</span></div></div>`)
	if more != "" {
		fmt.Fprintf(&b, `<div class="tgme_widget_message_centered js-messages_more_wrap"><a href="%s">More</a></div>`, more)
	}
	for _, id := range ids {
		fmt.Fprintf(&b, `<div class="tgme_widget_message_wrap js-widget_message_wrap"><div class="tgme_widget_message" data-post="chan/%d">`+
			`<div class="tgme_widget_message_text">msg %d</div>`+
			`<span class="tgme_widget_message_meta"><span class="tgme_widget_message_views">1K</span><a class="tgme_widget_message_date" href="https://t.me/chan/%d"><time datetime="2026-10-14T10:00:%02dZ"></time></a></span>`+
			`</div></div>`, id, id, id, id%60)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// First poll reads the full page and sets the cursor; later polls ask for
// messages after it, follow the "newer" link when more than a page
// arrived, and keep the cursor when nothing did. The proxy fields of the
// override survive, and a cursor from another channel is ignored.
func TestFetch_IncrementalAfterCursor(t *testing.T) {
	tr := &cannedTransport{pages: map[string]string{
		"https://t.me/s/chan":           tgPage([]int{531, 540, 552}, "/s/chan?before=531"),
		"https://t.me/s/chan?after=552": tgPage(nil, ""),
		"https://t.me/s/chan?after=540": tgPage([]int{541, 545}, "/s/chan?after=545"),
		"https://t.me/s/chan?after=545": tgPage([]int{552}, ""),
	}}
	h := NewHandler(nil, &http.Client{Transport: tr}, Config{UseProxy: false})
	ctx := context.Background()

	res, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://t.me/chan", State: json.RawMessage(`{"proxy_service_url":"http://p"}`)})
	if err != nil || len(res.Entries) != 3 {
		t.Fatalf("first poll: n=%d err=%v", len(res.Entries), err)
	}
	var o BridgeOverride
	if err := json.Unmarshal(res.State, &o); err != nil || o.LastMessageID != 552 || o.LastChannel != "chan" || o.ProxyServiceURL != "http://p" {
		t.Fatalf("state after first poll = %s err=%v", res.State, err)
	}

	res, err = h.Fetch(ctx, bridge.Request{FeedURL: "https://t.me/chan", State: res.State})
	if err != nil || len(res.Entries) != 0 {
		t.Fatalf("quiet poll: n=%d err=%v", len(res.Entries), err)
	}
	if tr.got[len(tr.got)-1] != "https://t.me/s/chan?after=552" {
		t.Fatalf("quiet poll fetched %s", tr.got[len(tr.got)-1])
	}
	if err := json.Unmarshal(res.State, &o); err != nil || o.LastMessageID != 552 {
		t.Fatalf("state after quiet poll = %s", res.State)
	}

	res, err = h.Fetch(ctx, bridge.Request{FeedURL: "https://t.me/chan", State: json.RawMessage(`{"last_message_id":540,"last_channel":"chan"}`)})
	if err != nil || len(res.Entries) != 3 {
		t.Fatalf("catch-up poll: n=%d err=%v", len(res.Entries), err)
	}
	if got := strings.Join(tr.got[len(tr.got)-2:], " "); got != "https://t.me/s/chan?after=540 https://t.me/s/chan?after=545" {
		t.Fatalf("catch-up requests: %s", got)
	}
	if err := json.Unmarshal(res.State, &o); err != nil || o.LastMessageID != 552 {
		t.Fatalf("state after catch-up = %s", res.State)
	}

	res, err = h.Fetch(ctx, bridge.Request{FeedURL: "https://t.me/chan", State: json.RawMessage(`{"last_message_id":9999,"last_channel":"other"}`)})
	if err != nil || len(res.Entries) != 3 || tr.got[len(tr.got)-1] != "https://t.me/s/chan" {
		t.Fatalf("foreign cursor must be ignored: n=%d err=%v url=%s", len(res.Entries), err, tr.got[len(tr.got)-1])
	}
}

func TestMessageID(t *testing.T) {
	for in, want := range map[string]int64{
		"https://t.me/chan/552":        552,
		"https://t.me/chan/552?single": 552,
		"https://t.me/chan/":           0,
		"https://t.me/chan/abc":        0,
		"":                             0,
	} {
		if got := MessageID(in); got != want {
			t.Fatalf("MessageID(%q)=%d want %d", in, got, want)
		}
	}
}

func TestParsePage_MessagesAndLinksInOnePass(t *testing.T) {
	body := []byte(tgPage([]int{10, 11}, "/s/chan?before=10"))
	page, err := ParsePage(body, "chan")
	if err != nil || len(page.Messages) != 2 || page.Older != "https://t.me/s/chan?before=10" || page.Newer != "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if older, ok := NextPageURL(body); !ok || older != page.Older {
		t.Fatalf("NextPageURL=%q", older)
	}
	body = []byte(tgPage([]int{12}, "https://t.me/s/chan?after=12"))
	page, err = ParsePage(body, "chan")
	if err != nil || len(page.Messages) != 1 || page.Newer != "https://t.me/s/chan?after=12" || page.Older != "" {
		t.Fatalf("after page=%+v err=%v", page, err)
	}
	if newer, ok := NewerPageURL(body); !ok || newer != page.Newer {
		t.Fatalf("NewerPageURL=%q", newer)
	}
	if _, err := ParsePage([]byte(`<html><body><div class="tgme_page">open in telegram</div></body></html>`), "chan"); !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("private channel page err=%v", err)
	}
}

func TestIsEmptyChannelPage(t *testing.T) {
	if !IsEmptyChannelPage([]byte(tgPage(nil, ""))) {
		t.Fatal("channel header without messages is an empty page")
	}
	// What t.me really answers to ?after=<newest id>: the placeholder sits
	// inside a message wrap.
	quiet := `<html><body><div class="tgme_channel_info"></div><section class="tgme_channel_history js-message_history">` +
		`<div class="tgme_widget_message_wrap js-widget_message_wrap"><div class="tgme_widget_message_centered"><div class="tme_no_messages_found">No posts found</div></div></div>` +
		`</section></body></html>`
	if !IsEmptyChannelPage([]byte(quiet)) {
		t.Fatal("the 'No posts found' page is an empty page")
	}
	if page, err := ParsePage([]byte(quiet), "chan"); err != nil || len(page.Messages) != 0 {
		t.Fatalf("parser on the quiet page: %+v %v", page, err)
	}
	if IsEmptyChannelPage([]byte(tgPage([]int{1}, ""))) {
		t.Fatal("a page with a message is not empty")
	}
	// A page without the channel header must still go through the parser,
	// which tells a private channel from a layout change.
	if IsEmptyChannelPage([]byte(`<html><body><div class="tgme_page">open in telegram</div></body></html>`)) {
		t.Fatal("unknown page must not be skipped")
	}
}

// An incremental poll of a channel that posted nothing must not reach
// the HTML parser, while a private channel is still reported.
func TestFetch_QuietIncrementalPollSkipsParse(t *testing.T) {
	tr := &cannedTransport{pages: map[string]string{
		"https://t.me/s/chan?after=552": tgPage(nil, ""),
		"https://t.me/s/gone?after=5":   `<html><body><div class="tgme_page">open in telegram</div></body></html>`,
	}}
	h := NewHandler(nil, &http.Client{Transport: tr}, Config{UseProxy: false})
	res, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://t.me/chan", State: json.RawMessage(`{"last_message_id":552,"last_channel":"chan"}`)})
	if err != nil || len(res.Entries) != 0 {
		t.Fatalf("quiet poll: n=%d err=%v", len(res.Entries), err)
	}
	var o BridgeOverride
	if err := json.Unmarshal(res.State, &o); err != nil || o.LastMessageID != 552 {
		t.Fatalf("cursor must survive a quiet poll: %s", res.State)
	}
	_, err = h.Fetch(context.Background(), bridge.Request{FeedURL: "https://t.me/gone", State: json.RawMessage(`{"last_message_id":5,"last_channel":"gone"}`)})
	if !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("private channel on an incremental poll: err=%v", err)
	}
}

func BenchmarkQuietPoll_ParsePage(b *testing.B) {
	body := []byte(tgPage(nil, ""))
	for b.Loop() {
		if _, err := ParsePage(body, "chan"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQuietPoll_IsEmptyChannelPage(b *testing.B) {
	body := []byte(tgPage(nil, ""))
	for b.Loop() {
		if !IsEmptyChannelPage(body) {
			b.Fatal("not empty")
		}
	}
}

func BenchmarkFullPage_ParseOnce(b *testing.B) {
	ids := make([]int, 20)
	for i := range ids {
		ids[i] = 500 + i
	}
	body := []byte(tgPage(ids, "/s/chan?before=500"))
	for b.Loop() {
		if _, err := ParsePage(body, "chan"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFullPage_ParseTwice(b *testing.B) {
	ids := make([]int, 20)
	for i := range ids {
		ids[i] = 500 + i
	}
	body := []byte(tgPage(ids, "/s/chan?before=500"))
	for b.Loop() {
		if _, err := ParseHTML(body, "chan"); err != nil {
			b.Fatal(err)
		}
		NextPageURL(body)
	}
}
