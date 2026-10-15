// Package janitor 负责后台数据清理：按保留策略滚动删除过期请求日志。
package janitor

import (
	"context"
	"log/slog"
	"time"

	"flash-llm-router/internal/store"
)

// Run 启动日志清理循环：立即执行一次，之后按 interval 周期执行，直到 ctx 取消。
func Run(ctx context.Context, st *store.Store, logger *slog.Logger, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	cleanup := func() {
		// 1) 先把超时仍"进行中"的日志标记为中断（进程重启/异常退出的残留）
		if n, err := st.MarkStaleInProgress(ctx, time.Now().UTC().Add(-staleInProgressAfter)); err != nil {
			logger.Warn("标记中断日志失败", "err", err)
		} else if n > 0 {
			logger.Info("已将超时的进行中日志标记为中断", "count", n)
		}

		// 2) 按保留策略滚动删除过期日志
		n, err := CleanupOnce(ctx, st)
		if err != nil {
			logger.Warn("日志清理失败", "err", err)
			return
		}
		if n > 0 {
			logger.Info("已滚动清理过期日志", "deleted", n)
		}
	}
	cleanup()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
	}
}

// staleInProgressAfter 超过该时长仍处于"进行中"的日志视为中断。
const staleInProgressAfter = time.Hour

// CleanupOnce 按当前保留策略删除一次过期日志，返回删除条数。
// 保留天数为 0 时表示永久保留，不做任何删除。
func CleanupOnce(ctx context.Context, st *store.Store) (int64, error) {
	days, err := st.GetLogRetentionDays(ctx)
	if err != nil {
		return 0, err
	}
	if days <= 0 {
		return 0, nil
	}
	before := time.Now().UTC().AddDate(0, 0, -days)
	return st.DeleteLogsBefore(ctx, before)
}
