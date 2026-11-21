package storage

import (
	"context"
	"errors"
)

// UnavailableRetryAfter is the Retry-After value (seconds) for responses
// sent while the database cannot be reached.
const UnavailableRetryAfter = "5"

// IsLookupFailure tells a lookup that failed because the database could not
// answer from one that answered "no such row" or was cancelled by the
// client: only the former is the server's fault (503, not 401/404).
func IsLookupFailure(err error) bool {
	return err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, context.Canceled)
}
