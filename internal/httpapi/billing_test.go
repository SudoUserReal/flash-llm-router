package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mock 上游返回带缓存命中的 usage，验证端到端计费与 token 来源。
func TestBillingPrecisionWithCachedTokens(t *testing.T) {
	e := newEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1000,"completion_tokens":500,"total_tokens":1500,
			"prompt_tokens_details":{"cached_tokens":800}}}`)
	}))
	t.Cleanup(upstream.Close)

	// 场景 1：缓存价已配置（三段独立计费）
	apiID, key := e.createLLMAPI("billing-a", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "p1", "base_url": upstream.URL,
		"price_input": 2.0, "price_output": 8.0, "price_cached_input": 0.04,
	})
	resp := e.req(http.MethodPost, "/v1/chat/completions", `{"model":"m","messages":[]}`, key)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	lg := e.latestLog()
	// 未命中 200 × 2/1M + 缓存 800 × 0.04/1M + 输出 500 × 8/1M
	// = 0.0004 + 0.000032 + 0.004 = 0.004432
	if lg.Cost < 0.0044319 || lg.Cost > 0.0044321 {
		t.Fatalf("three-segment billing wrong: cost=%v, want 0.004432", lg.Cost)
	}
	// token 必须全部来自上游 usage（响应体），不做本地估算
	if lg.PromptTokens != 1000 || lg.CachedTokens != 800 || lg.CompletionTokens != 500 || lg.TotalTokens != 1500 {
		t.Fatalf("tokens must come from upstream usage: %+v", lg)
	}

	// 场景 2：缓存价未配置（0）→ 缓存部分按输入价计，不漏收
	apiID2, key2 := e.createLLMAPI("billing-b", []string{"m"})
	e.createProvider(apiID2, map[string]any{
		"name": "p2", "base_url": upstream.URL,
		"price_input": 2.0, "price_output": 8.0, // price_cached_input 缺省 = 0
	})
	resp = e.req(http.MethodPost, "/v1/chat/completions", `{"model":"m","messages":[]}`, key2)
	resp.Body.Close()
	lg = e.latestLog()
	// 输入 1000 × 2/1M + 输出 500 × 8/1M = 0.002 + 0.004 = 0.006
	if lg.Cost < 0.0059999 || lg.Cost > 0.0060001 {
		t.Fatalf("missing cached price should fall back to input price: cost=%v, want 0.006", lg.Cost)
	}

	// 场景 3：模型覆盖价只填部分字段 → 其余字段回退 Provider 默认价
	apiID3, key3 := e.createLLMAPI("billing-c", []string{"m"})
	e.createProvider(apiID3, map[string]any{
		"name": "p3", "base_url": upstream.URL,
		"price_input": 1.0, "price_output": 4.0, "price_cached_input": 0.02,
		"model_prices": map[string]any{"m": map[string]any{"input": 2.0}}, // 只覆盖输入价
	})
	resp = e.req(http.MethodPost, "/v1/chat/completions", `{"model":"m","messages":[]}`, key3)
	resp.Body.Close()
	lg = e.latestLog()
	// override: input 2；fallback: cached 0.02 / output 4
	// 200×2/1M + 800×0.02/1M + 500×4/1M = 0.0004 + 0.000016 + 0.002 = 0.002416
	if lg.Cost < 0.0024159 || lg.Cost > 0.0024161 {
		t.Fatalf("per-field price fallback wrong: cost=%v, want 0.002416", lg.Cost)
	}
}
