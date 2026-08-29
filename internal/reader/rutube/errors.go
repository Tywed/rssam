package rutube

import "errors"

var (
	errInvalidFeedURL = errors.New("rutube: cannot resolve channel id from feed URL")
	errMissingChannel = errors.New("rutube: channel id is required")
)
