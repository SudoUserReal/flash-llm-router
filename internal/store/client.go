package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// APIClient 调用方（钱包账户）：持有独立 API Key，受钱包余额与周期限额约束。
type APIClient struct {
	ID            int64      `json:"id"`
	LLMAPIID      int64      `json:"llm_api_id"`
	Name          string     `json:"name"`
	APIKey        string     `json:"api_key"`
	Description   string     `json:"description"`
	WalletBalance float64    `json:"wallet_balance"`
	WalletEnabled bool       `json:"wallet_enabled"`
	LimitDaily    *float64   `json:"limit_daily"`
	LimitWeekly   *float64   `json:"limit_weekly"`
	LimitMonthly  *float64   `json:"limit_monthly"`
	TotalSpent    float64    `json:"total_spent"`
	Enabled       bool       `json:"enabled"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	// 当前周期用量（列表接口填充）
	SpentDay   float64 `json:"spent_day"`
	SpentWeek  float64 `json:"spent_week"`
	SpentMonth float64 `json:"spent_month"`
}

const clientCols = `id, llm_api_id, name, api_key, description,
	wallet_balance::float8, wallet_enabled, limit_daily::float8, limit_weekly::float8, limit_monthly::float8,
	total_spent::float8, enabled, created_at, updated_at`

func scanClient(row pgx.Row) (*APIClient, error) {
	c := &APIClient{}
	if err := row.Scan(&c.ID, &c.LLMAPIID, &c.Name, &c.APIKey, &c.Description,
		&c.WalletBalance, &c.WalletEnabled, &c.LimitDaily, &c.LimitWeekly, &c.LimitMonthly,
		&c.TotalSpent, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return c, nil
}

// GenerateClientKey 生成调用方 API Key。
func GenerateClientKey() string {
	return GenerateAPIKey()
}

type ClientInput struct {
	Name          string
	Description   string
	APIKey        string
	WalletBalance float64
	WalletEnabled bool
	LimitDaily    *float64
	LimitWeekly   *float64
	LimitMonthly  *float64
	Enabled       bool
}

func (s *Store) CreateClient(ctx context.Context, llmAPIID int64, in ClientInput) (*APIClient, error) {
	key := strings.TrimSpace(in.APIKey)
	if key == "" {
		key = GenerateClientKey()
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO api_clients (llm_api_id, name, api_key, description, wallet_balance, wallet_enabled,
			limit_daily, limit_weekly, limit_monthly, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING `+clientCols,
		llmAPIID, in.Name, key, in.Description, in.WalletBalance, in.WalletEnabled,
		in.LimitDaily, in.LimitWeekly, in.LimitMonthly, in.Enabled)
	c, err := scanClient(row)
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return c, nil
}

