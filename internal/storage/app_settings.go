package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type AppSettingsStore interface {
	GetAppSetting(ctx context.Context, key string) (json.RawMessage, error)
	SetAppSetting(ctx context.Context, key string, value json.RawMessage) error
}

func (s *PostgresStore) GetAppSetting(ctx context.Context, key string) (json.RawMessage, error) {
	const q = `SELECT value FROM app_settings WHERE key = $1`
	var raw json.RawMessage
	err := s.db.QueryRow(ctx, q, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get app setting: %w", err)
	}
	return raw, nil
}

func (s *PostgresStore) SetAppSetting(ctx context.Context, key string, value json.RawMessage) error {
	if len(value) == 0 {
		value = json.RawMessage(`{}`)
	}
	const q = `
INSERT INTO app_settings(key, value, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.db.Exec(ctx, q, key, value); err != nil {
		return fmt.Errorf("set app setting: %w", err)
	}
	return nil
}
