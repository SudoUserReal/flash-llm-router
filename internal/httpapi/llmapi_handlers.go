package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"flash-llm-router/internal/store"
)

type llmAPIReq struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Models         []string `json:"models"`
	SupportsVision *bool    `json:"supports_vision"`
	SupportsVideo  *bool    `json:"supports_video"`
	Enabled        *bool    `json:"enabled"`
	APIKey         string   `json:"api_key"` // 可选；留空自动生成
}

func (req *llmAPIReq) toInput(isCreate bool) (store.LLMAPIInput, error) {
	in := store.LLMAPIInput{
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Models:      cleanModels(req.Models),
		Enabled:     true,
	}
	if in.Name == "" {
		return in, errors.New("名称不能为空")
	}
	if len(in.Name) > 100 {
		return in, errors.New("名称过长")
	}
	if req.SupportsVision != nil {
		in.SupportsVision = *req.SupportsVision
	} else if isCreate {
		in.SupportsVision = true
	}
	if req.SupportsVideo != nil {
		in.SupportsVideo = *req.SupportsVideo
	}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	return in, nil
}

func cleanModels(models []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func (s *Server) handleListLLMAPIs(w http.ResponseWriter, r *http.Request, _ *store.User) {
	apis, err := s.Store.ListLLMAPIs(r.Context())
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"llm_apis": apis})
}

func (s *Server) handleCreateLLMAPI(w http.ResponseWriter, r *http.Request, _ *store.User) {
	var req llmAPIReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	in, err := req.toInput(true)
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	api, err := s.Store.CreateLLMAPI(r.Context(), in, req.APIKey)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			errJSON(w, http.StatusConflict, "API Key 已存在，请换一个或留空自动生成")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, api)
}

func (s *Server) handleGetLLMAPI(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	api, err := s.Store.GetLLMAPI(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "LLM API 不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers, err := s.Store.ListProviders(r.Context(), id)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"llm_api": api, "providers": providers})
}

func (s *Server) handleUpdateLLMAPI(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	var req llmAPIReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	// 更新时保留原能力开关（前端会传全量）
	current, err := s.Store.GetLLMAPI(r.Context(), id)
	if err != nil {
		errJSON(w, http.StatusNotFound, "LLM API 不存在")
		return
	}
	if req.SupportsVision == nil {
		v := current.SupportsVision
		req.SupportsVision = &v
	}
	if req.SupportsVideo == nil {
		v := current.SupportsVideo
		req.SupportsVideo = &v
	}
	in, err := req.toInput(false)
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	api, err := s.Store.UpdateLLMAPI(r.Context(), id, in)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "LLM API 不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api)
}

func (s *Server) handleDeleteLLMAPI(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	if err := s.Store.DeleteLLMAPI(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "LLM API 不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRotateKey(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	api, err := s.Store.RotateLLMAPIKey(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "LLM API 不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api)
}
