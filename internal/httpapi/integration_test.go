package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"flash-llm-router/internal/auth"
	"flash-llm-router/internal/gateway"
	"flash-llm-router/internal/store"
)

// ---------------------------------------------------------------- 测试环境

const defaultTestDSN = "postgres://localhost:5432/flash_llm_router_test"

func testDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return defaultTestDSN
}

// ensureTestDB 连接测试库；不存在则尝试创建。PG 不可用时跳过集成测试。
func ensureTestDB(t *testing.T) string {
	t.Helper()
	dsn := testDSN()
	ctx := context.Background()

	if pool, err := pgxpool.New(ctx, dsn); err == nil {
		if err := pool.Ping(ctx); err == nil {
			pool.Close()
			return dsn
		}
		pool.Close()
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("TEST_DATABASE_URL 无效: %v", err)
	}
	dbName := cfg.ConnConfig.Database
	adminCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("TEST_DATABASE_URL 无效: %v", err)
	}
	adminCfg.ConnConfig.Database = "postgres"
	conn, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		t.Skipf("无法连接 PostgreSQL（跳过集成测试）: %v", err)
	}
	defer conn.Close()
	if err := conn.Ping(ctx); err != nil {
		t.Skipf("PostgreSQL 不可用（跳过集成测试）: %v", err)
	}
	quoted := pgx.Identifier{dbName}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("创建测试库失败: %v", err)
	}
	return dsn
}

