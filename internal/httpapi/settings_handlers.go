package httpapi

import (
	"net/http"

	"flash-llm-router/internal/store"
)

// handleGetSettings 返回系统设置。
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, _ *store.User) {
	days, err := s.Store.GetLogRetentionDays(r.Context())
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"log_retention_days": days,
	})
}

type settingsReq struct {
	LogRetentionDays *int `json:"log_retention_days"`
}

// handleUpdateSettings 更新系统设置（目前仅日志保留天数；0 = 永久保留）。
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request, _ *store.User) {
	var req settingsReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.LogRetentionDays == nil {
		errJSON(w, http.StatusBadRequest, "缺少 log_retention_days")
		return
	}
	days := *req.LogRetentionDays
	if days < 0 || days > 3650 {
		errJSON(w, http.StatusBadRequest, "日志保留天数需在 0-3650 之间（0 = 永久保留）")
		return
	}
	if err := s.Store.SetLogRetentionDays(r.Context(), days); err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"log_retention_days": days,
	})
}
