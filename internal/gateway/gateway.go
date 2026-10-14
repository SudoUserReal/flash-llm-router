// Package gateway 实现对外 API 的反向代理核心：
// Provider 选路（优先级 + 跳过条件）、熔断、失败自动切换、SSE 流式直通、token 计费。
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"flash-llm-router/internal/store"
)

const (
	EndpointChat        = "chat.completions"
	EndpointCompletions = "completions"
	EndpointResponses   = "responses"
)

const (
	MaxRequestBody        = 64 << 20 // 对外请求体上限 64MB
	maxUpstreamErrorBody  = 1 << 20  // 保留上游错误体上限 1MB
	maxUpstreamRespBody   = 64 << 20 // 非流式响应体读取上限 64MB
	maxModelNameLen       = 200
)

type Gateway struct {
	Store *store.Store
	Log   *slog.Logger

	transports sync.Map // int(timeoutSeconds) -> *http.Transport
}

func New(st *store.Store, logger *slog.Logger) *Gateway {
	return &Gateway{Store: st, Log: logger}
}

type requestMeta struct {
	Model             string
	Stream            bool
	IncludeUsageSet   bool
	IncludeUsageValue bool
}

func parseRequestMeta(body []byte) (*requestMeta, error) {
	var m struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
		StreamOptions *struct {
			IncludeUsage *bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("请求体不是合法 JSON: %v", err)
	}
	if strings.TrimSpace(m.Model) == "" {
		return nil, fmt.Errorf("缺少必填字段 model")
	}
	meta := &requestMeta{Model: m.Model, Stream: m.Stream}
	if len(meta.Model) > maxModelNameLen {
		meta.Model = meta.Model[:maxModelNameLen]
	}
	if m.StreamOptions != nil && m.StreamOptions.IncludeUsage != nil {
		meta.IncludeUsageSet = true
		meta.IncludeUsageValue = *m.StreamOptions.IncludeUsage
	}
	return meta, nil
}

// Proxy 是代理入口。调用方已完成 API key 认证；body 为完整请求体。
// client 非 nil 表示请求来自某个调用方（钱包/限额约束），费用成功后从其钱包扣除。
func (g *Gateway) Proxy(w http.ResponseWriter, r *http.Request, api *store.LLMAPI, endpoint string, body []byte, client *store.APIClient) {
	ctx := r.Context()
	clientAddr := ClientIP(r)

	meta, err := parseRequestMeta(body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		_ = g.Store.InsertLog(ctx, store.LogInput{
			LLMAPIID: &api.ID, LLMAPIName: api.Name,
			Endpoint: endpoint, Error: err.Error(), RequestBytes: int64(len(body)),
			ClientIP: clientAddr, Priced: true,
			ClientID: clientRef(client), ClientName: clientName(client),
		})
		return
	}

	logIn := store.LogInput{
		LLMAPIID:     &api.ID,
		LLMAPIName:   api.Name,
		Endpoint:     endpoint,
		Model:        meta.Model,
		Stream:       meta.Stream,
		RequestBytes: int64(len(body)),
		ClientIP:     clientAddr,
		ClientID:     clientRef(client),
		ClientName:   clientName(client),
	}
	start := time.Now()

	// 1. 选路：按优先级可选 Provider 列表
	now := time.Now()
	candidates := g.selectProviders(ctx, api.ID, endpoint, len(body), now)

	if len(candidates) == 0 {
		msg := "no available provider: 所有 Provider 均被禁用、处于熔断冷却期，或被跳过条件（请求体大小/Responses 支持）过滤"
		writeOpenAIError(w, http.StatusServiceUnavailable, "router_unavailable", msg)
		logIn.Error = msg
		logIn.LatencyMS = time.Since(start).Milliseconds()
		logIn.StatusCode = http.StatusServiceUnavailable
		_ = g.Store.InsertLog(ctx, logIn)
		return
	}

	// 2. 依次尝试，失败自动切换下一个
	var (
		attempts []store.AttemptInfo
		lastResp *upstreamError
		lastErr  error
	)
	for i := range candidates {
		p := &candidates[i]
		attemptStart := time.Now()
		timeoutSec := p.TimeoutSeconds
		if timeoutSec <= 0 {
			timeoutSec = 300
		}

		attemptCtx := ctx
		var cancel context.CancelFunc
		if !meta.Stream {
			// 非流式：总超时覆盖「请求 + 读取响应体」全过程
			attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
		}

		reqBody, injected := maybeInjectUsage(body, meta, p)
		resp, err := g.doUpstream(attemptCtx, r, p, endpoint, reqBody, meta.Stream)
		latency := time.Since(attemptStart).Milliseconds()

		if err != nil {
			if cancel != nil {
				cancel()
			}
			attempts = append(attempts, store.AttemptInfo{Provider: p.Name, Error: err.Error(), LatencyMS: latency})
			_ = g.Store.RecordProviderFailure(ctx, p.ID, err.Error())
			lastErr = err
			lastResp = nil
			g.Log.Warn("upstream request failed", "provider", p.Name, "err", err)
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// 2xx：成功路径（completeSuccess 内读完响应体后由本函数释放 cancel）
			attempts = append(attempts, store.AttemptInfo{Provider: p.Name, Status: resp.StatusCode, LatencyMS: latency})
			g.completeSuccess(w, r, api, p, endpoint, meta, resp, injected, logIn, start, attempts, client)
			if cancel != nil {
				cancel()
			}
			return
		}

		// 非 2xx：按约定一律视为失败，记录并切换下一家
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamErrorBody))
		resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		errText := summarizeUpstreamError(resp.StatusCode, errBody)
		attempts = append(attempts, store.AttemptInfo{Provider: p.Name, Status: resp.StatusCode, Error: errText, LatencyMS: latency})
		_ = g.Store.RecordProviderFailure(ctx, p.ID, errText)
		lastResp = &upstreamError{status: resp.StatusCode, header: resp.Header, body: errBody}
		lastErr = nil
		g.Log.Warn("upstream returned error", "provider", p.Name, "status", resp.StatusCode)
	}

	// 3. 全部失败
	logIn.Success = false
	logIn.Attempts = len(candidates)
	logIn.AttemptsDetail = attempts
	logIn.LatencyMS = time.Since(start).Milliseconds()
	if lastResp != nil {
		logIn.StatusCode = lastResp.status
		logIn.Error = fmt.Sprintf("all %d provider(s) failed, last status %d", len(candidates), lastResp.status)
		copyResponseHeaders(w.Header(), lastResp.header)
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(lastResp.status)
		_, _ = w.Write(lastResp.body)
	} else {
		logIn.StatusCode = http.StatusBadGateway
		msg := "all providers failed"
		if lastErr != nil {
			msg = "all providers failed, last error: " + lastErr.Error()
		}
		logIn.Error = msg
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", msg)
	}
	_ = g.Store.InsertLog(ctx, logIn)
}

