package httpserver

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"rssam/internal/auth"
)

// Tests create users on every run; hashing at the production cost is the
// single largest share of the package's wall time and proves nothing here.
func TestMain(m *testing.M) {
	restore := auth.SetHashCost(bcrypt.MinCost)
	code := m.Run()
	restore()
	os.Exit(code)
}
