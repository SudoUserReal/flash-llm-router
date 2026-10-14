package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flash-llm-router/internal/store"
)

// ---------------------------------------------------------------- 内置价目

// DeepSeekTier 价格时段：高峰 / 空闲（空闲为高峰半价）。
type DeepSeekTier string

const (
	TierPeak    DeepSeekTier = "peak"
	TierOffPeak DeepSeekTier = "offpeak"
)

// deepseekPrices 内置 DeepSeek 官方价目（CNY / 1M tokens，2026-10 官网）。
// 高峰时段：北京时间周一至周五（不含法定节假日）9:00-12:00、14:00-18:00；其余为空闲时段。
var deepseekPrices = map[string]map[DeepSeekTier]store.ModelPrice{
	"deepseek-flash": {
		TierPeak:    {Input: 2.0, Output: 8.0, CachedInput: 0.04},
		TierOffPeak: {Input: 1.0, Output: 4.0, CachedInput: 0.02},
	},
	"deepseek-v4-pro": {
		TierPeak:    {Input: 9.0, Output: 27.0, CachedInput: 0.30},
		TierOffPeak: {Input: 4.5, Output: 13.5, CachedInput: 0.15},
	},
}

// 旧模型名与现役模型对应（官网：旧名仍可调用，按对应现役模型计费）。
var deepseekLegacyNames = map[string]string{
	"deepseek-v4-flash":            "deepseek-flash",
	"deepseek-v4-flash-vision-exp": "deepseek-flash",
}

// DeepSeekDefaultPrices 返回内置价目表（按模型名），用于「自动配置价格」。
func DeepSeekDefaultPrices(tier DeepSeekTier) map[string]store.ModelPrice {
	if tier != TierOffPeak {
		tier = TierPeak
	}
	out := map[string]store.ModelPrice{}
	for model, byTier := range deepseekPrices {
		out[model] = byTier[tier]
	}
	for legacy, current := range deepseekLegacyNames {
		if p, ok := deepseekPrices[current]; ok {
			out[legacy] = p[tier]
		}
	}
	return out
}

// DeepSeekDefaultPrice 返回默认价（deepseek-flash 的价格），作为 Provider 默认价兜底。
func DeepSeekDefaultPrice(tier DeepSeekTier) store.ModelPrice {
	if tier != TierOffPeak {
		tier = TierPeak
	}
	return deepseekPrices["deepseek-flash"][tier]
}

// ---------------------------------------------------------------- 余额查询

// DeepSeekBalance 调用 GET {base}/user/balance 查询余额，返回原始 JSON。
func DeepSeekBalance(ctx context.Context, baseURL, apiKey string) (json.RawMessage, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("bad base_url")
	}
	u.Path = "/user/balance"
	u.RawQuery = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 DeepSeek 余额接口失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DeepSeek 余额接口返回 %d: %s", resp.StatusCode, truncateText(string(body), 300))
	}
	if !json.Valid(body) {
		return nil, fmt.Errorf("DeepSeek 余额接口返回非 JSON")
	}
	return json.RawMessage(body), nil
}

// ---------------------------------------------------------------- 连通性测试

// PingProvider 探测 Provider 上游连通性：请求 {base}/v1/models（GET，按 key 认证）。
func (g *Gateway) PingProvider(ctx context.Context, p *store.Provider) (int, int64, error) {
	u, err := url.Parse(strings.TrimSpace(p.BaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return 0, 0, fmt.Errorf("bad base_url")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	u.Path = path + "/models"
	u.RawQuery = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Transport: g.transportFor(15),
		Timeout:   15 * time.Second,
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return 0, latency, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, latency, fmt.Errorf("上游返回 %d: %s", resp.StatusCode, truncateText(string(body), 200))
	}
	return resp.StatusCode, latency, nil
}

func truncateText(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
