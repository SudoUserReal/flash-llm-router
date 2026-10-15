package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flash-llm-router/internal/store"
)

// 慢速流式上游：先吐一个 chunk，停顿后再吐完，用于在请求进行中观察日志状态。
func slowStreamUpstream(t *testing.T, pause time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"开\"}}]}\n\n")
		if fl != nil {
			fl.Flush()
		}
		time.Sleep(pause)
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"始\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 流式请求进行中：日志里应出现"请求中"（in_progress=true）记录，完成后转为终态。
func TestInProgressLogLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	up := slowStreamUpstream(t, 900*time.Millisecond)

	apiID, key := e.createLLMAPI("进行中日志", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "slow", "base_url": up.URL, "timeout_seconds": 30,
		"price_input": 2.0, "price_output": 8.0,
	})

	// 后台发起流式请求（不阻塞主线程观察日志）
	type result struct {
		status int
		body   string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, e.ts.URL+"/v1/chat/completions",
			strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		if err != nil {
			done <- result{err: err}
			return
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		done <- result{status: resp.StatusCode, body: string(b)}
	}()

	// 轮询：请求进行中应当能看到 in_progress 日志
	var sawInProgress bool
	var inProgressCost float64
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		logs, _, err := e.st.ListLogs(ctx, store.LogFilter{Page: 1, PageSize: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) > 0 && logs[0].InProgress {
			sawInProgress = true
			inProgressCost = logs[0].Cost
			// 进行中的请求不应计入统计
			totals, err := e.st.Totals(ctx, store.RangeFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if totals.Requests != 0 {
				t.Fatalf("in-progress request must be excluded from stats, got requests=%d", totals.Requests)
			}
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !sawInProgress {
		t.Fatal("expected an in_progress log while the stream was running")
	}
	if inProgressCost != 0 {
		t.Fatalf("in-progress log should have no cost yet, got %v", inProgressCost)
	}

	// 等待请求完成
	res := <-done
	if res.err != nil {
		t.Fatalf("request error: %v", res.err)
	}
	if res.status != http.StatusOK {
		t.Fatalf("status %d: %s", res.status, res.body)
	}

	// 完成后：同一条日志变为终态（200 成功、有 tokens 与费用），不再有进行中记录
	logs, _, err := e.st.ListLogs(ctx, store.LogFilter{Page: 1, PageSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected exactly 1 log row (updated in place), got %d", len(logs))
	}
	lg := logs[0]
	if lg.InProgress {
		t.Fatal("log should be finished after the request completed")
	}
	if !lg.Success || lg.StatusCode != http.StatusOK {
		t.Fatalf("finished log wrong: %+v", lg)
	}
	if lg.Stream != true || lg.ProviderName != "slow" {
		t.Fatalf("log fields wrong: %+v", lg)
	}
	if lg.PromptTokens != 11 || lg.CompletionTokens != 7 || lg.TotalTokens != 18 {
		t.Fatalf("tokens not recorded: %+v", lg)
	}
	// 11×2/1M + 7×8/1M = 0.000022 + 0.000056 = 0.000078
	if lg.Cost < 0.0000779 || lg.Cost > 0.0000781 {
		t.Fatalf("cost wrong: %v", lg.Cost)
	}
	if !lg.Priced {
		t.Fatal("log should be priced")
	}

	// 完成后计入统计
	totals, err := e.st.Totals(ctx, store.RangeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if totals.Requests != 1 || totals.TotalTokens != 18 {
		t.Fatalf("finished request should be counted: %+v", totals)
	}
}

// 残留的"进行中"日志（进程异常退出）应被标记为中断。
func TestStaleInProgressMarkedInterrupted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// 造一条 2 小时前、仍处于进行中的日志
	if _, err := e.st.Pool.Exec(ctx, `
		INSERT INTO request_logs (created_at, llm_api_name, endpoint, model, in_progress, success, priced)
		VALUES (now() - interval '2 hours', 'stale', 'chat.completions', 'm', true, false, false)`); err != nil {
		t.Fatal(err)
	}
	// 以及一条刚发起的进行中日志（不应被误标）
	if _, err := e.st.Pool.Exec(ctx, `
		INSERT INTO request_logs (llm_api_name, endpoint, model, in_progress, success, priced)
		VALUES ('fresh', 'chat.completions', 'm', true, false, false)`); err != nil {
		t.Fatal(err)
	}

	n, err := e.st.MarkStaleInProgress(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("should mark exactly 1 stale log, got %d", n)
	}

	logs, _, err := e.st.ListLogs(ctx, store.LogFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	var stale, fresh *store.RequestLog
	for i := range logs {
		switch logs[i].LLMAPIName {
		case "stale":
			stale = &logs[i]
		case "fresh":
			fresh = &logs[i]
		}
	}
	if stale == nil || fresh == nil {
		t.Fatalf("logs missing: %+v", logs)
	}
	if stale.InProgress {
		t.Fatal("stale log should no longer be in progress")
	}
	if stale.StatusCode != 499 || stale.Success {
		t.Fatalf("stale log should be marked interrupted (499): %+v", stale)
	}
	if !fresh.InProgress {
		t.Fatal("recent in-progress log must not be touched")
	}
}

// 无可用 Provider 时也要把进行中日志收敛为终态（503）。
func TestInProgressLogFinalizedOnNoProvider(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	apiID, key := e.createLLMAPI("无通道", []string{"m"})
	e.createProvider(apiID, map[string]any{"name": "disabled", "base_url": "http://127.0.0.1:1", "enabled": false})

	resp := e.req(http.MethodPost, "/v1/chat/completions", `{"model":"m","messages":[]}`, key)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
	logs, _, err := e.st.ListLogs(ctx, store.LogFilter{Page: 1, PageSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log row, got %d", len(logs))
	}
	lg := logs[0]
	if lg.InProgress {
		t.Fatal("log must be finalized even when no provider is available")
	}
	if lg.StatusCode != http.StatusServiceUnavailable || lg.Success {
		t.Fatalf("log wrong: %+v", lg)
	}
}
