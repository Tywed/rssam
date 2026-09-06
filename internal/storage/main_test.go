package storage

import (
	"os"
	"testing"

	"rssam/internal/auth"

	"golang.org/x/crypto/bcrypt"
)

// Integration tests hash a few passwords per test; production cost (12) is
// ~250 ms each and dominates the suite. Nothing here measures hash strength.
func TestMain(m *testing.M) {
	auth.SetHashCost(bcrypt.MinCost)
	os.Exit(m.Run())
}
