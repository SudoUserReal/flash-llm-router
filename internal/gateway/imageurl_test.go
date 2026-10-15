package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"flash-llm-router/internal/store"
)

// ---------------------------------------------------------------- data URL 解析

func TestParseDataURL(t *testing.T) {
	// 1×1 PNG
	pngB64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	key, ct, data, err := parseDataURL("data:image/png;base64,"+pngB64, "flr-images")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ct != "image/png" || !strings.HasPrefix(key, "flr-images/") || !strings.HasSuffix(key, ".png") {
		t.Fatalf("key/ct wrong: %s %s", key, ct)
	}
	if len(data) == 0 || data[0] != 0x89 {
		t.Fatalf("decoded data wrong: %v", data[:2])
	}
	// 相同内容 → 相同 key（内容寻址，可去重）
	key2, _, _, _ := parseDataURL("data:image/png;base64,"+pngB64, "flr-images")
	if key != key2 {
		t.Fatal("same content should map to same key")
	}
	// jpeg / webp
	if k, _, _, _ := parseDataURL("data:image/jpeg;base64,"+pngB64, "flr-images"); !strings.HasSuffix(k, ".jpg") {
		t.Fatalf("jpeg ext wrong: %s", k)
	}

	// 非法输入
	for _, bad := range []string{
		"https://example.com/a.png",
		"data:image/png;utf8,hello",           // 非 base64
		"data:text/plain;base64,aGVsbG8=",     // 非图片
		"data:image/png;base64,!!!not-base64", // 解码失败
		"data:image/png",                      // 缺逗号
	} {
		if _, _, _, err := parseDataURL(bad, "flr-images"); err == nil {
			t.Fatalf("should reject: %s", bad)
		}
	}
}

// ---------------------------------------------------------------- OSS 上传与签名

