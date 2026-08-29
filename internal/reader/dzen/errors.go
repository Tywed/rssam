package dzen

import "errors"

var (
	errInvalidFeedURL     = errors.New("dzen: cannot resolve search query from feed URL")
	errMissingQuery       = errors.New("dzen: search query is required")
	errNeoPayloadNotFound = errors.New("dzen: news payload not found in page")
	errEmptyResults       = errors.New("dzen: no news items in response")
)
