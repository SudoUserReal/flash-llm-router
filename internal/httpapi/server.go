// Package httpapi 汇总管理端 API 与对外代理端点。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flash-llm-router/internal/auth"
	"flash-llm-router/internal/gateway"
	"flash-llm-router/internal/store"
)

type Server struct {
	Store      *store.Store
	GW         *gateway.Gateway
	Captcha    *auth.CaptchaStore
	Log        *slog.Logger
	StatsTZ    *time.Location
	SessionTTL time.Duration
	StaticFS   fs.FS // 前端构建产物（web/dist），可为 nil
}

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// ---- 认证与用户
	mux.HandleFunc("GET /api/auth/captcha", s.handleCaptcha)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))

	mux.HandleFunc("GET /api/users", s.requireAdmin(s.handleListUsers))
	mux.HandleFunc("POST /api/users", s.requireAdmin(s.handleCreateUser))
	mux.HandleFunc("PATCH /api/users/{id}", s.requireAdmin(s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/users/{id}", s.requireAdmin(s.handleDeleteUser))

	// ---- LLM API 配置
	mux.HandleFunc("GET /api/llm-apis", s.requireAuth(s.handleListLLMAPIs))
	mux.HandleFunc("POST /api/llm-apis", s.requireWriter(s.handleCreateLLMAPI))
	mux.HandleFunc("GET /api/llm-apis/{id}", s.requireAuth(s.handleGetLLMAPI))
	mux.HandleFunc("PUT /api/llm-apis/{id}", s.requireWriter(s.handleUpdateLLMAPI))
	mux.HandleFunc("DELETE /api/llm-apis/{id}", s.requireWriter(s.handleDeleteLLMAPI))
	mux.HandleFunc("POST /api/llm-apis/{id}/rotate-key", s.requireWriter(s.handleRotateKey))

	// ---- Provider 配置
	mux.HandleFunc("POST /api/llm-apis/{id}/providers", s.requireWriter(s.handleCreateProvider))
	mux.HandleFunc("POST /api/llm-apis/{id}/providers/reorder", s.requireWriter(s.handleReorderProviders))
	mux.HandleFunc("PUT /api/providers/{id}", s.requireWriter(s.handleUpdateProvider))
	mux.HandleFunc("DELETE /api/providers/{id}", s.requireWriter(s.handleDeleteProvider))
	mux.HandleFunc("POST /api/providers/{id}/reset-health", s.requireWriter(s.handleResetProviderHealth))
	mux.HandleFunc("POST /api/providers/{id}/balance", s.requireWriter(s.handleProviderBalance))
	mux.HandleFunc("POST /api/providers/{id}/autofill-price", s.requireWriter(s.handleProviderAutofillPrice))
	mux.HandleFunc("POST /api/providers/{id}/ping", s.requireWriter(s.handleProviderPing))

	// ---- 调用方 / 钱包
	mux.HandleFunc("GET /api/llm-apis/{id}/clients", s.requireAuth(s.handleListClients))
	mux.HandleFunc("POST /api/llm-apis/{id}/clients", s.requireWriter(s.handleCreateClient))
	mux.HandleFunc("PUT /api/clients/{id}", s.requireWriter(s.handleUpdateClient))
	mux.HandleFunc("POST /api/clients/{id}/recharge", s.requireWriter(s.handleRechargeClient))
	mux.HandleFunc("POST /api/clients/{id}/rotate-key", s.requireWriter(s.handleRotateClientKey))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireWriter(s.handleDeleteClient))

	// ---- 统计与日志
	mux.HandleFunc("GET /api/overview", s.requireAuth(s.handleOverview))
	mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.requireWriter(s.handleUpdateSettings))
	mux.HandleFunc("GET /api/stats/timeseries", s.requireAuth(s.handleTimeseries))
	mux.HandleFunc("GET /api/stats/providers", s.requireAuth(s.handleProviderStats))
	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleLogs))

	// ---- 对外代理（调用方钱包 key 或服务主 key 认证）
	mux.HandleFunc("POST /v1/chat/completions", s.proxyAuth(s.handleChatCompletions))
	mux.HandleFunc("POST /v1/completions", s.proxyAuth(s.handleCompletions))
	mux.HandleFunc("POST /v1/responses", s.proxyAuth(s.handleResponses))
	mux.HandleFunc("GET /v1/models", s.proxyAuth(s.handleListModels))
	mux.HandleFunc("GET /v1/models/{model}", s.proxyAuth(s.handleGetModel))

	// ---- 前端静态资源
	mux.Handle("/", s.staticHandler())

	return s.recoverer(s.requestLogger(mux))
}

// ---------------------------------------------------------------- 中间件

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic", "err", rec, "path", r.URL.Path)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/v1/") {
			s.Log.Debug("http", "method", r.Method, "path", r.URL.Path,
				"status", sw.status, "dur_ms", time.Since(start).Milliseconds())
		}
	})
}

// requireAuth 校验管理会话。
func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		if u == nil {
			errJSON(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		next(w, r, u)
	}
}

// requireWriter 要求 operator 或 admin。
func (s *Server) requireWriter(next func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, u *store.User) {
		if !u.Role.CanWrite() {
			errJSON(w, http.StatusForbidden, "当前角色无写入权限")
			return
		}
		next(w, r, u)
	})
}

