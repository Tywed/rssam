// Package quota bounds what one editor may put on the instance. Limits are
// global (.env) and never apply to admins; a zero limit means unlimited.
package quota

import (
	"errors"
	"fmt"
	"time"

	"rssam/internal/auth"
)

type Limits struct {
	MaxFeedsPerEditor    int
	MaxWebhooksPerEditor int
	// EditorMinPollInterval is the shortest poll interval an editor may set
	// on a feed; admins may go down to the storage minimum.
	EditorMinPollInterval time.Duration
}

// Error is a limit violation; handlers answer it with 4xx and the message.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

// Is lets callers match any quota violation with errors.Is(err, quota.ErrExceeded).
func (e *Error) Is(target error) bool { return target == ErrExceeded }

var ErrExceeded = errors.New("quota exceeded")

// Feeds checks whether p may add one more feed to the current count.
func (l Limits) Feeds(p auth.Principal, current int) error {
	if l.MaxFeedsPerEditor <= 0 || p.IsAdmin() || current < l.MaxFeedsPerEditor {
		return nil
	}
	return &Error{fmt.Sprintf("feed limit reached (%d per editor)", l.MaxFeedsPerEditor)}
}

// Webhooks checks whether p may add one more webhook to the current count.
func (l Limits) Webhooks(p auth.Principal, current int) error {
	if l.MaxWebhooksPerEditor <= 0 || p.IsAdmin() || current < l.MaxWebhooksPerEditor {
		return nil
	}
	return &Error{fmt.Sprintf("webhook limit reached (%d per editor)", l.MaxWebhooksPerEditor)}
}

// PollInterval checks a feed interval in minutes against the editor floor.
func (l Limits) PollInterval(p auth.Principal, minutes int) error {
	if l.EditorMinPollInterval <= 0 || p.IsAdmin() {
		return nil
	}
	floor := int(l.EditorMinPollInterval / time.Minute)
	if l.EditorMinPollInterval%time.Minute != 0 {
		floor++
	}
	if minutes >= floor {
		return nil
	}
	return &Error{fmt.Sprintf("interval_minutes must be at least %d for editors", floor)}
}
