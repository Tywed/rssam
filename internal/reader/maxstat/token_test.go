package maxstat

import (
	"errors"
	"testing"
)

func TestResolveAccessToken(t *testing.T) {
	got, err := ResolveAccessToken("feed-token", "global-token")
	if err != nil || got != "feed-token" {
		t.Fatalf("feed override: got %q err=%v", got, err)
	}
	got, err = ResolveAccessToken("", "global-token")
	if err != nil || got != "global-token" {
		t.Fatalf("global fallback: got %q err=%v", got, err)
	}
	_, err = ResolveAccessToken("", "")
	if !errors.Is(err, errMissingToken) {
		t.Fatalf("expected errMissingToken, got %v", err)
	}
}
