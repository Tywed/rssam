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
