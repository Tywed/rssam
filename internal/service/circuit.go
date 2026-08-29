package service

import "errors"

// ErrFeedCircuitOpen is returned when background polling is paused after repeated failures.
var ErrFeedCircuitOpen = errors.New("feed polling paused after repeated errors; use manual refresh")
