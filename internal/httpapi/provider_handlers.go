package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"flash-llm-router/internal/gateway"
	"flash-llm-router/internal/store"
)

type providerReq struct {
	Name              string                   `json:"name"`
	Kind              string                   `json:"kind"`
	BaseURL           string                   `json:"base_url"`
	APIKey            string                   `json:"api_key"`
	Priority          int                      `json:"priority"`
	Enabled           *bool                    `json:"enabled"`
	SupportsResponses *bool                    `json:"supports_responses"`
	InjectStreamUsage *bool                    `json:"inject_stream_usage"`
	MaxBodyMB         float64                  `json:"max_body_mb"`
	MaxFailures       int                      `json:"max_failures"`
	CooldownSeconds   int                      `json:"cooldown_seconds"`
	TimeoutSeconds    int                      `json:"timeout_seconds"`
	PriceInput        float64                  `json:"price_input"`
	PriceOutput       float64                  `json:"price_output"`
	PriceCachedInput  float64                  `json:"price_cached_input"`
	ModelPrices       map[string]store.ModelPrice `json:"model_prices"`
}

func validKind(kind string) bool {
	switch kind {
	case "openai", "deepseek", "custom":
		return true
	}
	return false
}

func (req *providerReq) toInput(isCreate bool, defaults *store.Provider) (store.ProviderInput, error) {
	in := store.ProviderInput{
		Name:              strings.TrimSpace(req.Name),
		Kind:              strings.TrimSpace(req.Kind),
		BaseURL:           strings.TrimSpace(req.BaseURL),
		APIKey:            strings.TrimSpace(req.APIKey),
		Priority:          req.Priority,
		MaxBodyMB:         req.MaxBodyMB,
		MaxFailures:       req.MaxFailures,
		CooldownSeconds:   req.CooldownSeconds,
		TimeoutSeconds:    req.TimeoutSeconds,
		PriceInput:        req.PriceInput,
		PriceOutput:       req.PriceOutput,
		PriceCachedInput:  req.PriceCachedInput,
		ModelPrices:       req.ModelPrices,
	}
	if in.Name == "" {
		return in, errors.New("Provider 名称不能为空")
	}
	if in.Kind == "" {
		in.Kind = "openai"
	}
	if !validKind(in.Kind) {
		return in, errors.New("kind 必须是 openai / deepseek / custom")
	}
	if u, err := url.Parse(in.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
		return in, errors.New("base_url 必须是完整 URL，例如 https://api.deepseek.com")
	}
	if in.APIKey == "" {
		return in, errors.New("API Key 不能为空")
	}
	if in.MaxBodyMB < 0 {
		in.MaxBodyMB = 0
	}
	if in.MaxFailures < 0 {
		in.MaxFailures = 0
	}
	if in.CooldownSeconds <= 0 {
		in.CooldownSeconds = 60
	}
	if in.TimeoutSeconds <= 0 {
		in.TimeoutSeconds = 300
	}
	if in.TimeoutSeconds > 3600 {
		in.TimeoutSeconds = 3600
	}
	for _, v := range []float64{in.PriceInput, in.PriceOutput, in.PriceCachedInput} {
		if v < 0 {
			return in, errors.New("价格不能为负数")
		}
	}
	for m, mp := range in.ModelPrices {
		if strings.TrimSpace(m) == "" || mp.Input < 0 || mp.Output < 0 || mp.CachedInput < 0 {
			return in, errors.New("模型价格配置不合法")
		}
	}
	if in.ModelPrices == nil {
		in.ModelPrices = map[string]store.ModelPrice{}
	}

	// 布尔开关：未传时按默认（创建）或保留旧值（更新）
	var base store.Provider
	if defaults != nil {
		base = *defaults
	} else if isCreate {
		base = store.Provider{Enabled: true, InjectStreamUsage: true}
	}
	in.Enabled = base.Enabled
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	in.SupportsResponses = base.SupportsResponses
	if req.SupportsResponses != nil {
		in.SupportsResponses = *req.SupportsResponses
	}
	in.InjectStreamUsage = base.InjectStreamUsage
	if req.InjectStreamUsage != nil {
		in.InjectStreamUsage = *req.InjectStreamUsage
	}
	return in, nil
}

