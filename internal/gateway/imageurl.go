package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"flash-llm-router/internal/store"
)

// 图片转 URL：Provider 可开启「请求中的 base64 图片上传 OSS 并替换为公开链接」。
// 主要收益：多轮对话不再每轮重复携带 base64（每轮可省数 MB），上游直接按 URL 拉图。

const (
	maxUploadImageBytes = 40 << 20 // 单张图片上限 40MB（超过则保留 base64）
	defaultObjectPrefix = "flr-images"
	ossConfigTTL        = 15 * time.Second
	ossUploadTimeout    = 60 * time.Second
	ossCacheMaxEntries  = 5000
)

// ---------------------------------------------------------------- OSS 上传

// ossUploader 极简 OSS 客户端：PUT 对象（签名 V1）。
type ossUploader struct {
	endpoint   string
	bucket     string
	akID       string
	akSecret   string
	publicBase string // 公开访问前缀（为空时用上传地址）

	baseURL string // 上传地址前缀（测试可注入）
	client  *http.Client
}

// newOSSUploader 依据配置构造上传器。
// 地址风格：endpoint 为 IP/localhost 时用路径风格（兼容 MinIO），否则用虚拟主机风格（阿里云 OSS）。
func newOSSUploader(cfg store.OSSSettings) *ossUploader {
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.PublicBase = strings.TrimSpace(cfg.PublicBase)

	scheme, host := "https", cfg.Endpoint
	if strings.HasPrefix(host, "http://") {
		scheme, host = "http", strings.TrimPrefix(host, "http://")
	} else if strings.HasPrefix(host, "https://") {
		host = strings.TrimPrefix(host, "https://")
	}
	host = strings.TrimRight(host, "/")
	hostOnly := host
	if i := strings.LastIndex(hostOnly, ":"); i >= 0 {
		hostOnly = hostOnly[:i]
	}
	base := scheme + "://" + cfg.Bucket + "." + host
	if net.ParseIP(hostOnly) != nil || hostOnly == "localhost" {
		base = scheme + "://" + host + "/" + cfg.Bucket
	}
	return &ossUploader{
		endpoint:   cfg.Endpoint,
		bucket:     cfg.Bucket,
		akID:       cfg.AccessKeyID,
		akSecret:   cfg.AccessKeySecret,
		publicBase: cfg.PublicBase,
		baseURL:    base,
		client:     &http.Client{Timeout: ossUploadTimeout},
	}
}

// publicURL 返回对象的公开访问地址（配置了 PublicBase 时用它，否则用 OSS 默认域名）。
func (u *ossUploader) publicURL(key string) string {
	base := u.publicBase
	if base == "" {
		base = u.baseURL
	}
	return strings.TrimRight(base, "/") + "/" + key
}

// sign 计算 OSS V1 签名（StringToSign 不包含 Host，因此测试注入 baseURL 时签名依旧可校验）。
func (u *ossUploader) sign(method, contentType, date, key string) string {
	stringToSign := method + "\n\n" + contentType + "\n" + date + "\n/" + u.bucket + "/" + key
	mac := hmac.New(sha1.New, []byte(u.akSecret))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// upload 上传对象（PUT，幂等）。
func (u *ossUploader) upload(ctx context.Context, key, contentType string, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.baseURL+"/"+key, bytes.NewReader(data))
	if err != nil {
		return err
	}
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "OSS "+u.akID+":"+u.sign(http.MethodPut, contentType, date, key))
	req.ContentLength = int64(len(data))

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("上传 OSS 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("OSS 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// exists 检查对象是否已存在且大小一致（HEAD，带签名以兼容私有桶）。
// 用于重启后避免重复上传：缓存丢失时先确认远端是否已有同一对象。
func (u *ossUploader) exists(ctx context.Context, key string, size int64) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.baseURL+"/"+key, nil)
	if err != nil {
		return false, err
	}
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	req.Header.Set("Authorization", "OSS "+u.akID+":"+u.sign(http.MethodHead, "", date, key))

	resp, err := u.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		// 大小一致才认为可复用（避免同名但内容不同的极端情况）
		return resp.ContentLength == size, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("HEAD 返回 %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------- data URL

// parseDataURL 解析 data:image/...;base64,<data>，返回对象 key、content-type 与原始字节。
// prefix 为对象前缀（受 RAM 授权路径限制时需与之一致）。
func parseDataURL(s, prefix string) (key, contentType string, data []byte, err error) {
	if !strings.HasPrefix(s, "data:") {
		return "", "", nil, fmt.Errorf("不是 data URL")
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return "", "", nil, fmt.Errorf("data URL 缺少逗号")
	}
	meta, payload := s[len("data:"):comma], s[comma+1:]
	if !strings.Contains(meta, "base64") {
		return "", "", nil, fmt.Errorf("仅支持 base64 编码的 data URL")
	}
	mime := strings.TrimSpace(strings.Split(meta, ";")[0])
	if !strings.HasPrefix(mime, "image/") {
		return "", "", nil, fmt.Errorf("仅支持图片类型，得到 %q", mime)
	}
	data, err = base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("base64 解码失败: %w", err)
	}
	sum := sha256.Sum256(data)
	key = prefix + "/" + hex.EncodeToString(sum[:])[:40] + "." + imageExt(mime)
	return key, mime, data, nil
}

