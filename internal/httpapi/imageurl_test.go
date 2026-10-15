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

// 图片转 URL 端到端：base64 → 上传 mock OSS → 上游收到 URL；重复图片只上传一次。
func TestImageToURLEndToEnd(t *testing.T) {
	e := newEnv(t)

	// ---- mock OSS（路径风格：PUT /<bucket>/flr-images/<key>）
	var (
		ossMu      sync.Mutex
		ossUploads []string
		ossBodies  [][]byte
	)
	oss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/img-bucket/flr-images/") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(r.Header.Get("Authorization"), "OSS ") {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		ossMu.Lock()
		ossUploads = append(ossUploads, r.URL.Path)
		ossBodies = append(ossBodies, body)
		ossMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(oss.Close)

	// ---- 配置 OSS（全局设置）
	if err := e.st.SetOSSSettings(t.Context(), store.OSSSettings{
		Endpoint:        oss.URL, // http://127.0.0.1:port → 路径风格
		Bucket:          "img-bucket",
		AccessKeyID:     "ak-demo",
		AccessKeySecret: "sk-demo",
	}); err != nil {
		t.Fatal(err)
	}

	// ---- mock 上游：捕获收到的请求体
	var (
		upMu   sync.Mutex
		upBody []byte
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		upMu.Lock()
		upBody = b
		upMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	t.Cleanup(upstream.Close)

	apiID, key := e.createLLMAPI("图片服务", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "img-up", "base_url": upstream.URL,
		"image_to_url": true, "price_input": 1.0, "price_output": 1.0,
	})

	// 1×1 PNG（base64）
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	reqBody := fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"看图"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}
	]}]}`, png)

	doCall := func() {
		t.Helper()
		resp := e.req(http.MethodPost, "/v1/chat/completions", reqBody, key)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("proxy status %d: %s", resp.StatusCode, b)
		}
	}

	doCall()
	upMu.Lock()
	got := string(upBody)
	upMu.Unlock()

	if strings.Contains(got, "data:image") {
		t.Fatalf("上游不应再收到 base64：%s", got[:min(len(got), 300)])
	}
	if !strings.Contains(got, oss.URL+"/img-bucket/flr-images/") {
		t.Fatalf("上游应收到 OSS URL：%s", got[:min(len(got), 300)])
	}
	ossMu.Lock()
	if len(ossUploads) != 1 {
		t.Fatalf("应上传 1 张图片，实际 %d", len(ossUploads))
	}
	if len(ossBodies[0]) == 0 || ossBodies[0][0] != 0x89 {
		t.Fatalf("上传内容应为 PNG 原始字节")
	}
	ossMu.Unlock()

	// 第二次请求：同一图片应命中缓存，不再重复上传
	doCall()
	ossMu.Lock()
	if len(ossUploads) != 1 {
		t.Fatalf("重复请求不应再次上传，累计上传 %d 次", len(ossUploads))
	}
	ossMu.Unlock()

	// 日志与计费不受影响
	lg := e.latestLog()
	if !lg.Success || lg.PromptTokens != 10 || lg.CompletionTokens != 5 || lg.Cost <= 0 {
		t.Fatalf("计费应保持正常: %+v", lg)
	}
}

// 开关关闭时上游应收到原始 base64（保持既有行为）。
func TestImageToURLDisabledPassesBase64(t *testing.T) {
	e := newEnv(t)

	var (
		upMu   sync.Mutex
		upBody []byte
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		upMu.Lock()
		upBody = b
		upMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)

	apiID, key := e.createLLMAPI("原样服务", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "plain", "base_url": upstream.URL, "image_to_url": false,
	})

	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	reqBody := fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,%s"}]}]}`, png)
	resp := e.req(http.MethodPost, "/v1/chat/completions", reqBody, key)
	resp.Body.Close()

	upMu.Lock()
	defer upMu.Unlock()
	if !strings.Contains(string(upBody), "data:image/png;base64,") {
		t.Fatal("开关关闭时 base64 应原样透传")
	}
}

// 开关打开但 OSS 未配置完整：请求仍应成功（保留 base64 降级）。
func TestImageToURLIncompleteConfigDegrades(t *testing.T) {
	e := newEnv(t)

	var (
		upMu   sync.Mutex
		upBody []byte
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		upMu.Lock()
		upBody = b
		upMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)

	apiID, key := e.createLLMAPI("降级服务", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "deg", "base_url": upstream.URL, "image_to_url": true,
	})

	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	reqBody := fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,%s"}]}]}`, png)
	resp := e.req(http.MethodPost, "/v1/chat/completions", reqBody, key)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OSS 未配置不应对请求失败，got %d", resp.StatusCode)
	}
	upMu.Lock()
	defer upMu.Unlock()
	if !strings.Contains(string(upBody), "data:image/png;base64,") {
		t.Fatal("未配置 OSS 时应保留 base64")
	}
}