type upstreamError struct {
	status int
	header http.Header
	body   []byte
}

// completeSuccess 处理 2xx 响应：转发（流式/非流式）、计费、扣钱包、落日志。
func (g *Gateway) completeSuccess(
	w http.ResponseWriter, r *http.Request, api *store.LLMAPI, p *store.Provider,
	endpoint string, meta *requestMeta, resp *http.Response, injectedUsage bool,
	logIn store.LogInput, start time.Time, attempts []store.AttemptInfo, client *store.APIClient,
) {
	defer resp.Body.Close()
	ctx := r.Context()

	var usage *Usage
	var relayNote string

	if meta.Stream {
		opts := relayOptions{
			endpoint:    endpoint,
			filterUsage: injectedUsage, // 我们注入的 usage 事件按客户端原语义过滤
		}
		var relayErr error
		usage, relayErr = relayStream(w, resp, opts)
		if relayErr != nil {
			relayNote = relayErr.Error()
		}
	} else {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamRespBody))
		if err != nil {
			relayNote = "read upstream response: " + err.Error()
		} else {
			usage = ParseUsageFromJSON(body, endpoint)
			copyResponseHeaders(w.Header(), resp.Header)
			if w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(body)
		}
	}

	// 对 Provider 而言：拿到 2xx 即成功（即使流中途被客户端中断）
	_ = g.Store.RecordProviderSuccess(ctx, p.ID)

	logIn.Success = true
	logIn.StatusCode = http.StatusOK
	logIn.ProviderID = &p.ID
	logIn.ProviderName = p.Name
	logIn.Attempts = len(attempts)
	logIn.AttemptsDetail = attempts
	logIn.LatencyMS = time.Since(start).Milliseconds()
	if relayNote != "" {
		logIn.Error = relayNote
	}

	if usage != nil && usage.Reported {
		price := p.PriceFor(meta.Model)
		cost := ComputeCost(*usage, price)
		logIn.PromptTokens = usage.PromptTokens
		logIn.CachedTokens = usage.CachedTokens
		logIn.CompletionTokens = usage.CompletionTokens
		logIn.TotalTokens = usage.TotalTokens
		logIn.Cost = cost
		logIn.Priced = p.HasPrice()

		// 调用方钱包扣费（条件更新防止并发超支）
		if client != nil && cost > 0 {
			if ok, err := g.Store.ChargeClient(ctx, client.ID, cost); err != nil {
				logIn.Error = appendNote(logIn.Error, "钱包扣费失败: "+err.Error())
			} else if !ok {
				logIn.Error = appendNote(logIn.Error, "钱包余额不足，本次费用未扣除")
			}
		}
	} else {
		logIn.Priced = false // 上游未返回 usage，无法计费
		if relayNote == "" {
			logIn.Error = "上游未返回 usage，未计费"
		}
	}
	_ = g.Store.InsertLog(ctx, logIn)
}

func clientRef(c *store.APIClient) *int64 {
	if c == nil {
		return nil
	}
	id := c.ID
	return &id
}

