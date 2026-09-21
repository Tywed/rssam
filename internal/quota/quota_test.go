package quota

import (
	"errors"
	"testing"
	"time"

	"rssam/internal/auth"
)

func TestLimits(t *testing.T) {
	editor := auth.Principal{Role: auth.RoleEditor}
	admin := auth.Principal{Role: auth.RoleAdmin}
	l := Limits{MaxFeedsPerEditor: 2, MaxWebhooksPerEditor: 1, EditorMinPollInterval: 90 * time.Second}

	if err := l.Feeds(editor, 1); err != nil {
		t.Fatalf("below limit: %v", err)
	}
	if err := l.Feeds(editor, 2); !errors.Is(err, ErrExceeded) {
		t.Fatalf("at limit: %v", err)
	}
	if err := l.Feeds(admin, 1000); err != nil {
		t.Fatalf("admin exempt: %v", err)
	}
	if err := (Limits{}).Feeds(editor, 1000); err != nil {
		t.Fatalf("zero = unlimited: %v", err)
	}

	if err := l.Webhooks(editor, 1); err == nil || err.Error() != "webhook limit reached (1 per editor)" {
		t.Fatalf("webhooks: %v", err)
	}

	// 90 s rounds up to a 2-minute floor.
	if err := l.PollInterval(editor, 1); err == nil {
		t.Fatal("1 min below 90 s floor")
	}
	if err := l.PollInterval(editor, 2); err != nil {
		t.Fatalf("2 min: %v", err)
	}
	if err := l.PollInterval(admin, 1); err != nil {
		t.Fatalf("admin: %v", err)
	}
}