func TestOSSUploadSigningAndPublicURL(t *testing.T) {
	const (
		akID   = "ak-demo-id"
		akSec  = "sk-demo-secret"
		bucket = "demo-bucket"
	)
	var (
		mu       sync.Mutex
		gotPath  string
		gotBody  []byte
		gotSigOK bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		// 独立重算签名并比对（StringToSign: PUT\n\nCT\nDate\n/bucket/key）
		date := r.Header.Get("Date")
		ct := r.Header.Get("Content-Type")
		expect := hmac.New(sha1.New, []byte(akSec))
		expect.Write([]byte("PUT\n\n" + ct + "\n" + date + "\n/" + bucket + "/flr-images/abc.png"))
		want := "OSS " + akID + ":" + base64.StdEncoding.EncodeToString(expect.Sum(nil))
		gotSigOK = r.Header.Get("Authorization") == want
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	up := newOSSUploader(store.OSSSettings{
		Endpoint: "oss-cn-hangzhou.aliyuncs.com", Bucket: bucket,
		AccessKeyID: akID, AccessKeySecret: akSec,
	})
	// 注入测试地址（虚拟主机风格无法本地解析；签名与 Host 无关，注入不影响校验）
	up.baseURL = srv.URL

	if err := up.upload(context.Background(), "flr-images/abc.png", "image/png", []byte("IMGDATA")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotPath != "/flr-images/abc.png" {
		t.Fatalf("path wrong: %s", gotPath)
	}
	if string(gotBody) != "IMGDATA" {
		t.Fatalf("body wrong: %q", gotBody)
	}
	if !gotSigOK {
		t.Fatal("OSS 签名校验失败")
	}

	// publicURL：默认用上传地址；配置 PublicBase 时用前缀
	if got := up.publicURL("flr-images/x.png"); got != srv.URL+"/flr-images/x.png" {
		t.Fatalf("default publicURL wrong: %s", got)
	}
	up.publicBase = "https://cdn.example.com/assets/"
	if got := up.publicURL("flr-images/x.png"); got != "https://cdn.example.com/assets/flr-images/x.png" {
		t.Fatalf("publicBase URL wrong: %s", got)
	}
}

func TestOSSBaseURLStyles(t *testing.T) {
	// 阿里云：虚拟主机风格
	up := newOSSUploader(store.OSSSettings{Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", Bucket: "b1"})
	if up.baseURL != "https://b1.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("virtual-host style wrong: %s", up.baseURL)
	}
	// IP/localhost：路径风格（兼容 MinIO 与本地测试）
	up2 := newOSSUploader(store.OSSSettings{Endpoint: "http://127.0.0.1:9000", Bucket: "b2"})
	if up2.baseURL != "http://127.0.0.1:9000/b2" {
		t.Fatalf("path style wrong: %s", up2.baseURL)
	}
}

// ---------------------------------------------------------------- 请求体重写

func fakeUpload(u string) func(string) (string, bool) {
	return func(string) (string, bool) { return u, true }
}

func TestRewriteChatImages(t *testing.T) {
	body := `{"model":"m","messages":[
		{"role":"user","content":"纯文本消息"},
		{"role":"user","content":[
			{"type":"text","text":"看这张图"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgo=","detail":"auto"}},
			{"type":"image_url","image_url":"data:image/jpeg;base64,/9j/4AAQ"},
			{"type":"image_url","image_url":{"url":"https://example.com/existing.png"}}
		]}
	]}`
	out, n, err := rewriteChatImages([]byte(body), fakeUpload("https://cdn.example.com/img.png"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("should replace 2 images, got %d", n)
	}
	s := string(out)
	if strings.Contains(s, "data:image") {
		t.Fatalf("base64 应被替换干净: %s", s)
	}
	if strings.Count(s, "https://cdn.example.com/img.png") != 2 {
		t.Fatalf("替换后的 URL 数量不对: %s", s)
	}
	if !strings.Contains(s, "https://example.com/existing.png") {
		t.Fatal("外链图片应保持原样")
	}
	if !strings.Contains(s, "纯文本消息") {
		t.Fatal("文本消息应保持原样")
	}
	// 其余字段保持
	var root map[string]any
	if err := json.Unmarshal(out, &root); err != nil || root["model"] != "m" {
		t.Fatalf("非 messages 字段应保留: %v %v", root["model"], err)
	}
}

func TestRewriteResponsesImages(t *testing.T) {
	body := `{"model":"m","input":[
		{"type":"message","role":"user","content":[
			{"type":"input_text","text":"图1"},
			{"type":"input_image","image_url":"data:image/webp;base64,UklGRg=="}
		]},
		{"type":"function_call_output","call_id":"c1","output":[
			{"type":"input_image","image_url":"data:image/png;base64,iVBORw0KGgo="}
		]}
	]}`
	out, n, err := rewriteResponsesImages([]byte(body), fakeUpload("https://cdn.example.com/r.png"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("should replace 2 images, got %d", n)
	}
	if s := string(out); strings.Contains(s, "data:image") || strings.Count(s, "https://cdn.example.com/r.png") != 2 {
		t.Fatalf("responses 重写结果不对: %s", s)
	}
	// input 为字符串：应原样返回
	if _, n2, _ := rewriteResponsesImages([]byte(`{"input":"hi"}`), fakeUpload("x")); n2 != 0 {
		t.Fatal("input 为字符串时不应替换")
	}
}

func TestRewriteNoImagesUntouched(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	out, n, err := rewriteImagesToURL([]byte(body), EndpointChat, fakeUpload("x"))
	if err != nil || n != 0 || string(out) != body {
		t.Fatalf("无图片请求必须字节级不变: n=%d err=%v", n, err)
	}
}

// 上传失败的图片应保留 base64，其余图片正常替换。
func TestRewritePartialFailureKeepsBase64(t *testing.T) {
	body := `{"messages":[{"role":"user","content":[
		{"type":"image_url","image_url":"data:image/png;base64,bad-one"},
		{"type":"image_url","image_url":"data:image/png;base64,good-one"}
	]}]}`
	upload := func(u string) (string, bool) {
		if strings.Contains(u, "bad-one") {
			return "", false
		}
		return "https://cdn.example.com/ok.png", true
	}
	out, n, _ := rewriteChatImages([]byte(body), upload)
	if n != 1 {
		t.Fatalf("should replace 1, got %d", n)
	}
	s := string(out)
	if !strings.Contains(s, "bad-one") || !strings.Contains(s, "https://cdn.example.com/ok.png") {
		t.Fatalf("部分失败处理不对: %s", s)
	}
}

// 关闭开关时不得触碰请求体（且不依赖数据库）。
func TestRewriteDisabledNoop(t *testing.T) {
	g := &Gateway{}
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":"data:image/png;base64,x"}]}]}`)
	out, n, note := g.rewriteImagesForProvider(context.Background(), body, EndpointChat, &store.Provider{ImageToURL: false})
	if n != 0 || note != "" || string(out) != string(body) {
		t.Fatal("开关关闭时必须原样返回")
	}
}

// 上传结果缓存：同一图片第二次不再上传。
func TestUploadMemoization(t *testing.T) {
	g := &Gateway{}
	if _, ok := g.memoUploaded("flr-images/x.png"); ok {
		t.Fatal("cache should be empty")
	}
	g.memoUploadedSet("flr-images/x.png", "https://cdn.example.com/x.png")
	if url, ok := g.memoUploaded("flr-images/x.png"); !ok || url != "https://cdn.example.com/x.png" {
		t.Fatal("memo set/get mismatch")
	}
}

// 对象前缀可配置（RAM 仅授权某前缀时必须一致）。
func TestParseDataURLPrefix(t *testing.T) {
	pngB64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	key, _, _, err := parseDataURL("data:image/png;base64,"+pngB64, "i")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "i/") {
		t.Fatalf("prefix not applied: %s", key)
	}
}
