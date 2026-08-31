package ops

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateHexSecret returns n random bytes encoded as hex (2n characters).
func GenerateHexSecret(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("secret length must be positive")
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b), nil
}
