package requestid

import (
	"context"
	"strings"
	"testing"
)

func TestContextRoundTrip(t *testing.T) {
	if got := FromContext(context.Background()); got != "" {
		t.Fatalf("empty ctx: %q", got)
	}
	ctx := NewContext(context.Background(), "abc")
	if got := FromContext(ctx); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

func TestGenerate(t *testing.T) {
	a, b := Generate(), Generate()
	if len(a) != 24 || !Valid(a) {
		t.Fatalf("bad id %q", a)
	}
	if a == b {
		t.Fatal("ids must differ")
	}
}

func TestValid(t *testing.T) {
	ok := []string{"a", "req-1", "550e8400-e29b-41d4-a716-446655440000", "trace:span.1", strings.Repeat("x", MaxLen)}
	bad := []string{"", "with space", "new\nline", "semi;colon", "quote\"", strings.Repeat("x", MaxLen+1), "юникод", "tab\t"}
	for _, s := range ok {
		if !Valid(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range bad {
		if Valid(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}
