package dzen

import "strings"

const (
	defaultSearchURL = "https://dzen.ru/news/search"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	defaultCookie    = "zen_sso_checked=1"
)

type Config struct {
	SearchURL string
	UserAgent string
	Cookie    string
}

func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.SearchURL) == "" {
		c.SearchURL = defaultSearchURL
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		c.UserAgent = defaultUserAgent
	}
	if strings.TrimSpace(c.Cookie) == "" {
		c.Cookie = defaultCookie
	}
	return c
}
