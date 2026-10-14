package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"flash-llm-router/internal/store"
)

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request, _ *store.User) {
	apiID, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	day, week, month := s.periodStarts()
	clients, err := s.Store.ListClients(r.Context(), apiID, day, week, month)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": clients})
}

type clientReq struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	APIKey        string   `json:"api_key"`
	WalletBalance float64  `json:"wallet_balance"`
	WalletEnabled *bool    `json:"wallet_enabled"`
	LimitDaily    *float64 `json:"limit_daily"`
	LimitWeekly   *float64 `json:"limit_weekly"`
	LimitMonthly  *float64 `json:"limit_monthly"`
	Enabled       *bool    `json:"enabled"`
}

func (req *clientReq) toInput() (store.ClientInput, error) {
	in := store.ClientInput{
		Name:          strings.TrimSpace(req.Name),
		Description:   strings.TrimSpace(req.Description),
		APIKey:        strings.TrimSpace(req.APIKey),
		WalletBalance: req.WalletBalance,
		WalletEnabled: true,
		Enabled:       true,
		LimitDaily:    req.LimitDaily,
		LimitWeekly:   req.LimitWeekly,
		LimitMonthly:  req.LimitMonthly,
	}
	if in.Name == "" {
		return in, errors.New("名称不能为空")
	}
	if in.WalletBalance < 0 {
		return in, errors.New("初始余额不能为负数")
	}
	if req.WalletEnabled != nil {
		in.WalletEnabled = *req.WalletEnabled
	}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	for name, v := range map[string]*float64{"日": in.LimitDaily, "周": in.LimitWeekly, "月": in.LimitMonthly} {
		if v != nil && *v < 0 {
			return in, errors.New(name + "限额不能为负数")
		}
	}
	return in, nil
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request, _ *store.User) {
	apiID, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	if _, err := s.Store.GetLLMAPI(r.Context(), apiID); err != nil {
		errJSON(w, http.StatusNotFound, "LLM API 不存在")
		return
	}
	var req clientReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	in, err := req.toInput()
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := s.Store.CreateClient(r.Context(), apiID, in)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			errJSON(w, http.StatusConflict, "API Key 已存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	var req clientReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		errJSON(w, http.StatusBadRequest, "名称不能为空")
		return
	}
	patch := store.ClientPatch{
		Name:          &req.Name,
		Description:   &req.Description,
		WalletEnabled: req.WalletEnabled,
		Enabled:       req.Enabled,
		LimitDaily:    req.LimitDaily,
		LimitWeekly:   req.LimitWeekly,
		LimitMonthly:  req.LimitMonthly,
	}
	c, err := s.Store.UpdateClient(r.Context(), id, patch)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "调用方不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

type rechargeReq struct {
	Amount float64 `json:"amount"` // 正数充值，负数扣减
}

func (s *Server) handleRechargeClient(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	var req rechargeReq
	if err := readJSONBody(r, &req); err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Amount == 0 {
		errJSON(w, http.StatusBadRequest, "金额不能为 0")
		return
	}
	c, err := s.Store.RechargeClient(r.Context(), id, req.Amount)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "调用方不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleRotateClientKey(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	c, err := s.Store.RotateClientKey(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "调用方不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request, _ *store.User) {
	id, err := pathID(r)
	if err != nil {
		errJSON(w, http.StatusBadRequest, "id 无效")
		return
	}
	if err := s.Store.DeleteClient(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			errJSON(w, http.StatusNotFound, "调用方不存在")
			return
		}
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
