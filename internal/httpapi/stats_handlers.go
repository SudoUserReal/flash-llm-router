package httpapi

import (
	"net/http"
	"time"

	"flash-llm-router/internal/store"
)

func (s *Server) todayStart() time.Time {
	now := time.Now().In(s.StatsTZ)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.StatsTZ)
}

// handleOverview 首页总览：全局统计 + 每个 LLM API 今日概况。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, _ *store.User) {
	ctx := r.Context()
	now := time.Now().UTC()
	todayStart := s.todayStart().UTC()
	weekAgo := now.AddDate(0, 0, -7)

	total, err := s.Store.Totals(ctx, store.RangeFilter{})
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	today, err := s.Store.Totals(ctx, store.RangeFilter{From: &todayStart, To: &now})
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	last7d, err := s.Store.Totals(ctx, store.RangeFilter{From: &weekAgo, To: &now})
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}

	apis, err := s.Store.ListLLMAPIs(ctx)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	type apiOverview struct {
		store.LLMAPI
		Today *store.Totals `json:"today"`
	}
	list := make([]apiOverview, 0, len(apis))
	for _, a := range apis {
		id := a.ID
		t, err := s.Store.Totals(ctx, store.RangeFilter{LLMAPIID: &id, From: &todayStart, To: &now})
		if err != nil {
			errJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		list = append(list, apiOverview{LLMAPI: a, Today: t})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total":     total,
		"today":     today,
		"last_7d":   last7d,
		"llm_apis":  list,
		"timezone":  s.StatsTZ.String(),
		"server_time": now,
	})
}

// handleTimeseries 时间序列（小时/天粒度）。
func (s *Server) handleTimeseries(w http.ResponseWriter, r *http.Request, _ *store.User) {
	ctx := r.Context()
	from, err := parseTimeParam(r, "from")
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	to, err := parseTimeParam(r, "to")
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	if to == nil {
		to = &now
	}
	if from == nil {
		f := to.AddDate(0, 0, -7)
		from = &f
	}
	granularity := r.URL.Query().Get("granularity")
	if granularity != "hour" && granularity != "day" {
		granularity = "day"
	}
	points, err := s.Store.Timeseries(ctx, store.RangeFilter{
		LLMAPIID: parseIDParam(r, "llm_api_id"),
		From:     from,
		To:       to,
	}, granularity, s.StatsTZ.String())
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"points":      points,
		"granularity": granularity,
		"timezone":    s.StatsTZ.String(),
		"from":        from,
		"to":          to,
	})
}

// handleProviderStats 区间内各 Provider 的费用与 token 明细。
func (s *Server) handleProviderStats(w http.ResponseWriter, r *http.Request, _ *store.User) {
	ctx := r.Context()
	from, err := parseTimeParam(r, "from")
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	to, err := parseTimeParam(r, "to")
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	if to == nil {
		to = &now
	}
	if from == nil {
		f := to.AddDate(0, 0, -7)
		from = &f
	}
	filter := store.RangeFilter{
		LLMAPIID: parseIDParam(r, "llm_api_id"),
		From:     from,
		To:       to,
	}
	providers, err := s.Store.ProviderUsages(ctx, filter)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	totals, err := s.Store.Totals(ctx, filter)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": providers,
		"totals":    totals,
		"from":      from,
		"to":        to,
	})
}

// handleLogs 请求日志分页查询。
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request, _ *store.User) {
	ctx := r.Context()
	from, err := parseTimeParam(r, "from")
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	to, err := parseTimeParam(r, "to")
	if err != nil {
		errJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	f := store.LogFilter{
		LLMAPIID:   parseIDParam(r, "llm_api_id"),
		ProviderID: parseIDParam(r, "provider_id"),
		ClientID:   parseIDParam(r, "client_id"),
		From:       from,
		To:         to,
		Model:      r.URL.Query().Get("model"),
		Page:       parseIntParam(r, "page", 1),
		PageSize:   parseIntParam(r, "page_size", 20),
	}
	switch r.URL.Query().Get("success") {
	case "true":
		v := true
		f.Success = &v
	case "false":
		v := false
		f.Success = &v
	}
	logs, total, err := s.Store.ListLogs(ctx, f)
	if err != nil {
		errJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":      logs,
		"total":     total,
		"page":      f.Page,
		"page_size": f.PageSize,
	})
}
