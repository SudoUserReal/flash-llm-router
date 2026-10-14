package httpapi

import (
	"io"
	"net/http"

	"flash-llm-router/internal/gateway"
	"flash-llm-router/internal/store"
)

func readProxyBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, gateway.MaxRequestBody+1))
	if err != nil {
		proxyErr(w, http.StatusBadRequest, "invalid_request_error", "读取请求体失败: "+err.Error())
		return nil, false
	}
	if len(body) > gateway.MaxRequestBody {
		proxyErr(w, http.StatusRequestEntityTooLarge, "request_too_large", "请求体超过 64MB 上限")
		return nil, false
	}
	if len(body) == 0 {
		proxyErr(w, http.StatusBadRequest, "invalid_request_error", "请求体不能为空")
		return nil, false
	}
	return body, true
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request, api *store.LLMAPI, client *store.APIClient) {
	body, ok := readProxyBody(w, r)
	if !ok {
		return
	}
	s.GW.Proxy(w, r, api, gateway.EndpointChat, body, client)
}

func (s *Server) handleCompletions(w http.ResponseWriter, r *http.Request, api *store.LLMAPI, client *store.APIClient) {
	body, ok := readProxyBody(w, r)
	if !ok {
		return
	}
	s.GW.Proxy(w, r, api, gateway.EndpointCompletions, body, client)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request, api *store.LLMAPI, client *store.APIClient) {
	body, ok := readProxyBody(w, r)
	if !ok {
		return
	}
	s.GW.Proxy(w, r, api, gateway.EndpointResponses, body, client)
}

// handleListModels 返回该 LLM API 手动维护的模型清单（OpenAI /v1/models 格式）。
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request, api *store.LLMAPI, _ *store.APIClient) {
	data := make([]map[string]any, 0, len(api.Models))
	created := api.CreatedAt.Unix()
	for _, m := range api.Models {
		data = append(data, map[string]any{
			"id":       m,
			"object":   "model",
			"created":  created,
			"owned_by": "flash-llm-router",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func (s *Server) handleGetModel(w http.ResponseWriter, r *http.Request, api *store.LLMAPI, _ *store.APIClient) {
	name := r.PathValue("model")
	for _, m := range api.Models {
		if m == name {
			writeJSON(w, http.StatusOK, map[string]any{
				"id":       m,
				"object":   "model",
				"created":  api.CreatedAt.Unix(),
				"owned_by": "flash-llm-router",
			})
			return
		}
	}
	proxyErr(w, http.StatusNotFound, "invalid_request_error",
		"当前 LLM API 未配置模型 "+name)
}