func imageExt(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	case "image/bmp":
		return "bmp"
	case "image/svg+xml":
		return "svg"
	}
	return "bin"
}

// ---------------------------------------------------------------- 大小预估

// quickDataURLInfo 轻量解析 data URL：只取 MIME 与 base64 载荷长度（不解码），
// 用于「图片替换为 URL 后」的请求体大小预估。
func quickDataURLInfo(s string) (mime string, payloadLen int, ok bool) {
	if !strings.HasPrefix(s, "data:") {
		return "", 0, false
	}
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return "", 0, false
	}
	meta, payload := s[len("data:"):comma], s[comma+1:]
	if !strings.Contains(meta, "base64") {
		return "", 0, false
	}
	mime = strings.TrimSpace(strings.Split(meta, ";")[0])
	if !strings.HasPrefix(mime, "image/") {
		return "", 0, false
	}
	return mime, len(payload), true
}

// syntheticImageURL 生成与真实 OSS URL 等长的占位 URL，形状：{base}/{prefix}/{sha256前40}.{ext}。
// 真实 URL 的长度只取决于 base/prefix/扩展名（哈希恒为 40 位十六进制），因此预估与替换后完全一致。
func syntheticImageURL(up *ossUploader, prefix, mime string) string {
	return up.publicURL(prefix + "/" + strings.Repeat("0", 40) + "." + imageExt(mime))
}

// projectImagesToURL 估算「图片替换为 URL 后」的请求体字节数。
// 纯本地计算：不解码、不上传；与真实替换共用同一套遍历/序列化逻辑，
// 因此预估结果与实际替换后的字节数一致。
// 返回预估大小与将被替换的图片数量；数量为 0 时调用方应使用原始大小。
func projectImagesToURL(body []byte, endpoint string, cfg store.OSSSettings) (int, int) {
	if !cfg.Complete() {
		return len(body), 0
	}
	up := newOSSUploader(cfg)
	prefix := cfg.ObjectPrefix()
	estimate := func(dataURL string) (string, bool) {
		mime, payloadLen, ok := quickDataURLInfo(dataURL)
		if !ok {
			return "", false
		}
		// 估算解码后大小：超过上传上限的图片实际上不会被替换（与上传逻辑保持一致）
		if int64(payloadLen)*3/4 > int64(maxUploadImageBytes) {
			return "", false
		}
		return syntheticImageURL(up, prefix, mime), true
	}
	projected, n, err := rewriteImagesToURL(body, endpoint, estimate)
	if err != nil || n == 0 {
		return len(body), 0
	}
	return len(projected), n
}

// ---------------------------------------------------------------- 请求体重写

// rewriteImagesToURL 遍历请求体，把 base64 图片替换为 URL。upload 返回新 URL 与是否成功替换。
// 返回修改后的 body 与替换数量；解析失败时原样返回。
func rewriteImagesToURL(body []byte, endpoint string, upload func(string) (string, bool)) ([]byte, int, error) {
	if !bytes.Contains(body, []byte("data:image")) {
		return body, 0, nil
	}
	switch endpoint {
	case EndpointChat, EndpointCompletions:
		return rewriteChatImages(body, upload)
	case EndpointResponses:
		return rewriteResponsesImages(body, upload)
	}
	return body, 0, nil
}

// replaceDataURLField 处理字段值：字符串形态（data URL 直接替换）与对象形态（取其 "url" 字段）。
func replaceDataURLField(raw json.RawMessage, upload func(string) (string, bool)) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false
	}
	switch trimmed[0] {
	case '"':
		var s string
		if json.Unmarshal(trimmed, &s) != nil || !strings.HasPrefix(s, "data:image") {
			return raw, false
		}
		if u, ok := upload(s); ok {
			b, err := json.Marshal(u)
			if err == nil {
				return b, true
			}
		}
		return raw, false
	case '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(trimmed, &obj) != nil {
			return raw, false
		}
		inner, ok := obj["url"]
		if !ok {
			return raw, false
		}
		replaced, ok2 := replaceDataURLField(inner, upload)
		if !ok2 {
			return raw, false
		}
		obj["url"] = replaced
		b, err := json.Marshal(obj)
		if err != nil {
			return raw, false
		}
		return b, true
	}
	return raw, false
}

