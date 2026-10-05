package dzenchannel

import (
	"errors"
	"time"
)

var (
	errInvalidFeedURL  = errors.New("dzen_channel: cannot resolve channel from feed URL")
	ErrChannelNotFound = errors.New("dzen_channel: channel not found")
)

// CooldownError is returned on HTTP 429: the core retries at RetryAt without
// counting a feed failure.
type CooldownError struct {
	At time.Time
}

func (e CooldownError) Error() string      { return "dzen_channel: rate limited by dzen.ru" }
func (e CooldownError) RetryAt() time.Time { return e.At }
