package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"flash-llm-router/internal/store"
)

// ---------------------------------------------------------------- 流式

func TestStreamingProxyInjectsUsageAndBills(t *testing.T) {
	e := newEnv(t)

	var mu sync.Mutex
	var capturedBodies []string
	sse := func(w http.ResponseWriter) {
		fl := w.(http.Flusher)
		for _, c := range []string{
			"data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你\"}}]}\n\n",
			"data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\"}}]}\n\n",
			"data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":10,\"total_tokens\":30}}\n\n",
			"data: [DONE]\n\n",
		} {
			_, _ = io.WriteString(w, c)
			fl.Flush()
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		capturedBodies = append(capturedBodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		sse(w)
	}))
	t.Cleanup(upstream.Close)

	apiID, key := e.createLLMAPI("stream", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "s1", "base_url": upstream.URL,
		"price_input": 2.0, "price_output": 4.0,
	})

	// 用例 1：客户端未要求 usage → 网关注入用于计费，并把 usage 事件过滤掉
	resp := e.req(http.MethodPost, "/v1/chat/completions",
		`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`, key)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	got := string(body)
	if !strings.Contains(got, "你") || !strings.Contains(got, "好") || !strings.Contains(got, "[DONE]") {
		t.Fatalf("stream content missing: %q", got)
	}
	// SSE 事件必须由空行分隔（曾有网关丢弃分隔空行的 bug）
	if !strings.Contains(got, "}\n\n") {
		t.Fatalf("SSE events must be separated by blank lines: %q", got)
	}
	if strings.Contains(got, "prompt_tokens") {
		t.Fatalf("injected usage event should be filtered for client: %q", got)
	}
	mu.Lock()
	firstBody := capturedBodies[0]
	mu.Unlock()
	if !strings.Contains(firstBody, `"include_usage":true`) {
		t.Fatalf("gateway should inject include_usage, upstream got: %s", firstBody)
	}

	lg := e.latestLog()
	if !lg.Stream || !lg.Success || lg.PromptTokens != 20 || lg.CompletionTokens != 10 {
		t.Fatalf("stream log/billing wrong: %+v", lg)
	}
	// 20*2/1e6 + 10*4/1e6 = 0.00004 + 0.00004 = 0.00008
	if lg.Cost < 0.000079 || lg.Cost > 0.000081 {
		t.Fatalf("stream cost wrong: %v", lg.Cost)
	}

	// 用例 2：客户端显式 include_usage=true → usage 事件透传
	resp = e.req(http.MethodPost, "/v1/chat/completions",
		`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`, key)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "prompt_tokens") {
		t.Fatalf("explicit include_usage should receive usage event: %q", body)
	}
}

// ---------------------------------------------------------------- 模型列表

