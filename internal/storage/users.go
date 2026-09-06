package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrDuplicateUsername = errors.New("username already exists")
	ErrDuplicateAPIKey   = errors.New("api key name conflict")
)

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	FeverAPIKey  string
	IsAdmin      bool
	CreatedAt    time.Time
}

type APIKey struct {
	ID         int64
	UserID     int64
	Name       string
	TokenHash  string
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

type APIKeyWithToken struct {
	APIKey
	Token string // plain token, only on create
}

type CreateUserParams struct {
	Username      string
	PasswordHash  string
	PlainPassword string // for fever_api_key derivation
	FeverAPIKey   string
	IsAdmin       bool
}

type UpdateUserParams struct {
	ID            int64
	PasswordHash  *string
	PlainPassword string
	FeverAPIKey   *string
}

type CreateAPIKeyParams struct {
	UserID    int64
	Name      string
	TokenHash string
}

type UserStore interface {
	CountUsers(ctx context.Context) (int, error)
	// CountLoginCapableUsers counts users that have a password set and can
	// actually sign in. Migration 0009 seeds a placeholder user "default" with
	// an empty password_hash (the AUTH_TOKEN principal), which must not count as
	// a bootstrapped admin.
	CountLoginCapableUsers(ctx context.Context) (int, error)
	ListUsers(ctx context.Context, limit, offset int) ([]User, int, error)
	GetUser(ctx context.Context, id int64) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	GetUserByFeverAPIKey(ctx context.Context, apiKey string) (User, error)
	CreateUser(ctx context.Context, params CreateUserParams) (User, error)
	UpdateUser(ctx context.Context, params UpdateUserParams) (User, error)
	DeleteUser(ctx context.Context, id int64) error

	LookupAPIKey(ctx context.Context, tokenHash string) (APIKey, error)
	TouchAPIKeyUsed(ctx context.Context, keyID int64) error
	ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error)
	CreateAPIKey(ctx context.Context, params CreateAPIKeyParams) (APIKey, error)
	DeleteAPIKey(ctx context.Context, userID, keyID int64) error
}

func (s *PostgresStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (s *PostgresStore) CountLoginCapableUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE password_hash <> ''`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count login-capable users: %w", err)
	}
	return n, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context, limit, offset int) ([]User, int, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `
SELECT id, username, password_hash, fever_api_key, is_admin, created_at, count(*) OVER()
FROM users
ORDER BY id ASC
LIMIT $1 OFFSET $2`
	rows, err := s.db.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	out := make([]User, 0, limit)
	total := 0
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.FeverAPIKey, &u.IsAdmin, &u.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) GetUser(ctx context.Context, id int64) (User, error) {
	const q = `SELECT id, username, password_hash, fever_api_key, is_admin, created_at FROM users WHERE id = $1`
	var u User
	err := s.db.QueryRow(ctx, q, id).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.FeverAPIKey, &u.IsAdmin, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (s *PostgresStore) GetUserByUsername(ctx context.Context, username string) (User, error) {
	const q = `SELECT id, username, password_hash, fever_api_key, is_admin, created_at FROM users WHERE username = $1`
	var u User
	err := s.db.QueryRow(ctx, q, strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.FeverAPIKey, &u.IsAdmin, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

func (s *PostgresStore) GetUserByFeverAPIKey(ctx context.Context, apiKey string) (User, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return User{}, ErrNotFound
	}
	const q = `SELECT id, username, password_hash, fever_api_key, is_admin, created_at FROM users WHERE fever_api_key = $1`
	var u User
	err := s.db.QueryRow(ctx, q, apiKey).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.FeverAPIKey, &u.IsAdmin, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user by fever api key: %w", err)
	}
	return u, nil
}

func (s *PostgresStore) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {
	username := strings.TrimSpace(params.Username)
	if username == "" {
		return User{}, errors.New("username is required")
	}
	feverKey := params.FeverAPIKey
	if feverKey == "" && params.PlainPassword != "" {
		feverKey = feverAPIKey(username, params.PlainPassword)
	}
	const q = `
INSERT INTO users(username, password_hash, fever_api_key, is_admin)
VALUES ($1, $2, $3, $4)
RETURNING id, username, password_hash, fever_api_key, is_admin, created_at`
	var u User
	err := s.db.QueryRow(ctx, q, username, params.PasswordHash, feverKey, params.IsAdmin).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.FeverAPIKey, &u.IsAdmin, &u.CreatedAt)
	if err != nil {
		if isDuplicateUsername(err) {
			return User{}, ErrDuplicateUsername
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (s *PostgresStore) UpdateUser(ctx context.Context, params UpdateUserParams) (User, error) {
	if params.PasswordHash != nil {
		feverKey := params.FeverAPIKey
		if feverKey == nil && params.PlainPassword != "" {
			u, err := s.GetUser(ctx, params.ID)
			if err != nil {
				return User{}, err
			}
			k := feverAPIKey(u.Username, params.PlainPassword)
			feverKey = &k
		}
		const q = `
UPDATE users SET password_hash = $2, fever_api_key = COALESCE($3, fever_api_key) WHERE id = $1
RETURNING id, username, password_hash, fever_api_key, is_admin, created_at`
		var u User
		err := s.db.QueryRow(ctx, q, params.ID, *params.PasswordHash, feverKey).
			Scan(&u.ID, &u.Username, &u.PasswordHash, &u.FeverAPIKey, &u.IsAdmin, &u.CreatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return User{}, ErrNotFound
			}
			return User{}, fmt.Errorf("update user password: %w", err)
		}
		return u, nil
	}
	return s.GetUser(ctx, params.ID)
}

func (s *PostgresStore) DeleteUser(ctx context.Context, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) LookupAPIKey(ctx context.Context, tokenHash string) (APIKey, error) {
	const q = `
SELECT id, user_id, name, token_hash, last_used_at, created_at
FROM api_keys
WHERE token_hash = $1`
	var k APIKey
	err := s.db.QueryRow(ctx, q, tokenHash).Scan(&k.ID, &k.UserID, &k.Name, &k.TokenHash, &k.LastUsedAt, &k.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, fmt.Errorf("lookup api key: %w", err)
	}
	return k, nil
}

func (s *PostgresStore) TouchAPIKeyUsed(ctx context.Context, keyID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1`, keyID)
	if err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	const q = `
SELECT id, user_id, name, token_hash, last_used_at, created_at
FROM api_keys
WHERE user_id = $1
ORDER BY id DESC`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.TokenHash, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api keys: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) CreateAPIKey(ctx context.Context, params CreateAPIKeyParams) (APIKey, error) {
	const q = `
INSERT INTO api_keys(user_id, name, token_hash)
VALUES ($1, $2, $3)
RETURNING id, user_id, name, token_hash, last_used_at, created_at`
	var k APIKey
	err := s.db.QueryRow(ctx, q, params.UserID, strings.TrimSpace(params.Name), params.TokenHash).
		Scan(&k.ID, &k.UserID, &k.Name, &k.TokenHash, &k.LastUsedAt, &k.CreatedAt)
	if err != nil {
		return APIKey{}, fmt.Errorf("create api key: %w", err)
	}
	return k, nil
}

