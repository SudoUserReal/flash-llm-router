package main

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"flash-llm-router/internal/auth"
	"flash-llm-router/internal/config"
	"flash-llm-router/internal/gateway"
	"flash-llm-router/internal/httpapi"
	"flash-llm-router/internal/store"
)

func main() {
	cfg := config.Load()
	logger := newLogger(cfg.LogLevel)

	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("数据库初始化失败", "err", err, "hint", "请检查 DATABASE_URL 环境变量")
		os.Exit(1)
	}
	defer st.Close()
	logger.Info("数据库已连接并完成迁移")

	if err := ensureAdmin(ctx, st, cfg, logger); err != nil {
		logger.Error("初始化管理员失败", "err", err)
		os.Exit(1)
	}

	tz, err := time.LoadLocation(cfg.StatsTZ)
	if err != nil {
		logger.Warn("统计时区加载失败，退回 UTC", "tz", cfg.StatsTZ, "err", err)
		tz = time.UTC
	}

	staticFS, err := fs.Sub(webDistFS, "web/dist")
	if err != nil {
		logger.Warn("前端资源缺失，仅提供 API", "err", err)
		staticFS = nil
	}

	gw := gateway.New(st, logger)
	srv := &httpapi.Server{
		Store:      st,
		GW:         gw,
		Captcha:    auth.NewCaptchaStore(2 * time.Minute),
		Log:        logger,
		StatsTZ:    tz,
		SessionTTL: cfg.SessionTTL,
		StaticFS:   staticFS,
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// 注意：不设置 WriteTimeout，否则会掐断 SSE 流式响应
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("收到退出信号，正在关闭", "signal", sig)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("服务已启动", "addr", cfg.Addr, "stats_tz", tz.String(),
		"ui", "http://localhost"+normalizeAddr(cfg.Addr))
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("服务异常退出", "err", err)
		os.Exit(1)
	}
	logger.Info("已退出")
}

func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":8080"
}

func ensureAdmin(ctx context.Context, st *store.Store, cfg *config.Config, logger *slog.Logger) error {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	password := cfg.AdminPassword
	generated := false
	if password == "" {
		password = randomPassword(16)
		generated = true
	}
	if err := auth.ValidatePasswordPolicy(password); err != nil {
		return errors.New("ADMIN_PASSWORD 不符合要求：" + err.Error())
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(ctx, cfg.AdminUsername, hash, store.RoleAdmin); err != nil {
		return err
	}
	if generated {
		logger.Info("已创建初始管理员（随机密码，请登录后修改）",
			"username", cfg.AdminUsername, "password", password)
	} else {
		logger.Info("已创建初始管理员（密码取自 ADMIN_PASSWORD）", "username", cfg.AdminUsername)
	}
	return nil
}

func randomPassword(n int) string {
	const charset = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = charset[int(raw[i])%len(charset)]
	}
	// 确保包含数字（通过校验策略）
	if !strings.ContainsAny(string(b), "23456789") {
		b[0] = '7'
	}
	return string(b)
}
