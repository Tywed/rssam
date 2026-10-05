package bridge

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

type cooldown struct{ at time.Time }

func (c cooldown) Error() string      { return "cooldown" }
func (c cooldown) RetryAt() time.Time { return c.at }

func TestRetryAt(t *testing.T) {
	at := time.Now().Add(time.Minute)
	if got, ok := RetryAt(fmt.Errorf("wrap: %w", cooldown{at})); !ok || !got.Equal(at) {
		t.Fatalf("RetryAt through wrap = %v %v", got, ok)
	}
	if _, ok := RetryAt(cooldown{}); ok {
		t.Fatal("zero RetryAt must not count")
	}
	if _, ok := RetryAt(errors.New("plain")); ok {
		t.Fatal("plain error must not count")
	}
}

func TestState(t *testing.T) {
	type st struct {
		Cursor int `json:"cursor"`
	}
	var v st
	if err := DecodeState(nil, &v); err != nil || v.Cursor != 0 {
		t.Fatalf("empty: %v %+v", err, v)
	}
	if err := DecodeState(EncodeState(st{Cursor: 3}), &v); err != nil || v.Cursor != 3 {
		t.Fatalf("round trip: %v %+v", err, v)
	}
	if err := DecodeState([]byte(`{`), &v); err == nil {
		t.Fatal("broken json must error")
	}
	if EncodeState(make(chan int)) != nil {
		t.Fatal("unmarshalable value must yield nil")
	}
}
