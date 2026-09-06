package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestValidateNewPassword(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		want error
	}{
		{"empty", "", nil},
		{"seven ascii", "abcdefg", ErrPasswordTooShort},
		{"eight ascii", "abcdefgh", nil},
		{"eight cyrillic runes (16 bytes)", "пароль12", nil},
		{"seven cyrillic runes", "пароль1", ErrPasswordTooShort},
		{"72 bytes", strings.Repeat("a", 72), nil},
		{"73 bytes", strings.Repeat("a", 73), ErrPasswordTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNewPassword(tc.pw)
			switch {
			case tc.pw == "":
				if err == nil {
					t.Fatal("empty password must be rejected")
				}
			case tc.want == nil && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyLogin(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyLogin(hash, "correct horse") {
		t.Fatal("valid password rejected")
	}
	if VerifyLogin(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if VerifyLogin("", "anything") {
		t.Fatal("missing hash must never verify")
	}
	if VerifyLogin(hash, "") {
		t.Fatal("empty password must never verify")
	}
	// The dummy hash must be a valid bcrypt hash; otherwise the compare returns
	// immediately and the timing equalisation silently stops working.
	if !strings.HasPrefix(dummyPasswordHash, "$2a$12$") || len(dummyPasswordHash) != 60 {
		t.Fatalf("dummyPasswordHash is not a cost-12 bcrypt hash: %q", dummyPasswordHash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte("x")); !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Fatalf("dummyPasswordHash must be parseable by bcrypt, got %v", err)
	}
}

// Username enumeration: a login attempt for an unknown user (hash == "") must
// take about as long as one for a known user with a wrong password.
func TestVerifyLogin_UnknownUserTakesBcryptTime(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	measure := func(h string) time.Duration {
		var best time.Duration
		for range 3 {
			start := time.Now()
			VerifyLogin(h, "wrong password")
			d := time.Since(start)
			if best == 0 || d < best {
				best = d
			}
		}
		return best
	}
	known := measure(hash)
	unknown := measure("")
	if unknown < known/2 {
		t.Fatalf("unknown-user login is too fast: unknown=%v known=%v", unknown, known)
	}
}
