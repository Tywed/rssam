package maxstat

import "errors"

var (
	errInvalidFeedURL = errors.New("maxstat: invalid feed url or missing search query")
	errMissingToken   = errors.New("maxstat: MAXSTAT_ACCESS_TOKEN is required")
)
