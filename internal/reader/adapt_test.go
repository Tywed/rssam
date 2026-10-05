package reader

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"rssam/internal/bridge/v1"
)

type fakeBridge struct {
	gotReq bridge.Request
	res    bridge.Response
	err    error
}

func (f *fakeBridge) Name() string { return "fake" }

func (f *fakeBridge) DetectFeedType(feedURL string) string {
	if feedURL == "fake://ok" {
		return "fake"
	}
	return ""
}

func (f *fakeBridge) Fetch(_ context.Context, req bridge.Request) (bridge.Response, error) {
	f.gotReq = req
	return f.res, f.err
}

func TestAdapt_PassesStateAndMeta(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	fb := &fakeBridge{res: bridge.Response{
		Entries:      []bridge.Entry{{Title: "a", URL: "https://x/1", Hash: "h"}},
		ETag:         "e",
		LastModified: "lm",
		State:        json.RawMessage(`{"cursor":2}`),
		MinNextCheck: when,
		NewURL:       "https://x/new",
	}}
	h := Adapt(fb)
	if h.Name() != "fake" || h.DetectFeedType("fake://ok") != "fake" || h.DetectFeedType("https://x") != "" {
		t.Fatal("Name/DetectFeedType must be forwarded")
	}
	prev := ParseBridgeState([]byte(`{"telegram":{"max_pages":1},"fake":{"cursor":1}}`))
	res, err := h.Fetch(context.Background(), FetchRequest{
		FeedURL: "fake://ok", FeedType: "fake", UserAgent: "ua", ETag: "e0", LastModified: "lm0",
		BridgeState: prev, FetchViaProxy: true, TLSInsecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := bridge.Request{FeedURL: "fake://ok", FeedType: "fake", UserAgent: "ua", ETag: "e0", LastModified: "lm0",
		State: json.RawMessage(`{"cursor":1}`), FetchViaProxy: true, TLSInsecure: true}
	if string(fb.gotReq.State) != string(want.State) {
		t.Fatalf("bridge got state %s, want %s", fb.gotReq.State, want.State)
	}
	fb.gotReq.State, want.State = nil, nil
	if !reflect.DeepEqual(fb.gotReq, want) {
		t.Fatalf("bridge got %+v, want %+v", fb.gotReq, want)
	}
	if len(res.Entries) != 1 || res.ETag != "e" || res.LastModified != "lm" || !res.MinNextCheck.Equal(when) || res.NewURL != "https://x/new" {
		t.Fatalf("response not forwarded: %+v", res)
	}
	if got := string(res.BridgeState.Marshal()); got != `{"fake":{"cursor":2},"telegram":{"max_pages":1}}` {
		t.Fatalf("stored state = %s", got)
	}
}

func TestAdapt_NilStateKeepsPrevious(t *testing.T) {
	fb := &fakeBridge{err: errors.New("boom")}
	prev := ParseBridgeState([]byte(`{"fake":{"cursor":7}}`))
	res, err := Adapt(fb).Fetch(context.Background(), FetchRequest{FeedURL: "fake://ok", BridgeState: prev})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
	if got := string(res.BridgeState.Marshal()); got != `{"fake":{"cursor":7}}` {
		t.Fatalf("state after nil response = %s", got)
	}
	// First poll, nothing stored, nothing returned: stays empty (NULL in DB).
	res, _ = Adapt(&fakeBridge{}).Fetch(context.Background(), FetchRequest{FeedURL: "fake://ok"})
	if !res.BridgeState.IsEmpty() {
		t.Fatalf("expected empty state, got %s", res.BridgeState.Marshal())
	}
}
