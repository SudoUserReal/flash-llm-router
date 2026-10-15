package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"flash-llm-router/internal/store"
)

// 客户端中途断开时，服务端仍必须把日志更新为终态（不能停留在"请求中"）。
// 这依赖网关用 detached context 写日志——否则 r.Context() 被取消会导致更新失败。
func TestLogFinalizedWhenClientDisconnects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	up := slowStreamUpstream(t, 1500*time.Millisecond) // 上游先吐一个 chunk 再停顿

	apiID, key := e.createLLMAPI("断连日志", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "slow", "base_url": up.URL, "timeout_seconds": 30,
		"price_input": 2.0, "price_output": 8.0,
	})

	// 客户端：发出请求，读到第一个 chunk 后立即断开
	reqCtx, cancelReq := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.ts.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	readFirst := make(chan struct{})
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			close(readFirst)
			return
		}
		buf := make([]byte, 64)
		_, _ = resp.Body.Read(buf) // 读到首个 chunk
		close(readFirst)
		cancelReq() // 立即断开
		resp.Body.Close()
	}()

	select {
	case <-readFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first chunk")
	}

	// 等待服务端处理完断连（上游停顿 1.5s 后服务端才会感知写失败）
	deadline := time.Now().Add(8 * time.Second)
	var lg *store.RequestLog
	for time.Now().Before(deadline) {
		logs, _, err := e.st.ListLogs(ctx, store.LogFilter{Page: 1, PageSize: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) == 1 && !logs[0].InProgress {
			lg = &logs[0]
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if lg == nil {
		logs, _, _ := e.st.ListLogs(ctx, store.LogFilter{Page: 1, PageSize: 5})
		t.Fatalf("log stuck in progress after client disconnect: %+v", logs)
	}
	if lg.LLMAPIName != "断连日志" || lg.ProviderName != "slow" {
		t.Fatalf("log fields wrong: %+v", lg)
	}
	t.Logf("断连后日志已收敛为终态: status=%d success=%v error=%q", lg.StatusCode, lg.Success, lg.Error)
}
