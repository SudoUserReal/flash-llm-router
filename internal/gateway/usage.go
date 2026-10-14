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
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
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
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
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

// ParseUsageFromJSON 从非流式响应体解析 usage。
func ParseUsageFromJSON(body []byte, endpoint string) *Usage {
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
		if ev.Type == "response.completed" && ev.Response != nil && ev.Response.Usage != nil {
			*out = ev.Response.Usage.toUsage()
		}
		return
	}
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

// ComputeCost 计算费用（price 单位：CNY / 1M tokens），四舍五入到 1e-6。
func ComputeCost(u Usage, price store.ModelPrice) float64 {
	cached := u.CachedTokens
	if cached > u.PromptTokens {
		cached = u.PromptTokens
	}
	uncached := u.PromptTokens - cached
	cost := float64(uncached)*price.Input/1e6 +
		float64(cached)*price.CachedInput/1e6 +
		float64(u.CompletionTokens)*price.Output/1e6
	return math.Round(cost*1e6) / 1e6
}
