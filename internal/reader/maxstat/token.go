package maxstat

import "strings"

// ResolveAccessToken returns per-feed token or global fallback.
func ResolveAccessToken(feedToken, globalToken string) (string, error) {
	if t := strings.TrimSpace(feedToken); t != "" {
		return t, nil
	}
	if t := strings.TrimSpace(globalToken); t != "" {
		return t, nil
	}
	return "", errMissingToken
}

func resolveAccessToken(feedToken, globalToken string) (string, error) {
	return ResolveAccessToken(feedToken, globalToken)
}
