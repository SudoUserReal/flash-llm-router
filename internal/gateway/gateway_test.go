package gateway

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flash-llm-router/internal/store"
)

func TestUpstreamURL(t *testing.T) {
	cases := []struct {
		base, endpoint, want string
	}{
		{"https://api.deepseek.com", EndpointChat, "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.deepseek.com/", EndpointChat, "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.openai.com/v1", EndpointChat, "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/", EndpointChat, "https://api.openai.com/v1/chat/completions"},
		{"https://one.example.com", EndpointResponses, "https://one.example.com/v1/responses"},
		{"http://127.0.0.1:9000/v1", EndpointCompletions, "http://127.0.0.1:9000/v1/completions"},
	}
	for _, c := range cases {
		got, err := UpstreamURL(c.base, c.endpoint, "")
		if err != nil {
			t.Fatalf("UpstreamURL(%q): %v", c.base, err)
		}
		if got != c.want {
			t.Errorf("UpstreamURL(%q) = %q, want %q", c.base, got, c.want)
		}
	}
	if _, err := UpstreamURL("not-a-url", EndpointChat, ""); err == nil {
		t.Error("bad base_url should error")
	}
}

func TestComputeCost(t *testing.T) {
	price := store.ModelPrice{Input: 2.0, Output: 8.0, CachedInput: 0.04}
	u := Usage{PromptTokens: 1_000_000, CachedTokens: 400_000, CompletionTokens: 500_000}
	// 未命中: 600k * 2/1M = 1.2 ；命中: 400k * 0.04/1M = 0.016；输出: 500k * 8/1M = 4
	got := ComputeCost(u, price)
	want := 5.216
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", got, want)
	}

	// cached 防越界
	u2 := Usage{PromptTokens: 100, CachedTokens: 500, CompletionTokens: 0}
	if got := ComputeCost(u2, price); math.Abs(got-100*0.04/1e6) > 1e-12 {
		t.Errorf("cached clamp failed: %v", got)
	}

	// 无价格 → 0
	if got := ComputeCost(u, store.ModelPrice{}); got != 0 {
		t.Errorf("zero price should cost 0, got %v", got)
	}
}

func TestParseUsageFromJSON(t *testing.T) {
	openai := `{"id":"x","usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150,
		"prompt_tokens_details":{"cached_tokens":30}}}`
	u := ParseUsageFromJSON([]byte(openai), EndpointChat)
	if u == nil || !u.Reported || u.PromptTokens != 100 || u.CompletionTokens != 50 || u.CachedTokens != 30 {
		t.Fatalf("openai usage parse wrong: %+v", u)
	}

	deepseek := `{"usage":{"prompt_tokens":200,"completion_tokens":80,"total_tokens":280,"prompt_cache_hit_tokens":120}}`
	u = ParseUsageFromJSON([]byte(deepseek), EndpointChat)
	if u == nil || u.CachedTokens != 120 {
		t.Fatalf("deepseek cache hit parse wrong: %+v", u)
	}

	responses := `{"id":"r","usage":{"input_tokens":60,"output_tokens":40,"total_tokens":100,
		"input_tokens_details":{"cached_tokens":10}}}`
	u = ParseUsageFromJSON([]byte(responses), EndpointResponses)
	if u == nil || u.PromptTokens != 60 || u.CompletionTokens != 40 || u.CachedTokens != 10 {
		t.Fatalf("responses usage parse wrong: %+v", u)
	}

	if u := ParseUsageFromJSON([]byte(`{"id":"x"}`), EndpointChat); u != nil && u.Reported {
		t.Fatal("no usage should be Reported=false")
	}
}