// rewriteChatImages 处理 Chat Completions：messages[].content[].image_url
// （兼容 content 为字符串、image_url 为字符串或 {url} 对象、以及 input_image 命名）。
func rewriteChatImages(body []byte, upload func(string) (string, bool)) ([]byte, int, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body, 0, fmt.Errorf("解析请求体失败: %w", err)
	}
	rawMessages, ok := root["messages"]
	if !ok {
		return body, 0, nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return body, 0, fmt.Errorf("解析 messages 失败: %w", err)
	}

	total := 0
	for i, rawMsg := range messages {
		if !bytes.Contains(rawMsg, []byte("data:image")) {
			continue
		}
		var msg map[string]json.RawMessage
		if json.Unmarshal(rawMsg, &msg) != nil {
			continue
		}
		var parts []json.RawMessage
		if json.Unmarshal(msg["content"], &parts) != nil {
			continue // content 是字符串，无图片可换
		}
		changed := 0
		for j, part := range parts {
			if !bytes.Contains(part, []byte("data:image")) {
				continue
			}
			var obj map[string]json.RawMessage
			if json.Unmarshal(part, &obj) != nil {
				continue
			}
			field, ok := obj["image_url"]
			if !ok {
				field, ok = obj["input_image"]
			}
			if !ok {
				continue
			}
			replaced, ok2 := replaceDataURLField(field, upload)
			if !ok2 {
				continue
			}
			if _, swapped := obj["image_url"]; swapped {
				obj["image_url"] = replaced
			} else {
				obj["input_image"] = replaced
			}
			if b, err := json.Marshal(obj); err == nil {
				parts[j] = b
				changed++
			}
		}
		if changed == 0 {
			continue
		}
		if b, err := json.Marshal(parts); err == nil {
			msg["content"] = b
			if b2, err := json.Marshal(msg); err == nil {
				messages[i] = b2
				total += changed
			}
		}
	}
	if total == 0 {
		return body, 0, nil
	}
	out, err := json.Marshal(messages)
	if err != nil {
		return body, 0, err
	}
	root["messages"] = out
	newBody, err := json.Marshal(root)
	if err != nil {
		return body, 0, err
	}
	return newBody, total, nil
}

// rewriteResponsesImages 处理 Responses API：input[] 中各项的 content / output 数组里的 input_image。
func rewriteResponsesImages(body []byte, upload func(string) (string, bool)) ([]byte, int, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body, 0, fmt.Errorf("解析请求体失败: %w", err)
	}
	rawInput, ok := root["input"]
	if !ok {
		return body, 0, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(rawInput, &items); err != nil {
		return body, 0, nil // input 是字符串，无图片可换
	}

	total := 0
	for i, rawItem := range items {
		if !bytes.Contains(rawItem, []byte("data:image")) {
			continue
		}
		var item map[string]json.RawMessage
		if json.Unmarshal(rawItem, &item) != nil {
			continue
		}
		changed := 0
		for _, field := range []string{"content", "output"} {
			rawParts, ok := item[field]
			if !ok || !bytes.Contains(rawParts, []byte("data:image")) {
				continue
			}
			var parts []json.RawMessage
			if json.Unmarshal(rawParts, &parts) != nil {
				continue
			}
			for j, part := range parts {
				if !bytes.Contains(part, []byte("data:image")) {
					continue
				}
				var obj map[string]json.RawMessage
				if json.Unmarshal(part, &obj) != nil {
					continue
				}
				img, ok := obj["image_url"]
				if !ok {
					continue
				}
				replaced, ok2 := replaceDataURLField(img, upload)
				if !ok2 {
					continue
				}
				obj["image_url"] = replaced
				if b, err := json.Marshal(obj); err == nil {
					parts[j] = b
					changed++
				}
			}
			if b, err := json.Marshal(parts); err == nil {
				item[field] = b
			}
		}
		if changed == 0 {
			continue
		}
		if b, err := json.Marshal(item); err == nil {
			items[i] = b
			total += changed
		}
	}
	if total == 0 {
		return body, 0, nil
	}
	out, err := json.Marshal(items)
	if err != nil {
		return body, 0, err
	}
	root["input"] = out
	newBody, err := json.Marshal(root)
	if err != nil {
		return body, 0, err
	}
	return newBody, total, nil
}

// ---------------------------------------------------------------- 网关集成

// ossState OSS 配置缓存 + 上传结果缓存。
type ossState struct {
	mu         sync.Mutex
	cfg        *store.OSSSettings
	cfgAt      time.Time
	uploadedMu sync.Mutex
	uploaded   map[string]string // 对象 key → 公开 URL
}

