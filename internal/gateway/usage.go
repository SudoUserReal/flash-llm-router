package gateway

import (
	"encoding/json"
	"math"

	"flash-llm-router/internal/store"
)

// Usage 一次上游调用的 token 消耗。
// 兼容 OpenAI Chat Completions / Completions 与 Responses API 的字段差异。
type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	Reported         bool  `json:"reported"` // 上游是否报告了 usage
}

func (u *Usage) normalize() {
	if u.CachedTokens > u.PromptTokens {
		u.CachedTokens = u.PromptTokens
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
}

// usageJSON 兼容各家字段：OpenAI prompt_tokens_details.cached_tokens / DeepSeek prompt_cache_hit_tokens。
type usageJSON struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"` // DeepSeek: 缓存命中 input
}

func (j *usageJSON) toUsage() *Usage {
	if j == nil {
		return nil
	}
	u := &Usage{
		PromptTokens:     j.PromptTokens,
		CompletionTokens: j.CompletionTokens,
		TotalTokens:      j.TotalTokens,
		Reported:         true,
	}
	if j.PromptTokensDetails != nil {
		u.CachedTokens = j.PromptTokensDetails.CachedTokens
	}
	if u.CachedTokens == 0 && j.PromptCacheHitTokens > 0 {
		u.CachedTokens = j.PromptCacheHitTokens
	}
	u.normalize()
	return u
}

type responsesUsageJSON struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (j *responsesUsageJSON) toUsage() *Usage {
	if j == nil {
		return nil
	}
	u := &Usage{
		PromptTokens:     j.InputTokens,
		CompletionTokens: j.OutputTokens,
		TotalTokens:      j.TotalTokens,
		Reported:         true,
	}
	if j.InputTokensDetails != nil {
		u.CachedTokens = j.InputTokensDetails.CachedTokens
	}
	u.normalize()
	return u
}

// unwrapEnvelope 兼容部分中转网关的包装格式：{"data": {...}, "success": true}。
// 若顶层没有预期字段但存在对象的 "data" 字段，则返回内层 JSON，否则原样返回。
func unwrapEnvelope(body []byte) []byte {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return body
	}
	if _, hasUsage := top["usage"]; hasUsage {
		return body
	}
	if _, hasChoices := top["choices"]; hasChoices {
		return body
	}
	if raw, ok := top["data"]; ok && len(raw) > 0 && raw[0] == '{' {
		return raw
	}
	return body
}

// ParseUsageFromJSON 从非流式响应体解析 usage。
// 自动兼容包装格式（见 unwrapEnvelope）。
func ParseUsageFromJSON(body []byte, endpoint string) *Usage {
	body = unwrapEnvelope(body)
	if endpoint == EndpointResponses {
		var resp struct {
			Usage *responsesUsageJSON `json:"usage"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil
		}
		return resp.Usage.toUsage()
	}
	var resp struct {
		Usage *usageJSON `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	return resp.Usage.toUsage()
}

// parseUsageFromPayload 从单个 SSE data payload 中提取 usage（覆盖 out）。
// Responses API 的 usage 出现在结束事件中：response.completed / response.incomplete / response.failed
// （DeepSeek 官方文档：流以这三者之一结束，没有 [DONE]；三者都携带完整 response 对象）。
func parseUsageFromPayload(payload []byte, endpoint string, out **Usage) {
	if endpoint == EndpointResponses {
		var ev struct {
			Type     string `json:"type"`
			Response *struct {
				Usage *responsesUsageJSON `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal(payload, &ev); err != nil {
			return
		}
		if isResponsesTerminalEvent(ev.Type) && ev.Response != nil && ev.Response.Usage != nil {
			*out = ev.Response.Usage.toUsage()
		}
		return
	}
	payload = unwrapEnvelope(payload)
	var chunk struct {
		Usage *usageJSON `json:"usage"`
	}
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return
	}
	if chunk.Usage != nil {
		*out = chunk.Usage.toUsage()
	}
}

// isResponsesTerminalEvent 判断是否为 Responses 流的结束事件。
func isResponsesTerminalEvent(t string) bool {
	switch t {
	case "response.completed", "response.incomplete", "response.failed":
		return true
	}
	return false
}

// ComputeCost 计算费用（price 单位：CNY / 1M tokens），四舍五入到 1e-6。
// 三段独立计算：未命中输入、缓存命中输入、输出。
// 缓存命中价未配置（0）时按输入价计费，避免漏收。
func ComputeCost(u Usage, price store.ModelPrice) float64 {
	cached := u.CachedTokens
	if cached > u.PromptTokens {
		cached = u.PromptTokens
	}
	uncached := u.PromptTokens - cached
	cachedPrice := price.CachedInput
	if cachedPrice == 0 {
		cachedPrice = price.Input // 未配置缓存优惠 = 缓存不省钱，按输入价计
	}
	cost := float64(uncached)*price.Input/1e6 +
		float64(cached)*cachedPrice/1e6 +
		float64(u.CompletionTokens)*price.Output/1e6
	return math.Round(cost*1e6) / 1e6
}