func providerInputFromProvider(p *store.Provider) store.ProviderInput {
	return store.ProviderInput{
		Name:              p.Name,
		Kind:              p.Kind,
		BaseURL:           p.BaseURL,
		APIKey:            p.APIKey,
		Priority:          p.Priority,
		Enabled:           p.Enabled,
		SupportsResponses: p.SupportsResponses,
		InjectStreamUsage: p.InjectStreamUsage,
		MaxBodyMB:         p.MaxBodyMB,
		MaxFailures:       p.MaxFailures,
		CooldownSeconds:   p.CooldownSeconds,
		TimeoutSeconds:    p.TimeoutSeconds,
		PriceInput:        p.PriceInput,
		PriceOutput:       p.PriceOutput,
		PriceCachedInput:  p.PriceCachedInput,
		ModelPrices:       p.ModelPrices,
	}
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request, _ *store.User) {
	apiID, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	if _, err := s.Store.GetLLMAPI(r.Context(), apiID); err != nil {
		errJSON(w, http.StatusNotFound, "LLM API 不存在")
		return
	}
	var req providerReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	in, err := req.toInput(true, nil)
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.Store.CreateProvider(r.Context(), apiID, in)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	current, err := s.Store.GetProvider(r.Context(), id)
	if err != nil {
		errJSON(w, http.StatusNotFound, "Provider 不存在")
		return
	}
	var req providerReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	in, err := req.toInput(false, current)
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.Store.UpdateProvider(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "Provider 不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 上游地址或 Key 变更后，旧的健康状态不再可信：自动清除熔断
	if current.BaseURL != in.BaseURL || current.APIKey != in.APIKey {
		_ = s.Store.ResetProviderHealth(r.Context(), id)
		if refreshed, err := s.Store.GetProvider(r.Context(), id); err == nil {
			updated = refreshed
		}
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	if err := s.Store.DeleteProvider(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "Provider 不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReorderProviders(w http.ResponseWriter, r *http.Request, _ *store.User) {
	apiID, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.IDs) == 0 {
		errJSON(w, http.StatusBadRequest, "ids 不能为空")
		return
	}
	if err := s.Store.ReorderProviders(r.Context(), apiID, req.IDs); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	providers, err := s.Store.ListProviders(r.Context(), apiID)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Server) handleResetProviderHealth(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	if err := s.Store.ResetProviderHealth(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "Provider 不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := s.Store.GetProvider(r.Context(), id)
	writeJSON(w, http.StatusOK, p)
}

// handleProviderBalance 查询 DeepSeek 余额并缓存到 Provider。
func (s *Server) handleProviderBalance(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	p, err := s.Store.GetProvider(r.Context(), id)
	if err != nil {
		errJSON(w, http.StatusNotFound, "Provider 不存在")
		return
	}
	if p.Kind != "deepseek" {
		errJSON(w, http.StatusBadRequest, "仅 DeepSeek 类型的 Provider 支持余额查询")
		return
	}
	bal, err := gateway.DeepSeekBalance(r.Context(), p.BaseURL, p.APIKey)
	if err != nil {
		errJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	if err := s.Store.SaveProviderBalance(r.Context(), id, bal); err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := s.Store.GetProvider(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"provider": updated, "balance": bal})
}

// handleProviderAutofillPrice 用内置 DeepSeek 官方价目一键填充价格配置（可选高峰/空闲时段）。
func (s *Server) handleProviderAutofillPrice(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	var req struct {
		Tier string `json:"tier"`
	}
	_ = readJSONBody(r, &req)
	tier := gateway.DeepSeekTier(req.Tier)
	if tier != gateway.TierOffPeak {
		tier = gateway.TierPeak
	}
	p, err := s.Store.GetProvider(r.Context(), id)
	if err != nil {
		errJSON(w, http.StatusNotFound, "Provider 不存在")
		return
	}
	if p.Kind != "deepseek" {
		errJSON(w, http.StatusBadRequest, "仅 DeepSeek 类型的 Provider 支持自动配置价格")
		return
	}
	in := providerInputFromProvider(p)
	def := gateway.DeepSeekDefaultPrice(tier)
	in.PriceInput, in.PriceOutput, in.PriceCachedInput = def.Input, def.Output, def.CachedInput
	in.ModelPrices = gateway.DeepSeekDefaultPrices(tier)
	updated, err := s.Store.UpdateProvider(r.Context(), id, in)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	note := "已按 DeepSeek 官方价目（高峰时段价）填充，模型价目覆盖已同步更新"
	if tier == gateway.TierOffPeak {
		note = "已按 DeepSeek 官方价目（空闲时段价）填充，模型价目覆盖已同步更新"
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": updated, "tier": tier, "note": note})
}

// handleProviderPing 测试 Provider 连通性（请求上游 /v1/models，不消耗 token）。
func (s *Server) handleProviderPing(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	p, err := s.Store.GetProvider(r.Context(), id)
	if err != nil {
		errJSON(w, http.StatusNotFound, "Provider 不存在")
		return
	}
	status, latency, err := s.GW.PingProvider(r.Context(), p)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "status": status, "latency_ms": latency, "error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "status": status, "latency_ms": latency,
		"note": fmt.Sprintf("上游 /v1/models 返回 %d", status),
	})
}
