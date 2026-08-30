package ops

import (
	"strings"
	"testing"
)

func TestRedactDatabaseURL(t *testing.T) {
	got := RedactDatabaseURL("postgres://rssam:secret@127.0.0.1:5432/rssam?sslmode=disable")
	if got == "" || strings.Contains(got, "secret") {
		t.Fatalf("got %q", got)
	}
}

func TestParseUpdateLog(t *testing.T) {
	done, ok, errMsg := ParseUpdateLog("", true)
	if done || ok || errMsg != "" {
		t.Fatal("running")
	}
	done, ok, errMsg = ParseUpdateLog("=== update ===\nok v0.1.1\n", false)
	if !done || !ok || errMsg != "" {
		t.Fatalf("ok: done=%v ok=%v err=%q", done, ok, errMsg)
	}
	done, ok, errMsg = ParseUpdateLog("curl: 403\nerror: no release\n", false)
	if !done || ok || errMsg != "no release" {
		t.Fatalf("fail: done=%v ok=%v err=%q", done, ok, errMsg)
	}
}
