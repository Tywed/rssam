package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CSRFSecretKey is the app_settings row holding the generated secret.
const CSRFSecretKey = "csrf_secret"

// SecretStore is the slice of storage.AppSettingsStore the secret needs.
type SecretStore interface {
	GetAppSetting(ctx context.Context, key string) (json.RawMessage, error)
	SetAppSetting(ctx context.Context, key string, value json.RawMessage) error
}

// ResolveCSRFSecret returns the explicit CSRF_SECRET when set, otherwise the
// secret persisted in app_settings, generating and storing it on first run.
// The secret is never derived from another credential: a token handed to a
// metrics scraper must not double as CSRF key material. notFound tells a
// missing row apart from a failure. A store error is returned, not papered
// over — a secret that lives only in memory logs every form out at restart.
func ResolveCSRFSecret(ctx context.Context, explicit string, store SecretStore, notFound error) (string, error) {
	if v := strings.TrimSpace(explicit); v != "" {
		return v, nil
	}
	raw, err := store.GetAppSetting(ctx, CSRFSecretKey)
	if err == nil {
		var s string
		if jerr := json.Unmarshal(raw, &s); jerr == nil && strings.TrimSpace(s) != "" {
			return s, nil
		}
	} else if !errors.Is(err, notFound) {
		return "", fmt.Errorf("load csrf secret: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate csrf secret: %w", err)
	}
	s := hex.EncodeToString(buf)
	val, _ := json.Marshal(s)
	if err := store.SetAppSetting(ctx, CSRFSecretKey, val); err != nil {
		return "", fmt.Errorf("persist csrf secret: %w", err)
	}
	return s, nil
}
