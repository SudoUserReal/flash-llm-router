package httpapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand"
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

// 生成指定尺寸的随机噪声 PNG（不可压缩，保证请求体足够大）的 base64。
func bigPNGBase64(t *testing.T, size int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if _, err := rand.Read(img.Pix); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func newCountingOSS(t *testing.T, uploads *int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, r.Body)
			mu.Lock()
			*uploads++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 开启图片转 URL 时，请求体上限按「替换后预估大小」判定：
// 原请求体超限、替换后很小 → 应放行（旧逻辑会误跳过）。
func TestMaxBodyMBProjectedSizeAllowsImageURL(t *testing.T) {
	e := newEnv(t)
	uploads := 0
	oss := newCountingOSS(t, &uploads)
	if err := e.st.SetOSSSettings(t.Context(), store.OSSSettings{
		Endpoint: oss.URL, Bucket: "img-b", AccessKeyID: "ak-demo", AccessKeySecret: "sk-demo", Prefix: "i",
	}); err != nil {
		t.Fatal(err)
	}

	upstream, calls := chatUpstream(t, 10, 5)
	apiID, key := e.createLLMAPI("投影放行", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "img-p", "base_url": upstream.URL, "image_to_url": true,
		"max_body_mb": 0.1, "price_input": 1.0, "price_output": 1.0,
	})

	body := fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,%s"}]}]}`, bigPNGBase64(t, 256))
	if mb := float64(len(body)) / (1 << 20); mb <= 0.1 {
		t.Fatalf("测试请求体应超过阈值: %.3f MB", mb)
	}

	resp := e.req(http.MethodPost, "/v1/chat/completions", body, key)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("替换后大小应通过阈值（原 %d 字节），got %d", len(body), resp.StatusCode)
	}
	if *calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", *calls)
	}
	if uploads != 1 {
		t.Fatalf("oss uploads = %d, want 1", uploads)
	}
}

// 替换后仍超限 → 跳过该 Provider，且不得发生任何上传（避免无效上传）。
func TestMaxBodyMBProjectedSkipAvoidsUpload(t *testing.T) {
	e := newEnv(t)
	uploads := 0
	oss := newCountingOSS(t, &uploads)
	if err := e.st.SetOSSSettings(t.Context(), store.OSSSettings{
		Endpoint: oss.URL, Bucket: "img-b", AccessKeyID: "ak-demo", AccessKeySecret: "sk-demo", Prefix: "i",
	}); err != nil {
		t.Fatal(err)
	}

	upstream, calls := chatUpstream(t, 10, 5)
	apiID, key := e.createLLMAPI("投影跳过", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "img-skip", "base_url": upstream.URL, "image_to_url": true,
		"max_body_mb": 0.001, // ≈1KB：替换掉图片后，正文文本仍超限
	})

	// ~12KB 中文文本 + 图片 → 替换后仍远超 1KB
	body := fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"%s"},{"type":"image_url","image_url":"data:image/png;base64,%s"}]}]}`,
		strings.Repeat("上下文填充", 800), bigPNGBase64(t, 128))

	resp := e.req(http.MethodPost, "/v1/chat/completions", body, key)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("替换后仍超限应 503，got %d", resp.StatusCode)
	}
	if *calls != 0 {
		t.Fatal("被跳过的 Provider 不应收到请求")
	}
	if uploads != 0 {
		t.Fatalf("超限跳过时不应发生上传（白上传），got %d 次", uploads)
	}
}
