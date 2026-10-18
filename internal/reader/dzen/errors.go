package dzen

import (
	"errors"

	"rssam/internal/bridge/v1"
)

var (
	errInvalidFeedURL = errors.New("dzen: cannot resolve search query from feed URL")
	errMissingQuery   = errors.New("dzen: search query is required")
	// errNeoPayloadNotFound: the search page came without its data island
	// (captcha, redesign) — a layout change, not the feed's failure.
	errNeoPayloadNotFound error = &bridge.LayoutError{Bridge: "dzen", Marker: "window.Ya.Neo"}
	errEmptyResults             = errors.New("dzen: no news items in response")
)
