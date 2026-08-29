package maxstat

import "time"

// Config controls the maxstat.ru search bridge.
type Config struct {
	AccessToken       string
	APIBaseURL        string
	DefaultLimit      int
	DefaultLookback   time.Duration
	Overlap           time.Duration
	RateLimitCooldown time.Duration
}

const (
	defaultAPIBaseURL = "https://maxstat.ru/api/v1"
	defaultLimit      = 100
	defaultLookback   = 24 * time.Hour
	defaultOverlap    = 2 * time.Minute
	defaultRateLimit  = 30 * time.Minute
)

func (c Config) withDefaults() Config {
	if c.APIBaseURL == "" {
		c.APIBaseURL = defaultAPIBaseURL
	}
	if c.DefaultLimit <= 0 {
		c.DefaultLimit = defaultLimit
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