func (s *Store) GetClient(ctx context.Context, id int64) (*APIClient, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+clientCols+` FROM api_clients WHERE id = $1`, id)
	c, err := scanClient(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) GetClientByKey(ctx context.Context, apiKey string) (*APIClient, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+clientCols+` FROM api_clients WHERE api_key = $1 AND enabled`, apiKey)
	c, err := scanClient(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ListClients 返回某 LLM API 下的调用方，并附带本周期用量（day/week/month 起止由调用方按统计时区计算）。
func (s *Store) ListClients(ctx context.Context, llmAPIID int64, dayStart, weekStart, monthStart time.Time) ([]APIClient, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+clientCols+`
		FROM api_clients WHERE llm_api_id = $1 ORDER BY id`, llmAPIID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIClient{}
	ids := []int64{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	usage, err := s.clientPeriodSpend(ctx, ids, dayStart, weekStart, monthStart)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if u, ok := usage[out[i].ID]; ok {
			out[i].SpentDay, out[i].SpentWeek, out[i].SpentMonth = u.Day, u.Week, u.Month
		}
	}
	return out, nil
}

type clientSpend struct{ Day, Week, Month float64 }

func (s *Store) clientPeriodSpend(ctx context.Context, ids []int64, dayStart, weekStart, monthStart time.Time) (map[int64]clientSpend, error) {
	if len(ids) == 0 {
		return map[int64]clientSpend{}, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT client_id,
			COALESCE(SUM(cost) FILTER (WHERE created_at >= $2), 0)::float8,
			COALESCE(SUM(cost) FILTER (WHERE created_at >= $3), 0)::float8,
			COALESCE(SUM(cost) FILTER (WHERE created_at >= $4), 0)::float8
		FROM request_logs WHERE client_id = ANY($1)
		GROUP BY client_id`, ids, dayStart, weekStart, monthStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]clientSpend{}
	for rows.Next() {
		var id int64
		var sp clientSpend
		if err := rows.Scan(&id, &sp.Day, &sp.Week, &sp.Month); err != nil {
			return nil, err
		}
		out[id] = sp
	}
	return out, rows.Err()
}

// ClientPeriodSpend 查询单个调用方的本周期消费（网关鉴权时使用）。
func (s *Store) ClientPeriodSpend(ctx context.Context, id int64, dayStart, weekStart, monthStart time.Time) (day, week, month float64, err error) {
	m, err := s.clientPeriodSpend(ctx, []int64{id}, dayStart, weekStart, monthStart)
	if err != nil {
		return 0, 0, 0, err
	}
	sp := m[id]
	return sp.Day, sp.Week, sp.Month, nil
}

type ClientPatch struct {
	Name          *string
	Description   *string
	WalletEnabled *bool
	Enabled       *bool
	// 三个限额总是覆盖：nil 时写入 SQL NULL（= 不限）
	LimitDaily   *float64
	LimitWeekly  *float64
	LimitMonthly *float64
}

// UpdateClient 更新调用方配置；nil 的可选字段保持原值；三个限额总是覆盖（nil = 清除限额）。
func (s *Store) UpdateClient(ctx context.Context, id int64, in ClientPatch) (*APIClient, error) {
	set := []string{"updated_at = now()"}
	args := []any{id}
	add := func(expr string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf(expr, len(args)))
	}
	if in.Name != nil {
		add("name = $%d", *in.Name)
	}
	if in.Description != nil {
		add("description = $%d", *in.Description)
	}
	if in.WalletEnabled != nil {
		add("wallet_enabled = $%d", *in.WalletEnabled)
	}
	if in.Enabled != nil {
		add("enabled = $%d", *in.Enabled)
	}
	add("limit_daily = $%d", in.LimitDaily)
	add("limit_weekly = $%d", in.LimitWeekly)
	add("limit_monthly = $%d", in.LimitMonthly)
	q := fmt.Sprintf(`UPDATE api_clients SET %s WHERE id = $1 RETURNING %s`, strings.Join(set, ", "), clientCols)
	row := s.Pool.QueryRow(ctx, q, args...)
	c, err := scanClient(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ChargeClient 扣费：余额充足时扣款并累计消费；返回 false 表示余额不足未扣除（并发竞态兜底）。
func (s *Store) ChargeClient(ctx context.Context, id int64, cost float64) (bool, error) {
	if cost <= 0 {
		return true, nil
	}
	ct, err := s.Pool.Exec(ctx, `
		UPDATE api_clients SET
			wallet_balance = wallet_balance - $2,
			total_spent = total_spent + $2,
			updated_at = now()
		WHERE id = $1 AND (NOT wallet_enabled OR wallet_balance >= $2)`, id, cost)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() > 0, nil
}

// RechargeClient 充值（amount 可为负数，表示手动扣减调整）。
func (s *Store) RechargeClient(ctx context.Context, id int64, amount float64) (*APIClient, error) {
	row := s.Pool.QueryRow(ctx, `
		UPDATE api_clients SET wallet_balance = wallet_balance + $2, updated_at = now()
		WHERE id = $1 RETURNING `+clientCols, id, amount)
	c, err := scanClient(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) RotateClientKey(ctx context.Context, id int64) (*APIClient, error) {
	row := s.Pool.QueryRow(ctx, `
		UPDATE api_clients SET api_key = $2, updated_at = now() WHERE id = $1 RETURNING `+clientCols,
		id, GenerateClientKey())
	c, err := scanClient(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) DeleteClient(ctx context.Context, id int64) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM api_clients WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
