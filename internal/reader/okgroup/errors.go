package okgroup

import (
	"errors"
	"time"
)

var (
	errInvalidFeedURL = errors.New("ok_group: cannot resolve group from feed URL")
	ErrGroupNotFound  = errors.New("ok_group: group not found")
	// ErrNotGroup is an ok.ru address that opens a user profile or another
	// non-group page.
	ErrNotGroup = errors.New("ok_group: address is not a group")
)

// CooldownError is returned on HTTP 429: the core retries at RetryAt without
// counting a feed failure.
type CooldownError struct {
	At time.Time
}

func (e CooldownError) Error() string      { return "ok_group: rate limited by ok.ru" }
func (e CooldownError) RetryAt() time.Time { return e.At }
