package httpapi

import (
	"context"
	"net/http"
	"testing"

	"flash-llm-router/internal/janitor"
	"flash-llm-router/internal/store"
)

func TestLogRetentionSettingAndCleanup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// 默认 30 天
	resp := e.req(http.MethodGet, "/api/settings", nil, e.adminToken)
	var got struct {
		LogRetentionDays int `json:"log_retention_days"`
	}
	decodeBody(t, resp, &got)
	resp.Body.Close()
	if got.LogRetentionDays != 30 {
		t.Fatalf("default retention = %d, want 30", got.LogRetentionDays)
	}

	// 设置为 7 天
	resp = e.req(http.MethodPut, "/api/settings", map[string]any{"log_retention_days": 7}, e.adminToken)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update settings status %d", resp.StatusCode)
	}
	// 回读确认持久化
	resp = e.req(http.MethodGet, "/api/settings", nil, e.adminToken)
	decodeBody(t, resp, &got)
	resp.Body.Close()
	if got.LogRetentionDays != 7 {
		t.Fatalf("retention after update = %d, want 7", got.LogRetentionDays)
	}

	// 造 3 条不同时间的日志：2 天前（保留）/ 8 天前 / 40 天前（超期）
	insertAt := func(daysAgo int) {
		t.Helper()
		_, err := e.st.Pool.Exec(ctx, `
			INSERT INTO request_logs (created_at, llm_api_name, provider_name, endpoint, model, status_code, success, cost, client_name)
			VALUES (now() - make_interval(days => $1), 'x', 'p', 'chat.completions', 'm', 200, true, 0, '')`, daysAgo)
		if err != nil {
			t.Fatal(err)
		}
	}
	insertAt(2)
	insertAt(8)
	insertAt(40)

	// 滚动清理一次 → 删除 2 条超期日志
	n, err := janitor.CleanupOnce(ctx, e.st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("cleanup deleted %d, want 2", n)
	}
	_, total, err := e.st.ListLogs(ctx, store.LogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("remaining logs = %d, want 1", total)
	}

	// 0 = 永久保留 → 不再删除
	resp = e.req(http.MethodPut, "/api/settings", map[string]any{"log_retention_days": 0}, e.adminToken)
	resp.Body.Close()
	n, err = janitor.CleanupOnce(ctx, e.st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("permanent retention should delete 0, got %d", n)
	}

	// 越界值拒绝
	resp = e.req(http.MethodPut, "/api/settings", map[string]any{"log_retention_days": -1}, e.adminToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative retention should be 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = e.req(http.MethodPut, "/api/settings", map[string]any{"log_retention_days": 99999}, e.adminToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized retention should be 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