func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, *store.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, u *store.User) {
		if u.Role != store.RoleAdmin {
			errJSON(w, http.StatusForbidden, "需要管理员权限")
			return
		}
		next(w, r, u)
	})
}

func (s *Server) currentUser(r *http.Request) *store.User {
	token := bearerToken(r)
	if token == "" {
		return nil
	}
	u, err := s.Store.SessionUser(r.Context(), auth.HashToken(token))
	if err != nil {
		return nil
	}
	return u
}

// proxyAuth 校验对外 API key。
// 优先匹配调用方（钱包）凭证：校验钱包余额与日/周/月限额，并返回调用方上下文用于扣费；
// 未匹配时回退到 LLM API 主 key（不限额，供管理/自测使用）。
func (s *Server) proxyAuth(next func(http.ResponseWriter, *http.Request, *store.LLMAPI, *store.APIClient)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := bearerToken(r)
		if key == "" {
			key = strings.TrimSpace(r.Header.Get("x-api-key"))
		}
		if key == "" {
			proxyErr(w, http.StatusUnauthorized, "invalid_request_error",
				"缺少 API key，请使用 Authorization: Bearer <API key> 或 x-api-key 头")
			return
		}

		if client, err := s.Store.GetClientByKey(r.Context(), key); err == nil {
			api, aerr := s.Store.GetLLMAPI(r.Context(), client.LLMAPIID)
			if aerr != nil || !api.Enabled {
				proxyErr(w, http.StatusUnauthorized, "invalid_request_error", "凭证对应的服务不可用")
				return
			}
			if status, code, msg, ok := s.checkClientQuota(r, client); !ok {
				proxyErr(w, status, code, msg)
				return
			}
			next(w, r, api, client)
			return
		}

		api, err := s.Store.GetLLMAPIByKey(r.Context(), key)
		if err != nil {
			proxyErr(w, http.StatusUnauthorized, "invalid_request_error",
				"API key 无效或对应的 LLM API 已禁用")
			return
		}
		next(w, r, api, nil)
	}
}

// checkClientQuota 校验调用方余额与周期限额。
func (s *Server) checkClientQuota(r *http.Request, c *store.APIClient) (status int, code, msg string, ok bool) {
	if !c.Enabled {
		return http.StatusForbidden, "forbidden", "调用方账号已禁用", false
	}
	if c.WalletEnabled && c.WalletBalance <= 0 {
		return http.StatusTooManyRequests, "insufficient_balance",
			"钱包余额不足（余额 ¥0），请联系管理员充值", false
	}
	dStart, wStart, mStart := s.periodStarts()
	spentDay, spentWeek, spentMonth, err := s.Store.ClientPeriodSpend(r.Context(), c.ID, dStart, wStart, mStart)
	if err != nil {
		// 统计失败不阻断请求（保守放行）
		s.Log.Warn("client period spend failed", "err", err, "client", c.ID)
		return 0, "", "", true
	}
	if c.LimitDaily != nil && spentDay >= *c.LimitDaily {
		return http.StatusTooManyRequests, "daily_limit_exceeded",
			fmt.Sprintf("已超出每日消费限额（已用 %s / 限额 %s），下个自然日重置", cny(spentDay), cny(*c.LimitDaily)), false
	}
	if c.LimitWeekly != nil && spentWeek >= *c.LimitWeekly {
		return http.StatusTooManyRequests, "weekly_limit_exceeded",
			fmt.Sprintf("已超出每周消费限额（已用 %s / 限额 %s），下个自然周重置", cny(spentWeek), cny(*c.LimitWeekly)), false
	}
	if c.LimitMonthly != nil && spentMonth >= *c.LimitMonthly {
		return http.StatusTooManyRequests, "monthly_limit_exceeded",
			fmt.Sprintf("已超出每月消费限额（已用 %s / 限额 %s），下月重置", cny(spentMonth), cny(*c.LimitMonthly)), false
	}
	return 0, "", "", true
}

// cny 自适应金额格式（应对小额费用与限额）。
func cny(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v < 0.0001:
		return strings.TrimRight(fmt.Sprintf("%.8f", v), "0")
	case v < 1:
		return strings.TrimRight(fmt.Sprintf("%.6f", v), "0")
	default:
		return strings.TrimRight(fmt.Sprintf("%.2f", v), "0")
	}
}

// periodStarts 返回统计时区下的今日 / 本周一 / 本月起点。
func (s *Server) periodStarts() (day, week, month time.Time) {
	now := time.Now().In(s.StatsTZ)
	day = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.StatsTZ)
	wd := int(now.Weekday())
	if wd == 0 {
		wd = 7 // 周日视为第 7 天
	}
	week = day.AddDate(0, 0, -(wd - 1))
	month = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, s.StatsTZ)
	return
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// proxyErr 以 OpenAI 错误格式返回（对外代理端点用）。
func proxyErr(w http.ResponseWriter, status int, typ, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": typ, "code": typ},
	})
}

func readJSONBody(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("请求体不是合法 JSON")
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func parseTimeParam(r *http.Request, name string) (*time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		t := time.Unix(n, 0).UTC()
		return &t, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, errors.New("时间参数格式错误，应为 RFC3339 或 unix 秒")
	}
	return &t, nil
}

func parseIntParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func parseIDParam(r *http.Request, name string) *int64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}
