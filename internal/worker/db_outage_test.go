package worker

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDBOutage_OneLinePerMinute(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	var o dbOutage
	err := errors.New("dial tcp: connection refused")

	o.up(log, "feed dispatcher")
	if buf.Len() != 0 {
		t.Fatalf("up without outage logged: %q", buf.String())
	}
	for range 40 {
		o.down(log, "claim failed", err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Fatalf("40 failures within a minute -> %d lines, want 1:\n%s", n, buf.String())
	}
	o.lastLog = time.Now().Add(-2 * outageLogEvery)
	o.down(log, "listener disconnected", err)
	out := buf.String()
	if n := strings.Count(out, "\n"); n != 2 || !strings.Contains(out, "suppressed=39") {
		t.Fatalf("summary line missing or wrong count:\n%s", out)
	}
	o.up(log, "due listener")
	o.up(log, "feed dispatcher")
	out = buf.String()
	if n := strings.Count(out, "\n"); n != 3 || !strings.Contains(out, "database available again") || !strings.Contains(out, "by=\"due listener\"") {
		t.Fatalf("recovery line:\n%s", out)
	}
	o.down(log, "claim failed", err)
	if n := strings.Count(buf.String(), "\n"); n != 4 {
		t.Fatalf("new outage after recovery must log again: %d lines", n)
	}
}
