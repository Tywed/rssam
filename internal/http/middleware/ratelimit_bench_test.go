package middleware

import (
	"strconv"
	"testing"
)

// Cost of one Allow with a fresh key once the table is at maxBuckets
// (P-2 in the 2026-11 audit).
func BenchmarkLimiterAllowNewKeyFull(b *testing.B) {
	l := NewLimiter(true, 1, 10)
	for i := range maxBuckets {
		l.Allow("warm-" + strconv.Itoa(i))
	}
	b.ResetTimer()
	for i := range b.N {
		l.Allow("new-" + strconv.Itoa(i))
	}
}

func BenchmarkLimiterAllowKnownKey(b *testing.B) {
	l := NewLimiter(true, 1, 10)
	for i := range 1000 {
		l.Allow("warm-" + strconv.Itoa(i))
	}
	b.ResetTimer()
	for range b.N {
		l.Allow("warm-5")
	}
}
