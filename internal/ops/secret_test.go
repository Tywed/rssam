package ops

import (
	"encoding/hex"
	"testing"
)

func TestGenerateHexSecret(t *testing.T) {
	s, err := GenerateHexSecret(32)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 32 {
		t.Fatalf("len=%d", len(b))
	}
	s2, err := GenerateHexSecret(32)
	if err != nil {
		t.Fatal(err)
	}
	if s == s2 {
		t.Fatal("expected distinct secrets")
	}
}