type env struct {
	t          *testing.T
	st         *store.Store
	srv        *Server
	ts         *httptest.Server
	adminToken string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := ensureTestDB(t)
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("初始化测试库失败: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.Pool.Exec(ctx,
		`TRUNCATE users, sessions, llm_apis, providers, request_logs RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("清库失败: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &Server{
		Store:      st,
		GW:         gateway.New(st, logger),
		Captcha:    auth.NewCaptchaStore(time.Minute),
		Log:        logger,
		StatsTZ:    time.UTC,
		SessionTTL: time.Hour,
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	e := &env{t: t, st: st, srv: srv, ts: ts}
	e.createUser("admin", "TestAccess123", store.RoleAdmin)
	e.adminToken = e.login("admin", "TestAccess123")
	return e
}

func (e *env) createUser(username, password string, role store.Role) {
	e.t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.st.CreateUser(context.Background(), username, hash, role); err != nil {
		e.t.Fatal(err)
	}
}

// login 通过真实 HTTP 流程登录（验证码答案直接从 CaptchaStore 读取）。
func (e *env) login(username, password string) string {
	e.t.Helper()
	resp := e.req(http.MethodGet, "/api/auth/captcha", nil, "")
	defer resp.Body.Close()
	var cap struct {
		CaptchaID string `json:"captcha_id"`
	}
	decodeBody(e.t, resp, &cap)
	code, ok := e.srv.Captcha.Code(cap.CaptchaID)
	if !ok {
		e.t.Fatal("captcha not found")
	}
	resp = e.req(http.MethodPost, "/api/auth/login", map[string]string{
		"username": username, "password": password,
		"captcha_id": cap.CaptchaID, "captcha_code": code,
	}, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("login failed: %d %s", resp.StatusCode, b)
	}
	var out struct {
		Token string `json:"token"`
	}
	decodeBody(e.t, resp, &out)
	return out.Token
}

func (e *env) req(method, path string, body any, token string) *http.Response {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		jb, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(jb)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func (e *env) createLLMAPI(name string, models []string) (int64, string) {
	e.t.Helper()
	resp := e.req(http.MethodPost, "/api/llm-apis", map[string]any{
		"name": name, "models": models, "supports_vision": true, "supports_video": false,
	}, e.adminToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("create llm api failed: %d %s", resp.StatusCode, b)
	}
	var api store.LLMAPI
	decodeBody(e.t, resp, &api)
	return api.ID, api.APIKey
}

func (e *env) createProvider(apiID int64, fields map[string]any) int64 {
	e.t.Helper()
	body := map[string]any{
		"name": "p", "kind": "openai", "base_url": "http://127.0.0.1:1",
		"api_key": "sk-up", "priority": 100, "enabled": true,
		"supports_responses": false, "inject_stream_usage": true,
		"max_body_mb": 0, "max_failures": 0, "cooldown_seconds": 60, "timeout_seconds": 30,
		"price_input": 0, "price_output": 0, "price_cached_input": 0, "model_prices": map[string]any{},
	}
	for k, v := range fields {
		body[k] = v
	}
	resp := e.req(http.MethodPost, fmt.Sprintf("/api/llm-apis/%d/providers", apiID), body, e.adminToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("create provider failed: %d %s", resp.StatusCode, b)
	}
	var p store.Provider
	decodeBody(e.t, resp, &p)
	return p.ID
}

func (e *env) getProvider(id int64) store.Provider {
	e.t.Helper()
	p, err := e.st.GetProvider(context.Background(), id)
	if err != nil {
		e.t.Fatalf("get provider: %v", err)
	}
	return *p
}

func (e *env) latestLog() store.RequestLog {
	e.t.Helper()
	logs, total, err := e.st.ListLogs(context.Background(), store.LogFilter{Page: 1, PageSize: 1})
	if err != nil || total == 0 {
		e.t.Fatalf("latest log: %v total=%d", err, total)
	}
	return logs[0]
}

// chatUpstream 模拟一个 OpenAI 兼容上游：/v1/chat/completions（非流式）。
func chatUpstream(t *testing.T, promptTok, completionTok int64) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"cmpl-1","object":"chat.completion","model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
			promptTok, completionTok, promptTok+completionTok)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// ---------------------------------------------------------------- 认证与权限

func TestLoginFlowAndPermissions(t *testing.T) {
	e := newEnv(t)
	e.createUser("ro", "Ro12345678", store.RoleViewer)
	e.createUser("op", "Op12345678", store.RoleOperator)

	// 错误验证码
	resp := e.req(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "TestAccess123", "captcha_id": "nope", "captcha_code": "AAAA",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong captcha should be 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 错误密码
	resp = e.req(http.MethodGet, "/api/auth/captcha", nil, "")
	var cap struct {
		CaptchaID string `json:"captcha_id"`
	}
	decodeBody(t, resp, &cap)
	resp.Body.Close()
	code, _ := e.srv.Captcha.Code(cap.CaptchaID)
	resp = e.req(http.MethodPost, "/api/auth/login", map[string]string{
		"username": "admin", "password": "WrongPass1", "captcha_id": cap.CaptchaID, "captcha_code": code,
	}, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password should be 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 未登录访问管理 API
	resp = e.req(http.MethodGet, "/api/llm-apis", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated should be 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	roToken := e.login("ro", "Ro12345678")
	opToken := e.login("op", "Op12345678")

	// viewer 可读不可写
	resp = e.req(http.MethodGet, "/api/llm-apis", nil, roToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer read should be 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = e.req(http.MethodPost, "/api/llm-apis", map[string]any{"name": "x"}, roToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer write should be 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// operator 可写 LLM API 但不能管用户
	resp = e.req(http.MethodPost, "/api/llm-apis", map[string]any{"name": "op-api"}, opToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("operator write should be 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = e.req(http.MethodGet, "/api/users", nil, opToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator user-list should be 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------- 代理与计费

func TestProxyChatSuccessBillingAndStats(t *testing.T) {
	e := newEnv(t)
	up, calls := chatUpstream(t, 1000, 500)

	apiID, key := e.createLLMAPI("主力服务", []string{"test-model"})
	e.createProvider(apiID, map[string]any{
		"name": "upstream-a", "base_url": up.URL, "priority": 10,
		"price_input": 2.0, "price_output": 8.0, "price_cached_input": 0.04,
	})

	resp := e.req(http.MethodPost, "/v1/chat/completions",
		`{"model":"test-model","messages":[{"role":"user","content":"ping"}]}`, key)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("proxy status %d: %s", resp.StatusCode, b)
	}
	var out map[string]any
	decodeBody(t, resp, &out)
	resp.Body.Close()
	if *calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", *calls)
	}

	lg := e.latestLog()
	if !lg.Success || lg.ProviderName != "upstream-a" || lg.LLMAPIName != "主力服务" {
		t.Fatalf("log wrong: %+v", lg)
	}
	if lg.PromptTokens != 1000 || lg.CompletionTokens != 500 || lg.TotalTokens != 1500 {
		t.Fatalf("tokens wrong: %+v", lg)
	}
	// 1000*2/1e6 + 500*8/1e6 = 0.002 + 0.004 = 0.006
	if lg.Cost < 0.005999 || lg.Cost > 0.006001 {
		t.Fatalf("cost wrong: %v", lg.Cost)
	}
	if !lg.Priced {
		t.Fatal("log should be priced")
	}

	// Provider 级统计
	resp = e.req(http.MethodGet, fmt.Sprintf("/api/stats/providers?llm_api_id=%d", apiID), nil, e.adminToken)
	var stats struct {
		Providers []store.ProviderUsage `json:"providers"`
		Totals    store.Totals          `json:"totals"`
	}
	decodeBody(t, resp, &stats)
	resp.Body.Close()
	if len(stats.Providers) != 1 || stats.Providers[0].Cost < 0.005999 {
		t.Fatalf("provider stats wrong: %+v", stats)
	}
	if stats.Totals.Requests != 1 || stats.Totals.TotalTokens != 1500 {
		t.Fatalf("totals wrong: %+v", stats.Totals)
	}

	// 时间序列
	resp = e.req(http.MethodGet, "/api/stats/timeseries?granularity=day", nil, e.adminToken)
	var ts struct {
		Points []store.UsagePoint `json:"points"`
	}
	decodeBody(t, resp, &ts)
	resp.Body.Close()
	if len(ts.Points) != 1 || ts.Points[0].Requests != 1 {
		t.Fatalf("timeseries wrong: %+v", ts.Points)
	}

	// 总览
	resp = e.req(http.MethodGet, "/api/overview", nil, e.adminToken)
	var ov struct {
		Total store.Totals `json:"total"`
	}
	decodeBody(t, resp, &ov)
	resp.Body.Close()
	if ov.Total.Requests != 1 {
		t.Fatalf("overview wrong: %+v", ov.Total)
	}
}

// ---------------------------------------------------------------- 故障切换与熔断

func TestFailoverCooldownAndSkip(t *testing.T) {
	e := newEnv(t)

	// A：始终 500；B：成功
	badCalls := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badCalls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	t.Cleanup(bad.Close)
	good, goodCalls := chatUpstream(t, 10, 5)

	apiID, key := e.createLLMAPI("故障切换", []string{"m"})
	aID := e.createProvider(apiID, map[string]any{
		"name": "A-bad", "base_url": bad.URL, "priority": 10,
		"max_failures": 2, "cooldown_seconds": 3600,
		"price_input": 1, "price_output": 1,
	})
	e.createProvider(apiID, map[string]any{
		"name": "B-good", "base_url": good.URL, "priority": 20,
		"price_input": 1, "price_output": 1,
	})

	call := func() store.RequestLog {
		resp := e.req(http.MethodPost, "/v1/chat/completions",
			`{"model":"m","messages":[{"role":"user","content":"hi"}]}`, key)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("proxy status %d: %s", resp.StatusCode, body)
		}
		return e.latestLog()
	}

	// 第 1 次：A 失败（计数 1），切到 B
	lg1 := call()
	if lg1.ProviderName != "B-good" || lg1.Attempts != 2 || len(lg1.AttemptsDetail) != 2 {
		t.Fatalf("log1 wrong: %+v", lg1)
	}
	if lg1.AttemptsDetail[0].Provider != "A-bad" || lg1.AttemptsDetail[0].Status != 500 {
		t.Fatalf("attempt detail wrong: %+v", lg1.AttemptsDetail[0])
	}
	if p := e.getProvider(aID); p.ConsecutiveFailures != 1 || p.CooldownUntil != nil {
		t.Fatalf("A state after call1: %+v", p)
	}

	// 第 2 次：A 再失败（计数 2 >= max_failures）→ 进入冷却
	_ = call()
	if p := e.getProvider(aID); p.ConsecutiveFailures != 2 || p.CooldownUntil == nil || !p.CooldownUntil.After(time.Now()) {
		t.Fatalf("A should be in cooldown: %+v", p)
	}

	// 第 3 次：A 在冷却中被跳过，只打 B
	lg3 := call()
	if lg3.ProviderName != "B-good" || lg3.Attempts != 1 || len(lg3.AttemptsDetail) != 1 {
		t.Fatalf("log3 should skip A: %+v", lg3)
	}
	if badCalls != 2 {
		t.Fatalf("A should be called exactly twice, got %d", badCalls)
	}
	_ = goodCalls

	// 手动恢复 A
	resp := e.req(http.MethodPost, fmt.Sprintf("/api/providers/%d/reset-health", aID), nil, e.adminToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset-health %d", resp.StatusCode)
	}
	resp.Body.Close()
	if p := e.getProvider(aID); p.ConsecutiveFailures != 0 || p.CooldownUntil != nil {
		t.Fatalf("A should be healed: %+v", p)
	}
}

// 任何非 2xx（含 4xx）都算失败并触发切换 —— 按用户拍板的熔断口径。
func TestAnyNon2xxTriggersFailover(t *testing.T) {
	e := newEnv(t)
	badCalls := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badCalls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"your request is bad"}}`))
	}))
	t.Cleanup(bad.Close)
	good, _ := chatUpstream(t, 10, 5)

	apiID, key := e.createLLMAPI("x", []string{"m"})
	e.createProvider(apiID, map[string]any{"name": "bad400", "base_url": bad.URL, "priority": 1})
	e.createProvider(apiID, map[string]any{"name": "good", "base_url": good.URL, "priority": 2})

	resp := e.req(http.MethodPost, "/v1/chat/completions",
		`{"model":"m","messages":[]}`, key)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("should failover to good provider, got %d: %s", resp.StatusCode, body)
	}
	lg := e.latestLog()
	if lg.Attempts != 2 || lg.ProviderName != "good" || lg.AttemptsDetail[0].Status != 400 {
		t.Fatalf("log wrong: %+v", lg)
	}
	if badCalls != 1 {
		t.Fatal("bad provider should have been called once")
	}
}