func TestModelsEndpoints(t *testing.T) {
	e := newEnv(t)
	_, key := e.createLLMAPI("模型清单", []string{"m1", "m2"})

	// 无 key → 401
	resp := e.req(http.MethodGet, "/v1/models", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no key should be 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = e.req(http.MethodGet, "/v1/models", nil, key)
	var list struct {
		Object string `json:"object"`
		Data   []struct {
			ID     string `json:"id"`
			Object string `json:"object"`
		} `json:"data"`
	}
	decodeBody(t, resp, &list)
	resp.Body.Close()
	if list.Object != "list" || len(list.Data) != 2 || list.Data[0].ID != "m1" {
		t.Fatalf("models list wrong: %+v", list)
	}

	resp = e.req(http.MethodGet, "/v1/models/m2", nil, key)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get model m2 = %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = e.req(http.MethodGet, "/v1/models/nope", nil, key)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown model should be 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------- 密钥与配置

func TestRotateKey(t *testing.T) {
	e := newEnv(t)
	apiID, key1 := e.createLLMAPI("rotate", []string{"m"})

	resp := e.req(http.MethodPost, fmt.Sprintf("/api/llm-apis/%d/rotate-key", apiID), nil, e.adminToken)
	var api store.LLMAPI
	decodeBody(t, resp, &api)
	resp.Body.Close()
	if api.APIKey == key1 || !strings.HasPrefix(api.APIKey, "sk-flr-") {
		t.Fatalf("rotate key wrong: %q -> %q", key1, api.APIKey)
	}

	resp = e.req(http.MethodGet, "/v1/models", nil, key1)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("old key must be invalid after rotation")
	}
	resp.Body.Close()
	resp = e.req(http.MethodGet, "/v1/models", nil, api.APIKey)
	if resp.StatusCode != http.StatusOK {
		t.Fatal("new key must work")
	}
	resp.Body.Close()
}

func TestDeepSeekAutofillPrice(t *testing.T) {
	e := newEnv(t)
	apiID, _ := e.createLLMAPI("ds", []string{"deepseek-flash"})
	pID := e.createProvider(apiID, map[string]any{
		"name": "deepseek-official", "kind": "deepseek", "base_url": "https://api.deepseek.com",
	})

	// 高峰价
	resp := e.req(http.MethodPost, fmt.Sprintf("/api/providers/%d/autofill-price", pID),
		map[string]string{"tier": "peak"}, e.adminToken)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("autofill status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	p := e.getProvider(pID)
	if p.PriceInput != 2.0 || p.PriceOutput != 8.0 || p.PriceCachedInput != 0.04 {
		t.Fatalf("peak default price wrong: %+v", p)
	}
	if mp := p.ModelPrices["deepseek-v4-pro"]; mp.Output != 27.0 {
		t.Fatalf("model price override wrong: %+v", p.ModelPrices)
	}

	// 空闲价
	resp = e.req(http.MethodPost, fmt.Sprintf("/api/providers/%d/autofill-price", pID),
		map[string]string{"tier": "offpeak"}, e.adminToken)
	resp.Body.Close()
	p = e.getProvider(pID)
	if p.PriceInput != 1.0 || p.PriceOutput != 4.0 {
		t.Fatalf("offpeak price wrong: %+v", p)
	}

	// 非 deepseek 类型拒绝
	pID2 := e.createProvider(apiID, map[string]any{"name": "other", "base_url": "https://x.example.com"})
	resp = e.req(http.MethodPost, fmt.Sprintf("/api/providers/%d/autofill-price", pID2),
		map[string]string{"tier": "peak"}, e.adminToken)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-deepseek autofill should be 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProviderPing(t *testing.T) {
	e := newEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"object":"list","data":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	apiID, _ := e.createLLMAPI("ping", []string{"m"})
	pID := e.createProvider(apiID, map[string]any{"name": "p", "base_url": upstream.URL})

	resp := e.req(http.MethodPost, fmt.Sprintf("/api/providers/%d/ping", pID), nil, e.adminToken)
	var out struct {
		OK     bool   `json:"ok"`
		Status int    `json:"status"`
		Error  string `json:"error"`
	}
	decodeBody(t, resp, &out)
	resp.Body.Close()
	if !out.OK || out.Status != 200 {
		t.Fatalf("ping should succeed: %+v", out)
	}

	// 不可达上游
	badID := e.createProvider(apiID, map[string]any{"name": "bad", "base_url": "http://127.0.0.1:1"})
	resp = e.req(http.MethodPost, fmt.Sprintf("/api/providers/%d/ping", badID), nil, e.adminToken)
	decodeBody(t, resp, &out)
	resp.Body.Close()
	if out.OK {
		t.Fatal("unreachable provider ping should fail")
	}
}

func TestNoAvailableProviderReturns503(t *testing.T) {
	e := newEnv(t)
	apiID, key := e.createLLMAPI("empty", []string{"m"})
	e.createProvider(apiID, map[string]any{"name": "disabled", "base_url": "http://127.0.0.1:1", "enabled": false})

	resp := e.req(http.MethodPost, "/v1/chat/completions",
		`{"model":"m","messages":[]}`, key)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("should be 503, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	lg := e.latestLog()
	if lg.Success || lg.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("log wrong: %+v", lg)
	}
}

func TestReorderProviders(t *testing.T) {
	e := newEnv(t)
	apiID, _ := e.createLLMAPI("reorder", []string{"m"})
	p1 := e.createProvider(apiID, map[string]any{"name": "p1", "priority": 10})
	p2 := e.createProvider(apiID, map[string]any{"name": "p2", "priority": 20})
	p3 := e.createProvider(apiID, map[string]any{"name": "p3", "priority": 30})

	resp := e.req(http.MethodPost, fmt.Sprintf("/api/llm-apis/%d/providers/reorder", apiID),
		map[string]any{"ids": []int64{p3, p1, p2}}, e.adminToken)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("reorder %d: %s", resp.StatusCode, b)
	}
	var out struct {
		Providers []store.Provider `json:"providers"`
	}
	decodeBody(t, resp, &out)
	resp.Body.Close()
	if len(out.Providers) != 3 || out.Providers[0].ID != p3 || out.Providers[1].ID != p1 || out.Providers[2].ID != p2 {
		t.Fatalf("order wrong: %+v", out.Providers)
	}
	if out.Providers[0].Priority != 10 || out.Providers[1].Priority != 20 || out.Providers[2].Priority != 30 {
		t.Fatalf("priority rewrite wrong: %d %d %d",
			out.Providers[0].Priority, out.Providers[1].Priority, out.Providers[2].Priority)
	}
}
