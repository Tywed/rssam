package auth

import "testing"

func TestCSRFTokenValidate(t *testing.T) {
	secret := "test-secret"
	sid := "abc123session"
	token := CSRFToken(secret, sid)
	if !ValidateCSRF(secret, sid, token) {
		t.Fatal("expected valid CSRF token")
	}
	if ValidateCSRF(secret, sid, "bad") {
		t.Fatal("expected invalid token rejected")
	}
	if ValidateCSRF(secret, "other-session", token) {
		t.Fatal("expected session binding")
	}
}