// 请求体超过 Provider 阈值时直接跳过（不消费该 Provider）。
func TestMaxBodyMBSkip(t *testing.T) {
	e := newEnv(t)
	small, smallCalls := chatUpstream(t, 1, 1)
	big, _ := chatUpstream(t, 1, 1)

	apiID, key := e.createLLMAPI("x", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "small-limit", "base_url": small.URL, "priority": 1,
		"max_body_mb": 0.0001, // ≈105 字节
	})
	e.createProvider(apiID, map[string]any{"name": "normal", "base_url": big.URL, "priority": 2})

	// 构造一个明显超过 105 字节的请求
	longMsg := strings.Repeat("a", 300)
	resp := e.req(http.MethodPost, "/v1/chat/completions",
		fmt.Sprintf(`{"model":"m","messages":[{"role":"user","content":%q}]}`, longMsg), key)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if *smallCalls != 0 {
		t.Fatalf("over-limit provider should be skipped, called %d", *smallCalls)
	}
	lg := e.latestLog()
	if lg.Attempts != 1 || lg.ProviderName != "normal" {
		t.Fatalf("log wrong: %+v", lg)
	}
}

// Responses API 仅直通：不支持感想 responses 的 Provider 被跳过。
func TestResponsesDirectOnly(t *testing.T) {
	e := newEnv(t)
	chatOnly, chatCalls := chatUpstream(t, 1, 1)
	respCalls := 0
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respCalls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp_1","object":"response","status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],
			"usage":{"input_tokens":30,"output_tokens":20,"total_tokens":50}}`)
	}))
	t.Cleanup(native.Close)

	apiID, key := e.createLLMAPI("x", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "chat-only", "base_url": chatOnly.URL, "priority": 1,
		"price_input": 1, "price_output": 1,
	})
	e.createProvider(apiID, map[string]any{
		"name": "native-responses", "base_url": native.URL, "priority": 2,
		"supports_responses": true, "price_input": 2, "price_output": 4,
	})

	resp := e.req(http.MethodPost, "/v1/responses",
		`{"model":"m","input":"hello"}`, key)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("responses status %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	if *chatCalls != 0 {
		t.Fatal("chat-only provider must not receive responses traffic")
	}
	if respCalls != 1 {
		t.Fatalf("native provider calls = %d", respCalls)
	}
	lg := e.latestLog()
	if lg.Endpoint != "responses" || lg.ProviderName != "native-responses" {
		t.Fatalf("log wrong: %+v", lg)
	}
	// 30*2/1e6 + 20*4/1e6 = 0.00006 + 0.00008 = 0.00014
	if lg.Cost < 0.000139 || lg.Cost > 0.000141 {
		t.Fatalf("responses cost wrong: %v", lg.Cost)
	}

	// 全部 Provider 不支持 responses 时返回 503
	apiID2, key2 := e.createLLMAPI("chat-only-api", []string{"m"})
	e.createProvider(apiID2, map[string]any{"name": "c", "base_url": chatOnly.URL})
	resp = e.req(http.MethodPost, "/v1/responses", `{"model":"m","input":"hi"}`, key2)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("no-responses-provider should be 503, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
