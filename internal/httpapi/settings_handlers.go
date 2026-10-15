package httpapi

import (
	"net/http"
	"strings"

	"flash-llm-router/internal/store"
)

// handleGetSettings 返回系统设置。
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, _ *store.User) {
	ctx := r.Context()
	days, err := s.Store.GetLogRetentionDays(ctx)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	oss, err := s.Store.GetOSSSettings(ctx)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"log_retention_days": days,
		"oss": map[string]any{
			"endpoint":          oss.Endpoint,
			"bucket":            oss.Bucket,
			"access_key_id":     oss.AccessKeyID,
			"access_key_secret": oss.AccessKeySecret,
			"public_base":       oss.PublicBase,
			"prefix":            oss.Prefix,
		},
	})
}

type settingsReq struct {
	LogRetentionDays *int    `json:"log_retention_days"`
	OSS              *ossReq `json:"oss"`
}

type ossReq struct {
	Endpoint        *string `json:"endpoint"`
	Bucket          *string `json:"bucket"`
	AccessKeyID     *string `json:"access_key_id"`
	AccessKeySecret *string `json:"access_key_secret"`
	PublicBase      *string `json:"public_base"`
	Prefix          *string `json:"prefix"`
}

// handleUpdateSettings 更新系统设置（日志保留天数 / OSS 配置；仅更新传入的字段）。
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request, _ *store.User) {
	var req settingsReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.LogRetentionDays == nil && req.OSS == nil {
		errJSON(w, http.StatusBadRequest, "没有可更新的设置项")
		return
	}

	if req.LogRetentionDays != nil {
		days := *req.LogRetentionDays
		if days < 0 || days > 3650 {
			errJSON(w, http.StatusBadRequest, "日志保留天数需在 0-3650 之间（0 = 永久保留）")
			return
		}
		if err := s.Store.SetLogRetentionDays(r.Context(), days); err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if req.OSS != nil {
		cur, err := s.Store.GetOSSSettings(r.Context())
		if err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		next := cur
		if req.OSS.Endpoint != nil {
			next.Endpoint = strings.TrimSpace(*req.OSS.Endpoint)
		}
		if req.OSS.Bucket != nil {
			next.Bucket = strings.TrimSpace(*req.OSS.Bucket)
		}
		if req.OSS.AccessKeyID != nil {
			next.AccessKeyID = strings.TrimSpace(*req.OSS.AccessKeyID)
		}
		if req.OSS.AccessKeySecret != nil {
			next.AccessKeySecret = strings.TrimSpace(*req.OSS.AccessKeySecret)
		}
		if req.OSS.PublicBase != nil {
			next.PublicBase = strings.TrimSpace(*req.OSS.PublicBase)
		}
		if req.OSS.Prefix != nil {
			next.Prefix = strings.TrimSpace(*req.OSS.Prefix)
		}
		if err := s.Store.SetOSSSettings(r.Context(), next); err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 让网关尽快用上新配置
		s.GW.InvalidateOSSCache()
	}

	s.handleGetSettings(w, r, nil)
}
