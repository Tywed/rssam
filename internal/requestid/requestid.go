// Package requestid carries a per-request correlation id through context.
// The HTTP middleware assigns it, the logger prints it, JSON 5xx errors
// return it, so a user-reported failure can be found in the journal.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type ctxKey struct{}

// Header is the request/response header name.
const Header = "X-Request-Id"

// MaxLen bounds ids accepted from a proxy; longer values are replaced.
const MaxLen = 128

// NewContext returns ctx carrying id.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the id stored by NewContext, or "".
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// Generate returns a fresh 24-hex-char id (96 random bits).
func Generate() string {
	var b [12]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error since Go 1.24
	return hex.EncodeToString(b[:])
}

// Valid reports whether an externally supplied id is safe to log and echo:
// 1..MaxLen chars from [A-Za-z0-9._:-], so it cannot inject log lines or
// header syntax.
func Valid(id string) bool {
	if id == "" || len(id) > MaxLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}
