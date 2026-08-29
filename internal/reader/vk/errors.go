package vk

import "errors"

var (
	errInvalidFeedURL = errors.New("vk: invalid feed url or missing search query")
	errMissingToken   = errors.New("vk: VK_ACCESS_TOKEN is required")
)
