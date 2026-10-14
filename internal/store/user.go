package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, role Role) (*User, error) {
	u := &User{}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id, username, password_hash, role, enabled, created_at, last_login_at`,
		username, passwordHash, role,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	u := &User{}
	err := s.Pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, enabled, created_at, last_login_at
		FROM users WHERE username = $1`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	u := &User{}
	err := s.Pool.QueryRow(ctx, `
		SELECT id, username, password_hash, role, enabled, created_at, last_login_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, username, password_hash, role, enabled, created_at, last_login_at
		FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUser 更新角色/启用状态/密码（passwordHash 为空表示不改）。
func (s *Store) UpdateUser(ctx context.Context, id int64, role *Role, enabled *bool, passwordHash *string) error {
	ct, err := s.Pool.Exec(ctx, `
		UPDATE users SET
			role = COALESCE($2, role),
			enabled = COALESCE($3, enabled),
			password_hash = COALESCE($4, password_hash)
		WHERE id = $1`, id, role, enabled, passwordHash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) TouchLastLogin(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	return err
}

// ---------------------------------------------------------------- sessions

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		userID, tokenHash, expiresAt)
	return err
}

// SessionUser 按 token hash 查询有效 session 对应的用户。
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (*User, error) {
	u := &User{}
	err := s.Pool.QueryRow(ctx, `
		SELECT u.id, u.username, u.password_hash, u.role, u.enabled, u.created_at, u.last_login_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND u.enabled`, tokenHash,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteSessionsForUser 用户被禁用/删除时清理其全部会话（UpdateUser 前调用）。
func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

func (s *Store) CleanupExpiredSessions(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	return err
}
