package vk

import "errors"

var (
	errInvalidFeedURL = errors.New("vk: invalid feed url or missing search query")
	// ErrMissingToken: no token in VK_ACCESS_TOKEN or the bridge settings.
	ErrMissingToken   = errors.New("vk: access token is not set (VK_ACCESS_TOKEN or the VK bridge settings)")
	errInvalidWallURL = errors.New("vk: cannot resolve wall from feed URL")
)
