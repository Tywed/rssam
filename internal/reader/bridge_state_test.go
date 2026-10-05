package reader

import (
	"encoding/json"
	"testing"
)

func TestBridgeState_KeepsUnknownKeys(t *testing.T) {
	raw := []byte(`{"telegram":{"max_pages":3},"dzen_channel":{"id":"abc","n":2},"future":[1,2]}`)
	st := ParseBridgeState(raw)
	if st.Telegram == nil || st.Telegram.MaxPages != 3 {
		t.Fatalf("typed field lost: %+v", st.Telegram)
	}
	if string(st.Raw("dzen_channel")) != `{"id":"abc","n":2}` {
		t.Fatalf("Raw(dzen_channel) = %s", st.Raw("dzen_channel"))
	}
	if string(st.Raw("telegram")) != `{"max_pages":3}` {
		t.Fatalf("Raw(telegram) = %s", st.Raw("telegram"))
	}
	if st.Raw("missing") != nil {
		t.Fatalf("Raw(missing) = %s", st.Raw("missing"))
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(st.Marshal(), &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != 3 || string(back["future"]) != `[1,2]` || string(back["telegram"]) != `{"max_pages":3}` {
		t.Fatalf("round trip = %s", st.Marshal())
	}
}

func TestBridgeState_IsEmptyAndWith(t *testing.T) {
	var st BridgeState
	if !st.IsEmpty() || string(st.Marshal()) != "{}" {
		t.Fatalf("zero state: empty=%v marshal=%s", st.IsEmpty(), st.Marshal())
	}
	if ParseBridgeState(nil).IsEmpty() != true || ParseBridgeState([]byte(`{}`)).IsEmpty() != true {
		t.Fatal("nil / {} must be empty")
	}
	with := st.With("x", json.RawMessage(`{"a":1}`))
	if with.IsEmpty() || string(with.Marshal()) != `{"x":{"a":1}}` {
		t.Fatalf("With = %s", with.Marshal())
	}
	if !st.IsEmpty() {
		t.Fatal("With must not mutate the receiver")
	}
	cleared := with.With("x", nil)
	if !cleared.IsEmpty() {
		t.Fatalf("With(nil) should drop the key: %s", cleared.Marshal())
	}
	// A typed document survives next to an extra one.
	tg := ParseBridgeState([]byte(`{"telegram":{"max_pages":2}}`)).With("x", json.RawMessage(`1`))
	if string(tg.Marshal()) != `{"telegram":{"max_pages":2},"x":1}` {
		t.Fatalf("typed+extra = %s", tg.Marshal())
	}
}
