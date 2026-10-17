package proxy

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestHealth_OutageNeedsStreakAndDuration(t *testing.T) {
	var h Health
	err := errors.New("dial tcp: i/o timeout")
	for range outageMinFailures + 5 {
		h.Failure(err)
	}
	if _, ok := h.Outage(); ok {
		t.Fatal("a fresh streak is not an outage yet")
	}
	h.mu.Lock()
	h.since = time.Now().Add(-outageMinDuration - time.Second)
	h.mu.Unlock()
	st, ok := h.Outage()
	if !ok || st.Failures != outageMinFailures+5 || st.LastErr != err.Error() {
		t.Fatalf("ok=%v st=%+v", ok, st)
	}
	h.Success()
	if _, ok := h.Outage(); ok {
		t.Fatal("one success ends the outage")
	}
	h.Failure(nil)
	if h.failures != 1 || time.Since(h.since) > time.Second {
		t.Fatalf("streak restarts: %d %s", h.failures, h.since)
	}
	var nilHealth *Health
	nilHealth.Failure(err)
	nilHealth.Success()
	if _, ok := nilHealth.Outage(); ok {
		t.Fatal("nil Health is always healthy")
	}
}

func TestUnavailable_WrapsAndClassifies(t *testing.T) {
	inner := errors.New("connect: refused")
	err := fmt.Errorf("telegram: fetch: %w", &Unavailable{Op: "get proxy", Err: inner})
	if !IsUnavailable(err) || !errors.Is(err, inner) {
		t.Fatalf("err=%v", err)
	}
	if IsUnavailable(inner) {
		t.Fatal("plain errors are not proxy failures")
	}
	if got := err.Error(); got != "telegram: fetch: proxy: get proxy: connect: refused" {
		t.Fatalf("message: %s", got)
	}
}
