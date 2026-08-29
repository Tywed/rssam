package smotrim

import "errors"

var (
	errInvalidFeedURL = errors.New("smotrim: invalid feed URL")
	errMissingBrandID = errors.New("smotrim: brand id is required")
	errEmptyResults   = errors.New("smotrim: no videos found")
)
