package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"time"
)

const csrfTTL = 24 * time.Hour

// CSRFToken returns an HMAC-based token bound to sessionID.
func CSRFToken(secret, sessionID string) string {
	if secret == "" || sessionID == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(sessionID))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(time.Now().UTC().Format("2006-01-02")))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ValidateCSRF checks token against sessionID (same-day window).
func ValidateCSRF(secret, sessionID, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" || secret == "" || sessionID == "" {
		return false
	}
	if subtleConstantTimeCompare(token, CSRFToken(secret, sessionID)) {
		return true
	}
	// Allow previous day token during midnight rollover.
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(sessionID))
	_, _ = mac.Write([]byte("|"))
	_, _ = mac.Write([]byte(yesterday))
	prev := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return subtleConstantTimeCompare(token, prev)
}

func subtleConstantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
