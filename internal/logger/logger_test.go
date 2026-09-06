package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"rssam/internal/requestid"
)

func TestRequestIDHandler(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(requestIDHandler{slog.NewJSONHandler(&buf, nil)}).With("component", "t")

	log.Error("plain")
	log.ErrorContext(requestid.NewContext(context.Background(), "req-7"), "with id", "err", "boom")

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("lines=%d: %s", len(lines), buf.String())
	}
	var a, b map[string]any
	if err := json.Unmarshal(lines[0], &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lines[1], &b); err != nil {
		t.Fatal(err)
	}
	if _, ok := a["request_id"]; ok {
		t.Fatalf("no-context record must not carry request_id: %v", a)
	}
	if b["request_id"] != "req-7" || b["err"] != "boom" || b["component"] != "t" {
		t.Fatalf("record: %v", b)
	}
}

func TestNewFormats(t *testing.T) {
	if l := New("debug", "text"); l == nil || !l.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("text/debug logger")
	}
	if l := New("warn", "json"); l.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("warn logger must not enable info")
	}
}
