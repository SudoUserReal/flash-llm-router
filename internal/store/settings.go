package store

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// 设置项 key
const (
	SettingLogRetentionDays = "log_retention_days" // 日志保留天数，0 = 永久保留
)

// DefaultLogRetentionDays 默认日志保留天数。
const DefaultLogRetentionDays = 30

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}

// GetLogRetentionDays 返回日志保留天数；未设置时返回默认值（30）。
func (s *Store) GetLogRetentionDays(ctx context.Context) (int, error) {
	v, err := s.GetSetting(ctx, SettingLogRetentionDays)
	if errors.Is(err, ErrNotFound) {
		return DefaultLogRetentionDays, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return DefaultLogRetentionDays, nil
	}
	return n, nil
}

// SetLogRetentionDays 设置日志保留天数（0 = 永久保留）。
func (s *Store) SetLogRetentionDays(ctx context.Context, days int) error {
	return s.SetSetting(ctx, SettingLogRetentionDays, strconv.Itoa(days))
}
