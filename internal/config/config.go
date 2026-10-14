package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL   string        // PostgreSQL DSN
	Addr          string        // 监听地址
	AdminUsername string        // 首次启动自动创建的 admin 用户名
	AdminPassword string        // 首次启动自动创建的 admin 密码（空则随机生成并打印）
	SessionTTL    time.Duration // 会话有效期
	StatsTZ       string        // 统计聚合时区
	LogLevel      string
	BehindTLS     bool // 部署在 HTTPS 反代后（仅影响日志展示，预留）
}

func Load() *Config {
	hours, _ := strconv.Atoi(envOr("SESSION_TTL_HOURS", "168"))
	if hours <= 0 {
		hours = 168
	}
	return &Config{
		DatabaseURL:   envOr("DATABASE_URL", "postgres://localhost:5432/flash_llm_router"),
		Addr:          envOr("ADDR", ":8080"),
		AdminUsername: envOr("ADMIN_USERNAME", "admin"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		SessionTTL:    time.Duration(hours) * time.Hour,
		StatsTZ:       envOr("STATS_TZ", "Asia/Shanghai"),
		LogLevel:      envOr("LOG_LEVEL", "info"),
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
