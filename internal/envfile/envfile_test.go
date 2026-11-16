package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := os.WriteFile(p, []byte("FOO=1\nSECRET=keep\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetKeys(p, map[string]string{"WORKER_POOL_SIZE": "20", "FOO": "2"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "FOO=2\n") {
		t.Fatalf("foo: %s", s)
	}
	if !strings.Contains(s, "SECRET=keep\n") {
		t.Fatal("secret lost")
	}
	if !strings.Contains(s, "WORKER_POOL_SIZE=20\n") {
		t.Fatal("worker")
	}
}

func TestSetKeys_createsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	if err := SetKeys(p, map[string]string{"CSRF_SECRET": "abc"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "CSRF_SECRET=abc\n") {
		t.Fatalf("got %s", b)
	}
}

func TestSetKeys_RejectsControlCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	for _, v := range []string{"a\nB=1", "a\r", "a\x00"} {
		if err := SetKeys(path, map[string]string{"A": v}); err == nil {
			t.Fatalf("%q: expected error", v)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("rejected write must not create the file")
	}
	if err := SetKeys(path, map[string]string{"A": "ok"}); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestSetKeys_ReadOnlyDirWritesInPlace(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\nB=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := SetKeys(path, map[string]string{"B": "3", "C": "4"}); err != nil {
		t.Fatalf("in-place fallback: %v", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "A=1\nB=3\nC=4\n" {
		t.Fatalf("content %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("stray files: %v", entries)
	}
}
