package vk

import "time"

// Config controls the VK newsfeed.search bridge.
type Config struct {
	AccessToken       string
	APIVersion        string
	DefaultCount      int
	DefaultLookback   time.Duration
	Overlap           time.Duration
	RateLimitCooldown time.Duration
}

const (
	defaultAPIVersion  = "5.199"
	defaultCount       = 100
	defaultLookback    = 24 * time.Hour
	defaultOverlap     = 2 * time.Minute
	defaultRateLimit   = 5 * time.Second
	vkAPIBaseURL       = "https://api.vk.com/method"
	newsfeedSearchPath = "/newsfeed.search"
)

func (c Config) withDefaults() Config {
	if c.APIVersion == "" {
		c.APIVersion = defaultAPIVersion
	}
	if c.DefaultCount <= 0 {
		c.DefaultCount = defaultCount
	}
	if c.DefaultLookback <= 0 {
		c.DefaultLookback = defaultLookback
	}
	if c.Overlap <= 0 {
		c.Overlap = defaultOverlap
	}
	if c.RateLimitCooldown <= 0 {
		c.RateLimitCooldown = defaultRateLimit
	}
	return c
}