func TestMaybeInjectUsage(t *testing.T) {
	p := &store.Provider{InjectStreamUsage: true}
	base := []byte(`{"model":"m","stream":true,"messages":[]}`)

	// 1) 流式 + 未设置 → 注入
	meta := &requestMeta{Stream: true}
	out, injected := maybeInjectUsage(base, meta, p)
	if !injected {
		t.Fatal("should inject")
	}
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(out, &obj)
	var so map[string]any
	_ = json.Unmarshal(obj["stream_options"], &so)
	if so["include_usage"] != true {
		t.Fatalf("include_usage not injected: %s", out)
	}

	// 2) 客户端显式 true → 不注入
	meta2 := &requestMeta{Stream: true, IncludeUsageSet: true, IncludeUsageValue: true}
	if _, injected := maybeInjectUsage(base, meta2, p); injected {
		t.Fatal("explicit true should not re-inject")
	}

	// 3) 客户端显式 false → 仍注入（为了计费），由转发层过滤
	meta3 := &requestMeta{Stream: true, IncludeUsageSet: true, IncludeUsageValue: false}
	out3, injected := maybeInjectUsage(base, meta3, p)
	if !injected {
		t.Fatal("explicit false should still inject for billing")
	}
	if err := json.Unmarshal(out3, &obj); err != nil || len(obj["stream_options"]) == 0 {
		t.Fatal("injected body invalid")
	}

	// 4) Provider 关闭注入 → 原样返回
	pOff := &store.Provider{InjectStreamUsage: false}
	if _, injected := maybeInjectUsage(base, &requestMeta{Stream: true}, pOff); injected {
		t.Fatal("disabled provider must not inject")
	}

	// 5) 非流式 → 不注入
	if _, injected := maybeInjectUsage(base, &requestMeta{Stream: false}, p); injected {
		t.Fatal("non-stream must not inject")
	}
}

func TestIsPureUsageEvent(t *testing.T) {
	pure := [][]byte{[]byte(`{"id":"x","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)}
	if !isPureUsageEvent(pure, EndpointChat) {
		t.Fatal("pure usage event should be detected")
	}
	withContent := [][]byte{[]byte(`{"choices":[{"delta":{"content":"hi"}}],"usage":null}`)}
	if isPureUsageEvent(withContent, EndpointChat) {
		t.Fatal("content event is not pure usage")
	}
	done := [][]byte{[]byte(`[DONE]`)}
	if isPureUsageEvent(done, EndpointChat) {
		t.Fatal("[DONE] is not pure usage")
	}
	if isPureUsageEvent(pure, EndpointResponses) {
		t.Fatal("responses events never classified as pure usage")
	}
}

func TestDeepSeekPrices(t *testing.T) {
	peak := DeepSeekDefaultPrices(TierPeak)
	if peak["deepseek-flash"].Output != 8.0 || peak["deepseek-v4-pro"].Input != 9.0 {
		t.Fatalf("unexpected peak prices: %+v", peak)
	}
	off := DeepSeekDefaultPrices(TierOffPeak)
	if off["deepseek-flash"].Output != 4.0 || off["deepseek-v4-pro"].Output != 13.5 {
		t.Fatalf("unexpected offpeak prices: %+v", off)
	}
	// 旧模型名映射
	if off["deepseek-v4-flash"].Input != 1.0 {
		t.Fatalf("legacy name mapping missing: %+v", off["deepseek-v4-flash"])
	}
	def := DeepSeekDefaultPrice(TierPeak)
	if def.Input != 2.0 || def.Output != 8.0 {
		t.Fatalf("default price should equal flash: %+v", def)
	}
}

// 构造上游 SSE 流，验证 relayStream 的过滤与 usage 提取。
func TestRelayStreamFiltering(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"1","choices":[{"delta":{"content":"你"}}]}`,
		``,
		`data: {"id":"1","choices":[{"delta":{"content":"好"}}]}`,
		``,
		`data: {"id":"1","choices":[],"usage":{"prompt_tokens":10,"cached_tokens":0,"completion_tokens":5,"total_tokens":15}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	run := func(filter bool) (string, *Usage) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(sse))
		}))
		defer upstream.Close()

		resp, err := http.Get(upstream.URL)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		usage, err := relayStream(rec, resp, relayOptions{endpoint: EndpointChat, filterUsage: filter})
		if err != nil {
			t.Fatalf("relay error: %v", err)
		}
		return rec.Body.String(), usage
	}

	// 过滤模式：usage 事件被移除，但计费数据仍被解析
	body, usage := run(true)
	if strings.Contains(body, "prompt_tokens") {
		t.Fatalf("usage event should be filtered: %s", body)
	}
	if usage == nil || usage.PromptTokens != 10 || usage.CompletionTokens != 5 {
		t.Fatalf("usage not parsed: %+v", usage)
	}
	if !strings.Contains(body, `data: [DONE]`) {
		t.Fatal("[DONE] should pass through")
	}

	// 不过滤模式：usage 事件透传
	body2, _ := run(false)
	if !strings.Contains(body2, "prompt_tokens") {
		t.Fatal("usage event should pass when not filtered")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if got := ClientIP(r); got != "10.0.0.1" {
		t.Errorf("got %q", got)
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := ClientIP(r); got != "1.2.3.4" {
		t.Errorf("xff got %q", got)
	}
}
