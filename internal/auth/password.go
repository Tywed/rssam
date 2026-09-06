package auth

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is the work factor for new hashes (~250 ms per hash on 2024
// hardware). Tests lower it through SetHashCost; production never does.
var bcryptCost = 12

// SetHashCost overrides the bcrypt cost used by HashPassword and returns a
// function that restores the previous value. It exists for test suites that
// create many users: at the production cost a package with a dozen logins
// spends half a minute in bcrypt. Verification paths are unaffected.
func SetHashCost(cost int) (restore func()) {
	prev := bcryptCost
	bcryptCost = cost
	return func() { bcryptCost = prev }
}

// MinPasswordLength is enforced server-side for every newly set password
// (API and UI); the UI forms declare the same minlength.
const MinPasswordLength = 8

// MaxPasswordLength mirrors bcrypt's 72-byte input limit: anything longer is
// silently truncated by bcrypt, so reject it instead of pretending it counts.
const MaxPasswordLength = 72

var (
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("password must be at most %d bytes", MaxPasswordLength)
)

// dummyPasswordHash is a real bcrypt (cost 12) hash of 48 random bytes that
// were discarded after hashing (no known preimage). It is compared against
// when the login user does not exist so that a failed login costs the same
// wall time whether or not the username is valid.
const dummyPasswordHash = "$2a$12$8i1KqTXDhZTjY54C9GNBO.BgI.rLJ9u4kTTpbisT.Iyc1huITqoou"

// ValidateNewPassword checks the policy for a password being set or changed.
// It is intentionally not called from HashPassword: login must keep accepting
// whatever hash is already stored, including legacy short passwords.
func ValidateNewPassword(password string) error {
	if password == "" {
		return errors.New("password is required")
	}
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	return nil
}

// VerifyLogin is CheckPassword for the login path: it always performs one
// bcrypt comparison, even when the account does not exist (hash == ""), so the
// response time does not reveal whether the username is registered.
func VerifyLogin(hash, password string) bool {
	if password == "" {
		return false
	}
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func CheckPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
