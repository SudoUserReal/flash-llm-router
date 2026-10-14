package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"flash-llm-router/internal/store"
)

func (e *env) createClient(apiID int64, fields map[string]any) store.APIClient {
	e.t.Helper()
	body := map[string]any{
		"name": "wallet-user", "wallet_balance": 0.02, "wallet_enabled": true,
		"limit_daily": nil, "limit_weekly": nil, "limit_monthly": nil,
	}
	for k, v := range fields {
		body[k] = v
	}
	resp := e.req(http.MethodPost, fmt.Sprintf("/api/llm-apis/%d/clients", apiID), body, e.adminToken)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("create client failed: %d %s", resp.StatusCode, b)
	}
	var c store.APIClient
	decodeBody(e.t, resp, &c)
	return c
}

func (e *env) getClient(id int64) store.APIClient {
	e.t.Helper()
	c, err := e.st.GetClient(context.Background(), id)
	if err != nil {
		e.t.Fatalf("get client: %v", err)
	}
	return *c
}

func (e *env) callAs(key, model string) (int, map[string]any) {
	e.t.Helper()
	resp := e.req(http.MethodPost, "/v1/chat/completions",
		fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model), key)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errType(t *testing.T, body map[string]any) string {
	t.Helper()
	e, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error object: %+v", body)
	}
	typ, _ := e["type"].(string)
	return typ
}

// 每次调用 cost = 1000*2/1e6 + 500*8/1e6 = 0.006 CNY
func TestClientWalletChargingAndLimits(t *testing.T) {
	e := newEnv(t)
	up, _ := chatUpstream(t, 1000, 500)

	apiID, mainKey := e.createLLMAPI("钱包服务", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "p", "base_url": up.URL, "price_input": 2.0, "price_output": 8.0,
	})

	c := e.createClient(apiID, map[string]any{
		"name": "团队A", "wallet_balance": 0.02, "limit_daily": 0.01,
	})
	if c.APIKey == "" {
		t.Fatal("client key should be generated")
	}

	// 1) 正常调用 → 扣费 0.006
	if status, body := e.callAs(c.APIKey, "m"); status != http.StatusOK {
		t.Fatalf("first call should succeed, got %d: %v", status, body)
	}
	got := e.getClient(c.ID)
	if diff := got.WalletBalance - 0.014; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance after call1 = %v, want 0.014", got.WalletBalance)
	}
	if got.TotalSpent < 0.0059 || got.TotalSpent > 0.0061 {
		t.Fatalf("total spent = %v, want ~0.006", got.TotalSpent)
	}
	// 日志归属调用方
	lg := e.latestLog()
	if lg.ClientID == nil || *lg.ClientID != c.ID || lg.ClientName != "团队A" {
		t.Fatalf("log should carry client info: %+v", lg)
	}

	// 2) 第二次：日消费 0.006 < 0.01 → 允许
	if status, body := e.callAs(c.APIKey, "m"); status != http.StatusOK {
		t.Fatalf("second call should succeed, got %d: %v", status, body)
	}
	got = e.getClient(c.ID)
	if diff := got.WalletBalance - 0.008; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance after call2 = %v, want 0.008", got.WalletBalance)
	}

	// 3) 第三次：日消费 0.012 >= 0.01 → 429
	status, body := e.callAs(c.APIKey, "m")
	if status != http.StatusTooManyRequests {
		t.Fatalf("third call should be rate-limited, got %d: %v", status, body)
	}
	if typ := errType(t, body); typ != "daily_limit_exceeded" {
		t.Fatalf("error type = %q, want daily_limit_exceeded", typ)
	}

	// 4) 充值 +1 元
	resp := e.req(http.MethodPost, fmt.Sprintf("/api/clients/%d/recharge", c.ID),
		map[string]any{"amount": 1.0}, e.adminToken)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recharge failed: %d", resp.StatusCode)
	}
	got = e.getClient(c.ID)
	if diff := got.WalletBalance - 1.008; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("balance after recharge = %v, want 1.008", got.WalletBalance)
	}

	// 5) 清空日限额后恢复可用
	resp = e.req(http.MethodPut, fmt.Sprintf("/api/clients/%d", c.ID), map[string]any{
		"name": "团队A", "limit_daily": nil,
	}, e.adminToken)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear limit failed: %d", resp.StatusCode)
	}
	if status, body := e.callAs(c.APIKey, "m"); status != http.StatusOK {
		t.Fatalf("call after clearing limit should succeed, got %d: %v", status, body)
	}

	// 6) 服务主 key 不受钱包/限额约束
	if status, body := e.callAs(mainKey, "m"); status != http.StatusOK {
		t.Fatalf("main key should bypass quota, got %d: %v", status, body)
	}
}

// 钱包余额耗尽 → 拒绝；充值后恢复
func TestClientInsufficientBalance(t *testing.T) {
	e := newEnv(t)
	up, _ := chatUpstream(t, 10, 5)

	apiID, _ := e.createLLMAPI("余额服务", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "p", "base_url": up.URL, "price_input": 1.0, "price_output": 1.0,
	})

	c := e.createClient(apiID, map[string]any{"name": "穷用户", "wallet_balance": 0})

	status, body := e.callAs(c.APIKey, "m")
	if status != http.StatusTooManyRequests {
		t.Fatalf("zero-balance call should be rejected, got %d: %v", status, body)
	}
	if typ := errType(t, body); typ != "insufficient_balance" {
		t.Fatalf("error type = %q, want insufficient_balance", typ)
	}

	// 充值后可用
	resp := e.req(http.MethodPost, fmt.Sprintf("/api/clients/%d/recharge", c.ID),
		map[string]any{"amount": 5.0}, e.adminToken)
	resp.Body.Close()
	if status, body := e.callAs(c.APIKey, "m"); status != http.StatusOK {
		t.Fatalf("after recharge call should succeed, got %d: %v", status, body)
	}
}

// 关闭钱包限制后，余额为 0 也可继续使用
func TestClientWalletDisabled(t *testing.T) {
	e := newEnv(t)
	up, _ := chatUpstream(t, 10, 5)
	apiID, _ := e.createLLMAPI("不限额", []string{"m"})
	e.createProvider(apiID, map[string]any{"name": "p", "base_url": up.URL})

	c := e.createClient(apiID, map[string]any{"name": "internal", "wallet_balance": 0, "wallet_enabled": false})
	if status, body := e.callAs(c.APIKey, "m"); status != http.StatusOK {
		t.Fatalf("wallet-disabled client should pass, got %d: %v", status, body)
	}
}

// 月/周限额同样生效（直接用月限额验证）
func TestClientMonthlyLimit(t *testing.T) {
	e := newEnv(t)
	up, _ := chatUpstream(t, 1000, 500)
	apiID, _ := e.createLLMAPI("月限额", []string{"m"})
	e.createProvider(apiID, map[string]any{
		"name": "p", "base_url": up.URL, "price_input": 2.0, "price_output": 8.0,
	})
	c := e.createClient(apiID, map[string]any{"name": "月用户", "wallet_balance": 100, "limit_monthly": 0.006})

	if status, body := e.callAs(c.APIKey, "m"); status != http.StatusOK {
		t.Fatalf("first call should pass, got %d: %v", status, body)
	}
	status, body := e.callAs(c.APIKey, "m")
	if status != http.StatusTooManyRequests {
		t.Fatalf("second call should hit monthly limit, got %d", status)
	}
	if typ := errType(t, body); typ != "monthly_limit_exceeded" {
		t.Fatalf("error type = %q, want monthly_limit_exceeded", typ)
	}
}
