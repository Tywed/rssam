package ops

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
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

func TestValidReleaseTag(t *testing.T) {
	for _, ok := range []string{"v0.1.5", "0.1.5", "v1.2.3-rc.1", "v10.20.30"} {
		if !ValidReleaseTag(ok) {
			t.Errorf("%q must be accepted", ok)
		}
	}
	for _, bad := range []string{
		"",
		"latest",
		"v1.0.0/../../../evil/repo/releases/download/v9.9.9",
		"v1.0.0?x=1",
		"v1.0.0 --remove",
		"v1.0.0;id",
		"v1.0",
		"v1.0.0\n",
	} {
		if ValidReleaseTag(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

// Probes run inside HTTP handlers; a hung sudo must be killed, not awaited.
func TestProbeCommandTimesOut(t *testing.T) {
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })

	cmd, cancel := probeCommand("sleep", "30")
	defer cancel()
	start := time.Now()
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected the probe to be killed")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("probe took %v, timeout did not apply", d)
	}
}

func TestRestartResult(t *testing.T) {
	if err := restartResult(nil, nil); err != nil {
		t.Fatalf("nil error: %v", err)
	}
	// sudo killed by systemd stopping our cgroup → restart is in progress.
	killed := exec.Command("sh", "-c", "kill -TERM $$")
	if err := killed.Run(); err == nil {
		t.Fatal("expected the helper process to be signalled")
	} else if got := restartResult(err, nil); got != nil {
		t.Fatalf("signalled sudo should count as success, got %v", got)
	}
	// A genuine non-zero exit (e.g. no sudoers entry) is still an error.
	failed := exec.Command("sh", "-c", "exit 1")
	if err := failed.Run(); err == nil {
		t.Fatal("expected exit 1")
	} else if got := restartResult(err, []byte("sudo: a password is required")); got == nil || !strings.Contains(got.Error(), "password is required") {
		t.Fatalf("exit 1 should be an error carrying the output, got %v", got)
	}
	if got := restartResult(errors.New("exec: not found"), nil); got == nil {
		t.Fatal("non-exit errors must propagate")
	}
}