func (s *PostgresStore) DeleteAPIKey(ctx context.Context, userID, keyID int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM api_keys WHERE id = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isDuplicateUsername(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "username")
}

// EnsureBootstrapAdmin creates the first admin when no user can sign in yet.
//
// Migration 0009 seeds users(id=1, username=default) with an empty password_hash
// so AUTH_TOKEN has a principal. That row must not count as a bootstrapped
// admin, otherwise ADMIN_USERNAME/ADMIN_PASSWORD are ignored and UI login fails.
//
// When the only rows are password-less placeholders, the env admin is written
// onto id=1 (rename + password) so AUTH_TOKEN and the UI share the same tenant
// and existing feeds stay visible. A password-less row that already has the
// requested username is adopted in place. Only if neither exists is a new user
// inserted.
func (s *PostgresStore) EnsureBootstrapAdmin(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return nil
	}
	n, err := s.CountLoginCapableUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	fever := feverAPIKey(username, password)

	existing, err := s.GetUserByUsername(ctx, username)
	if err == nil && existing.PasswordHash == "" {
		return s.adoptPlaceholderAdmin(ctx, existing.ID, username, hash, fever)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	u, err := s.GetUser(ctx, 1)
	if err == nil && u.PasswordHash == "" {
		return s.adoptPlaceholderAdmin(ctx, u.ID, username, hash, fever)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	_, err = s.CreateUser(ctx, CreateUserParams{
		Username:      username,
		PasswordHash:  hash,
		PlainPassword: password,
		IsAdmin:       true,
	})
	if errors.Is(err, ErrDuplicateUsername) {
		return nil
	}
	return err
}

func (s *PostgresStore) adoptPlaceholderAdmin(ctx context.Context, id int64, username, hash, feverKey string) error {
	const q = `
UPDATE users
SET username = $2, password_hash = $3, fever_api_key = $4, is_admin = TRUE
WHERE id = $1 AND password_hash = ''`
	if _, err := s.db.Exec(ctx, q, id, username, hash, feverKey); err != nil {
		return fmt.Errorf("bootstrap admin: adopt placeholder user: %w", err)
	}
	return nil
}
