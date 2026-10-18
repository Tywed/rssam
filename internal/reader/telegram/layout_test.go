package telegram

import (
	"errors"
	"os"
	"testing"

	"rssam/internal/bridge/v1"
)

// A channel page with the header and no posts is an honest zero; t.me's
// stub page is "channel unavailable"; anything else is unknown markup.
func TestParseHTML_ClassifiesPagesWithoutMessages(t *testing.T) {
	msgs, err := ParseHTML([]byte(`<html><body><div class="tgme_channel_info"><div class="tgme_channel_info_header_title"><span>Chan</span></div></div></body></html>`), "chan")
	if err != nil || len(msgs) != 0 {
		t.Fatalf("header only: n=%d err=%v", len(msgs), err)
	}

	stub, err := os.ReadFile("testdata/stub_page.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseHTML(stub, "chan"); !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("stub page: err=%v", err)
	}
	if _, ok := bridge.AsLayout(err); ok {
		t.Fatal("stub page must not count as a layout change")
	}

	for _, body := range []string{
		`<html><body><div class="captcha">Are you human?</div></body></html>`,
		`<html><body><div class="tgme_channel_info_header_title"><span>Chan</span></div></body></html>`,
		``,
	} {
		_, err := ParseHTML([]byte(body), "chan")
		le, ok := bridge.AsLayout(err)
		if !ok || le.Bridge != "telegram" || le.Marker != "tgme_channel_info" {
			t.Fatalf("%q: err=%v", body, err)
		}
	}
}
