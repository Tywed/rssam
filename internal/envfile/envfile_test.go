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
