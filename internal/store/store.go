package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// Store 封装 PostgreSQL 访问。
type Store struct {
	Pool *pgxpool.Pool
}

// New 建立连接池并执行迁移。
func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

// ---------------------------------------------------------------- models

type Role string

const (
	RoleAdmin    Role = "admin"    // 全部权限（含用户管理）
	RoleOperator Role = "operator" // 管理 LLM API / Provider，不能管用户
	RoleViewer   Role = "viewer"   // 只读
)

func (r Role) CanWrite() bool { return r == RoleAdmin || r == RoleOperator }

type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         Role       `json:"role"`
	Enabled      bool       `json:"enabled"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}

type LLMAPI struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	APIKey         string    `json:"api_key"`
	Description    string    `json:"description"`
	Models         []string  `json:"models"`
	SupportsVision bool      `json:"supports_vision"`
	SupportsVideo  bool      `json:"supports_video"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	ProviderCount  int       `json:"provider_count"`
}

// ModelPrice 单个模型的价格覆盖（CNY / 1M tokens）。
type ModelPrice struct {
	Input       float64 `json:"input"`
	Output      float64 `json:"output"`
	CachedInput float64 `json:"cached_input"`
}

type Provider struct {
	ID                int64   `json:"id"`
	LLMAPIID          int64   `json:"llm_api_id"`
	Name              string  `json:"name"`
	Kind              string  `json:"kind"`
	BaseURL           string  `json:"base_url"`
	APIKey            string  `json:"api_key"`
	Priority          int     `json:"priority"`
	Enabled           bool    `json:"enabled"`
	SupportsResponses bool    `json:"supports_responses"`
	InjectStreamUsage bool    `json:"inject_stream_usage"`
	MaxBodyMB         float64 `json:"max_body_mb"`
	MaxFailures       int     `json:"max_failures"`
	CooldownSeconds   int     `json:"cooldown_seconds"`
	TimeoutSeconds    int     `json:"timeout_seconds"`

	PriceInput       float64               `json:"price_input"`
	PriceOutput      float64               `json:"price_output"`
	PriceCachedInput float64               `json:"price_cached_input"`
	ModelPrices      map[string]ModelPrice `json:"model_prices"`

	ConsecutiveFailures int             `json:"consecutive_failures"`
	CooldownUntil       *time.Time      `json:"cooldown_until"`
	LastError           string          `json:"last_error"`
	LastErrorAt         *time.Time      `json:"last_error_at"`
	LastBalance         json.RawMessage `json:"last_balance"`
	BalanceCheckedAt    *time.Time      `json:"balance_checked_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PriceFor 返回某模型适用的价格：优先模型覆盖，否则回退 Provider 默认价。
func (p *Provider) PriceFor(model string) ModelPrice {
	if mp, ok := p.ModelPrices[model]; ok {
		return mp
	}
	return ModelPrice{
		Input:       p.PriceInput,
		Output:      p.PriceOutput,
		CachedInput: p.PriceCachedInput,
	}
}

// HasPrice 判断该 Provider 是否配置过任何价格。
func (p *Provider) HasPrice() bool {
	if p.PriceInput != 0 || p.PriceOutput != 0 || p.PriceCachedInput != 0 {
		return true
	}
	for _, mp := range p.ModelPrices {
		if mp.Input != 0 || mp.Output != 0 || mp.CachedInput != 0 {
			return true
		}
	}
	return false
}

// InCooldown 判断 Provider 当前是否处于熔断冷却期。
func (p *Provider) InCooldown(now time.Time) bool {
	return p.CooldownUntil != nil && p.CooldownUntil.After(now)
}

type AttemptInfo struct {
	Provider  string `json:"provider"`
	Status    int    `json:"status"`
	Error     string `json:"error"`
	LatencyMS int64  `json:"latency_ms"`
}

type RequestLog struct {
	ID               int64         `json:"id"`
	CreatedAt        time.Time     `json:"created_at"`
	LLMAPIID         *int64        `json:"llm_api_id"`
	LLMAPIName       string        `json:"llm_api_name"`
	ProviderID       *int64        `json:"provider_id"`
	ProviderName     string        `json:"provider_name"`
	Endpoint         string        `json:"endpoint"`
	Model            string        `json:"model"`
	Stream           bool          `json:"stream"`
	StatusCode       int           `json:"status_code"`
	Success          bool          `json:"success"`
	Error            string        `json:"error"`
	RequestBytes     int64         `json:"request_bytes"`
	LatencyMS        int64         `json:"latency_ms"`
	PromptTokens     int64         `json:"prompt_tokens"`
	CachedTokens     int64         `json:"cached_tokens"`
	CompletionTokens int64         `json:"completion_tokens"`
	TotalTokens      int64         `json:"total_tokens"`
	Cost             float64       `json:"cost"`
	Priced           bool          `json:"priced"`
	Attempts         int           `json:"attempts"`
	AttemptsDetail   []AttemptInfo `json:"attempts_detail"`
	ClientIP         string        `json:"client_ip"`
	ClientID         *int64        `json:"client_id"`
	ClientName       string        `json:"client_name"`
}

// LogInput 写入日志的输入。
type LogInput struct {
	LLMAPIID         *int64
	LLMAPIName       string
	ProviderID       *int64
	ProviderName     string
	Endpoint         string
	Model            string
	Stream           bool
	StatusCode       int
	Success          bool
	Error            string
	RequestBytes     int64
	LatencyMS        int64
	PromptTokens     int64
	CachedTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Cost             float64
	Priced           bool
	Attempts         int
	AttemptsDetail   []AttemptInfo
	ClientIP         string
	ClientID         *int64
	ClientName       string
}

type UsagePoint struct {
	Bucket           time.Time `json:"bucket"`
	Requests         int64     `json:"requests"`
	Success          int64     `json:"success"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CachedTokens     int64     `json:"cached_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	TotalTokens      int64     `json:"total_tokens"`
	Cost             float64   `json:"cost"`
}

type ProviderUsage struct {
	ProviderID       int64   `json:"provider_id"`
	ProviderName     string  `json:"provider_name"`
	Requests         int64   `json:"requests"`
	Success          int64   `json:"success"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
	AvgLatencyMS     float64 `json:"avg_latency_ms"`
}

type Totals struct {
	Requests         int64   `json:"requests"`
	Success          int64   `json:"success"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CachedTokens     int64   `json:"cached_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
}

type LogFilter struct {
	LLMAPIID   *int64
	ProviderID *int64
	ClientID   *int64
	From       *time.Time
	To         *time.Time
	Success    *bool
	Model      string
	Page       int
	PageSize   int
}
