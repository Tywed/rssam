package smotrim

import "strings"

const (
	defaultBrandURL = "https://smotrim.ru/brand"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	defaultLimit     = 50
	maxLimit         = 100
)

type Config struct {
	BrandBaseURL string
	GraphQLURL   string
	UserAgent    string
}

func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.BrandBaseURL) == "" {
		c.BrandBaseURL = defaultBrandURL
	}
	if strings.TrimSpace(c.GraphQLURL) == "" {
		c.GraphQLURL = defaultGraphQLURL
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		c.UserAgent = defaultUserAgent
	}
	return c
}
