package rutube

import "strings"

const defaultAPIBaseURL = "https://rutube.ru/api"

// Config controls the Rutube person-videos bridge.
type Config struct {
	APIBaseURL string
	UserAgent  string
}

func (c Config) withDefaults() Config {
	c.APIBaseURL = strings.TrimRight(strings.TrimSpace(c.APIBaseURL), "/")
	if c.APIBaseURL == "" {
		c.APIBaseURL = defaultAPIBaseURL
	}
	return c
}
