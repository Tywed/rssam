package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

var errNoRow = errors.New("not found")

type memSecrets struct {
	rows    map[string]json.RawMessage
	setErr  error
	getErr  error
	setSeen int
}

func (m *memSecrets) GetAppSetting(_ context.Context, key string) (json.RawMessage, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	v, ok := m.rows[key]
	if !ok {
		return nil, errNoRow
	}
	return v, nil
}

func (m *memSecrets) SetAppSetting(_ context.Context, key string, value json.RawMessage) error {
	m.setSeen++
	if m.setErr != nil {
		return m.setErr
	}
	if m.rows == nil {
		m.rows = map[string]json.RawMessage{}
	}
	m.rows[key] = value
	return nil
}

func TestResolveCSRFSecret(t *testing.T) {
	ctx := context.Background()

	t.Run("explicit wins and is not stored", func(t *testing.T) {
		m := &memSecrets{}
		got, err := ResolveCSRFSecret(ctx, "  explicit  ", m, errNoRow)
		if err != nil || got != "explicit" || m.setSeen != 0 {
			t.Fatalf("got %q err=%v sets=%d", got, err, m.setSeen)
		}
	})

	t.Run("generated once, stable across restarts", func(t *testing.T) {
		m := &memSecrets{}
		a, err := ResolveCSRFSecret(ctx, "", m, errNoRow)
		if err != nil || len(a) != 64 {
			t.Fatalf("first: %q err=%v", a, err)
		}
		b, err := ResolveCSRFSecret(ctx, "", m, errNoRow)
		if err != nil || b != a || m.setSeen != 1 {
			t.Fatalf("second: %q err=%v sets=%d", b, err, m.setSeen)
		}
	})

	t.Run("store failures are fatal, not silent", func(t *testing.T) {
		if _, err := ResolveCSRFSecret(ctx, "", &memSecrets{setErr: errors.New("disk full")}, errNoRow); err == nil {
			t.Fatal("persist failure must surface")
		}
		if _, err := ResolveCSRFSecret(ctx, "", &memSecrets{getErr: errors.New("conn refused")}, errNoRow); err == nil {
			t.Fatal("load failure must surface")
		}
	})

	t.Run("corrupt row is regenerated", func(t *testing.T) {
		m := &memSecrets{rows: map[string]json.RawMessage{CSRFSecretKey: json.RawMessage(`""`)}}
		got, err := ResolveCSRFSecret(ctx, "", m, errNoRow)
		if err != nil || len(got) != 64 || m.setSeen != 1 {
			t.Fatalf("got %q err=%v sets=%d", got, err, m.setSeen)
		}
	})
}
