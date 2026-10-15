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
	SettingOSSEndpoint      = "oss_endpoint"
	SettingOSSBucket        = "oss_bucket"
	SettingOSSAccessKeyID   = "oss_access_key_id"
	SettingOSSAccessKeySecret = "oss_access_key_secret"
	SettingOSSPublicBase    = "oss_public_base"
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

// OSSSettings 阿里云 OSS 配置（全局，所有启用「图片转 URL」的 Provider 共享）。
type OSSSettings struct {
	Endpoint        string `json:"endpoint"`          // 如 oss-cn-hangzhou.aliyuncs.com（可带 http(s):// 前缀）
	Bucket          string `json:"bucket"`            // 存储桶名
	AccessKeyID     string `json:"access_key_id"`     // AccessKey ID
	AccessKeySecret string `json:"access_key_secret"` // AccessKey Secret
	PublicBase      string `json:"public_base"`       // 可选：公开访问前缀（CDN 域名）；为空时用 bucket.endpoint
}

// Complete 判断配置是否足够发起上传。
func (o OSSSettings) Complete() bool {
	return o.Endpoint != "" && o.Bucket != "" && o.AccessKeyID != "" && o.AccessKeySecret != ""
}

func (s *Store) getSettingOr(ctx context.Context, key string) (string, error) {
	v, err := s.GetSetting(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return v, err
}

// GetOSSSettings 读取 OSS 配置（未设置的项为空字符串）。
func (s *Store) GetOSSSettings(ctx context.Context) (OSSSettings, error) {
	var out OSSSettings
	var err error
	if out.Endpoint, err = s.getSettingOr(ctx, SettingOSSEndpoint); err != nil {
		return out, err
	}
	if out.Bucket, err = s.getSettingOr(ctx, SettingOSSBucket); err != nil {
		return out, err
	}
	if out.AccessKeyID, err = s.getSettingOr(ctx, SettingOSSAccessKeyID); err != nil {
		return out, err
	}
	if out.AccessKeySecret, err = s.getSettingOr(ctx, SettingOSSAccessKeySecret); err != nil {
		return out, err
	}
	if out.PublicBase, err = s.getSettingOr(ctx, SettingOSSPublicBase); err != nil {
		return out, err
	}
	return out, nil
}

// SetOSSSettings 覆盖写入 OSS 配置。
func (s *Store) SetOSSSettings(ctx context.Context, in OSSSettings) error {
	pairs := map[string]string{
		SettingOSSEndpoint:        in.Endpoint,
		SettingOSSBucket:          in.Bucket,
		SettingOSSAccessKeyID:     in.AccessKeyID,
		SettingOSSAccessKeySecret: in.AccessKeySecret,
		SettingOSSPublicBase:      in.PublicBase,
	}
	for k, v := range pairs {
		if err := s.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}