func (g *Gateway) oss() *ossState {
	g.ossOnce.Do(func() {
		g.ossCached = &ossState{uploaded: map[string]string{}}
	})
	return g.ossCached
}

// InvalidateOSSCache 设置变更后调用：重新读取 OSS 配置，并清空已上传缓存
// （配置可能更换了 bucket/前缀，旧缓存不再可靠）。
func (g *Gateway) InvalidateOSSCache() {
	o := g.oss()
	o.mu.Lock()
	o.cfgAt = time.Time{}
	o.mu.Unlock()
	o.uploadedMu.Lock()
	o.uploaded = map[string]string{}
	o.uploadedMu.Unlock()
}

// ossConfig 读取 OSS 配置（带短 TTL 缓存）。
func (g *Gateway) ossConfig(ctx context.Context) (store.OSSSettings, error) {
	o := g.oss()
	o.mu.Lock()
	if o.cfg != nil && time.Since(o.cfgAt) < ossConfigTTL {
		cfg := *o.cfg
		o.mu.Unlock()
		return cfg, nil
	}
	o.mu.Unlock()

	cfg, err := g.Store.GetOSSSettings(ctx)
	if err != nil {
		return cfg, err
	}
	o.mu.Lock()
	o.cfg = &cfg
	o.cfgAt = time.Now()
	o.mu.Unlock()
	return cfg, nil
}

// memoUploaded 查询/记录"已上传"缓存，避免同一图片在多轮对话中重复上传。
func (g *Gateway) memoUploaded(key string) (string, bool) {
	o := g.oss()
	o.uploadedMu.Lock()
	defer o.uploadedMu.Unlock()
	url, ok := o.uploaded[key]
	return url, ok
}

func (g *Gateway) memoUploadedSet(key, url string) {
	o := g.oss()
	o.uploadedMu.Lock()
	defer o.uploadedMu.Unlock()
	if len(o.uploaded) >= ossCacheMaxEntries {
		o.uploaded = map[string]string{}
	}
	o.uploaded[key] = url
}

// rewriteImagesForProvider 按 Provider 的开关重写请求体中的图片。
// 返回新 body、替换数量与提示（提示非空表示重写未生效或部分失败）。
func (g *Gateway) rewriteImagesForProvider(ctx context.Context, body []byte, endpoint string, p *store.Provider) ([]byte, int, string) {
	if !p.ImageToURL || !bytes.Contains(body, []byte("data:image")) {
		return body, 0, ""
	}
	cfg, err := g.ossConfig(ctx)
	if err != nil {
		return body, 0, "读取 OSS 配置失败: " + err.Error()
	}
	if !cfg.Complete() {
		return body, 0, "图片转 URL 已启用但 OSS 配置不完整（需 endpoint/bucket/AK/SK）"
	}
	up := newOSSUploader(cfg)

	prefix := cfg.ObjectPrefix()
	upload := func(dataURL string) (string, bool) {
		key, contentType, data, err := parseDataURL(dataURL, prefix)
		if err != nil {
			g.Log.Warn("图片解析失败，保留原样", "err", err)
			return "", false
		}
		if len(data) > maxUploadImageBytes {
			g.Log.Warn("图片超过上传上限，保留 base64", "bytes", len(data))
			return "", false
		}
		if url, ok := g.memoUploaded(key); ok {
			return url, true
		}
		uctx, cancel := context.WithTimeout(ctx, ossUploadTimeout)
		defer cancel()

		// 缓存未命中（如服务重启）先查远端：内容寻址下对象名即内容哈希，
		// 已存在且大小一致时直接复用，避免重复上传。
		if ok, err := up.exists(uctx, key, int64(len(data))); err != nil {
			g.Log.Debug("OSS 存在性检查失败，继续上传", "err", err, "key", key)
		} else if ok {
			url := up.publicURL(key)
			g.memoUploadedSet(key, url)
			g.Log.Debug("图片已存在于 OSS，跳过上传", "key", key)
			return url, true
		}

		if err := up.upload(uctx, key, contentType, data); err != nil {
			g.Log.Warn("上传 OSS 失败，保留 base64", "err", err, "key", key)
			return "", false
		}
		url := up.publicURL(key)
		g.memoUploadedSet(key, url)
		g.Log.Debug("图片已上传 OSS", "key", key, "bytes", len(data))
		return url, true
	}

	newBody, n, err := rewriteImagesToURL(body, endpoint, upload)
	if err != nil {
		return body, 0, "图片转 URL 失败: " + err.Error()
	}
	return newBody, n, ""
}
