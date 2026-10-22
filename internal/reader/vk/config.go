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
	wallGetPath        = "/wall.get"
	// wallGetCount is how many newest posts one poll reads; dedup drops the
	// ones already stored, so the number only has to cover the posting rate
	// between two polls.
	wallGetCount = 20
	// minRequestGap keeps all VK calls of this token under the API's
	// 3 requests/second regardless of how many feeds share it.
	minRequestGap = 350 * time.Millisecond
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