func clientName(c *store.APIClient) string {
	if c == nil {
		return ""
	}
	return c.Name
}

func appendNote(existing, note string) string {
	if existing == "" {
		return note
	}
	return existing + "; " + note
}

// selectProviders 返回按优先级排序、通过跳过条件过滤后的候选 Provider。
func (g *Gateway) selectProviders(ctx context.Context, llmAPIID int64, endpoint string, bodyLen int, now time.Time) []store.Provider {
	all, err := g.Store.ListProviders(ctx, llmAPIID)
	if err != nil {
		g.Log.Error("list providers", "err", err)
		return nil
	}
	bodyMB := float64(bodyLen) / (1024 * 1024)
	out := make([]store.Provider, 0, len(all))
	for _, p := range all {
		if !p.Enabled {
			continue
		}
		if p.InCooldown(now) {
			continue
		}
		if p.MaxBodyMB > 0 && bodyMB > p.MaxBodyMB {
			continue // 请求体超过该 Provider 的阈值：本次请求不消费它
		}
		if endpoint == EndpointResponses && !p.SupportsResponses {
			continue // 仅直通：不支持 Responses 的 Provider 不参与
		}
		out = append(out, p)
	}
	return out
}

// maybeInjectUsage 流式 chat 请求注入 stream_options.include_usage 以获得计费数据。
// 返回修改后的 body 与是否注入了（注入的事件在转发时需要过滤以保持客户端原语义）。
func maybeInjectUsage(body []byte, meta *requestMeta, p *store.Provider) ([]byte, bool) {
	if !meta.Stream || !p.InjectStreamUsage {
		return body, false
	}
	if meta.IncludeUsageSet && meta.IncludeUsageValue {
		return body, false // 客户端自己要求了 usage，无需注入
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body, false
	}
	so := map[string]json.RawMessage{}
	if raw, ok := obj["stream_options"]; ok {
		_ = json.Unmarshal(raw, &so)
	}
	if v, ok := so["include_usage"]; ok && string(v) == "true" {
		return body, false
	}
	so["include_usage"] = json.RawMessage("true")
	encoded, err := json.Marshal(so)
	if err != nil {
		return body, false
	}
	obj["stream_options"] = encoded
	newBody, err := json.Marshal(obj)
	if err != nil {
		return body, false
	}
	return newBody, true
}

// doUpstream 向上游发起请求。
// 超时语义由调用方控制：非流式用带超时的 ctx 覆盖整个请求与响应体读取；
// 流式不设 ctx 超时，只由 Transport 的 DialContext/ResponseHeaderTimeout 限制「连接+首字节」。
func (g *Gateway) doUpstream(ctx context.Context, r *http.Request, p *store.Provider, endpoint string, body []byte, stream bool) (*http.Response, error) {
	target, err := UpstreamURL(p.BaseURL, endpoint, r.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url %q: %v", p.BaseURL, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("User-Agent", "flash-llm-router/1.0")
	copyRequestHeaders(req.Header, r.Header)

	timeoutSec := p.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	client := &http.Client{Transport: g.transportFor(timeoutSec)}
	return client.Do(req)
}

// transportFor 按超时值缓存 Transport：DialContext 与 ResponseHeaderTimeout 均为配置值。
// 流式场景下这就是"连接 + 首字节"超时；之后的数据传输不受此限。
func (g *Gateway) transportFor(timeoutSec int) *http.Transport {
	if v, ok := g.transports.Load(timeoutSec); ok {
		return v.(*http.Transport)
	}
	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(timeoutSec) * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: time.Duration(timeoutSec) * time.Second,
	}
	actual, _ := g.transports.LoadOrStore(timeoutSec, t)
	return actual.(*http.Transport)
}

// UpstreamURL 拼接上游地址。base_url 可以是 https://api.deepseek.com 或 https://api.openai.com/v1。
func UpstreamURL(baseURL, endpoint, rawQuery string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("bad base_url")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	switch endpoint {
	case EndpointChat:
		path += "/chat/completions"
	case EndpointCompletions:
		path += "/completions"
	case EndpointResponses:
		path += "/responses"
	default:
		return "", fmt.Errorf("unknown endpoint %q", endpoint)
	}
	u.Path = path
	u.RawQuery = rawQuery
	return u.String(), nil
}

func summarizeUpstreamError(status int, body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 500 {
		text = text[:500] + "..."
	}
	return fmt.Sprintf("upstream %d: %s", status, text)
}

func copyResponseHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isHopByHopHeader(k) || k == "Content-Length" {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func copyRequestHeaders(dst, src http.Header) {
	for k, vs := range src {
		if isHopByHopHeader(k) {
			continue
		}
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "Cookie", "Host", "Content-Length", "Accept-Encoding":
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func isHopByHopHeader(k string) bool {
	switch http.CanonicalHeaderKey(k) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	}
	return false
}

func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-Ip"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeOpenAIError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    typ,
			"code":    typ,
		},
	})
}
