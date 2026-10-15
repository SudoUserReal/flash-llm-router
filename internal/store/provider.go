package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const providerCols = `id, llm_api_id, name, kind, base_url, api_key, priority, enabled, supports_responses,
	inject_stream_usage, image_to_url, max_body_mb, max_failures, cooldown_seconds, timeout_seconds,
	price_input::float8, price_output::float8, price_cached_input::float8, model_prices,
	consecutive_failures, cooldown_until, last_error, last_error_at, last_balance, balance_checked_at,
	created_at, updated_at`

func scanProvider(row pgx.Row) (*Provider, error) {
	p := &Provider{}
	var modelPrices []byte
	var balance []byte
	if err := row.Scan(&p.ID, &p.LLMAPIID, &p.Name, &p.Kind, &p.BaseURL, &p.APIKey, &p.Priority,
		&p.Enabled, &p.SupportsResponses, &p.InjectStreamUsage, &p.ImageToURL, &p.MaxBodyMB, &p.MaxFailures,
		&p.CooldownSeconds, &p.TimeoutSeconds, &p.PriceInput, &p.PriceOutput, &p.PriceCachedInput,
		&modelPrices, &p.ConsecutiveFailures, &p.CooldownUntil, &p.LastError, &p.LastErrorAt,
		&balance, &p.BalanceCheckedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(modelPrices, &p.ModelPrices)
	if p.ModelPrices == nil {
		p.ModelPrices = map[string]ModelPrice{}
	}
	if len(balance) > 0 {
		p.LastBalance = json.RawMessage(balance)
	}
	return p, nil
}

type ProviderInput struct {
	Name              string
	Kind              string
	BaseURL           string
	APIKey            string
	Priority          int
	Enabled           bool
	SupportsResponses bool
	InjectStreamUsage bool
	ImageToURL        bool
	MaxBodyMB         float64
	MaxFailures       int
	CooldownSeconds   int
	TimeoutSeconds    int
	PriceInput        float64
	PriceOutput       float64
	PriceCachedInput  float64
	ModelPrices       map[string]ModelPrice
}

func (s *Store) CreateProvider(ctx context.Context, llmAPIID int64, in ProviderInput) (*Provider, error) {
	mp, _ := json.Marshal(in.ModelPrices)
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO providers (llm_api_id, name, kind, base_url, api_key, priority, enabled,
			supports_responses, inject_stream_usage, image_to_url, max_body_mb, max_failures, cooldown_seconds,
			timeout_seconds, price_input, price_output, price_cached_input, model_prices)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+providerCols,
		llmAPIID, in.Name, in.Kind, in.BaseURL, in.APIKey, in.Priority, in.Enabled,
		in.SupportsResponses, in.InjectStreamUsage, in.ImageToURL, in.MaxBodyMB, in.MaxFailures, in.CooldownSeconds,
		in.TimeoutSeconds, in.PriceInput, in.PriceOutput, in.PriceCachedInput, mp)
	p, err := scanProvider(row)
	if err != nil {
		return nil, fmt.Errorf("create provider: %w", err)
	}
	return p, nil
}

func (s *Store) GetProvider(ctx context.Context, id int64) (*Provider, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+providerCols+` FROM providers WHERE id = $1`, id)
	p, err := scanProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// ListProviders 返回某 LLM API 下全部 Provider，按优先级排序。
func (s *Store) ListProviders(ctx context.Context, llmAPIID int64) ([]Provider, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+providerCols+` FROM providers WHERE llm_api_id = $1 ORDER BY priority, id`, llmAPIID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateProvider(ctx context.Context, id int64, in ProviderInput) (*Provider, error) {
	mp, _ := json.Marshal(in.ModelPrices)
	row := s.Pool.QueryRow(ctx, `
		UPDATE providers SET
			name = $2, kind = $3, base_url = $4, api_key = $5, priority = $6, enabled = $7,
			supports_responses = $8, inject_stream_usage = $9, image_to_url = $10, max_body_mb = $11,
			max_failures = $12, cooldown_seconds = $13, timeout_seconds = $14, price_input = $15,
			price_output = $16, price_cached_input = $17, model_prices = $18, updated_at = now()
		WHERE id = $1
		RETURNING `+providerCols,
		id, in.Name, in.Kind, in.BaseURL, in.APIKey, in.Priority, in.Enabled,
		in.SupportsResponses, in.InjectStreamUsage, in.ImageToURL, in.MaxBodyMB, in.MaxFailures, in.CooldownSeconds,
		in.TimeoutSeconds, in.PriceInput, in.PriceOutput, in.PriceCachedInput, mp)
	p, err := scanProvider(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *Store) DeleteProvider(ctx context.Context, id int64) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM providers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderProviders 按给定顺序重写优先级（步长 10，保留手工插入空间）。
func (s *Store) ReorderProviders(ctx context.Context, llmAPIID int64, ids []int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, id := range ids {
		ct, err := tx.Exec(ctx, `
			UPDATE providers SET priority = $3, updated_at = now()
			WHERE id = $1 AND llm_api_id = $2`, id, llmAPIID, (i+1)*10)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("provider %d not found in llm api %d", id, llmAPIID)
		}
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------- health

// RecordProviderFailure 记录一次失败：连续失败 +1；达到 maxFailures 则进入冷却。
func (s *Store) RecordProviderFailure(ctx context.Context, id int64, errMsg string) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE providers SET
			consecutive_failures = consecutive_failures + 1,
			last_error = $2,
			last_error_at = now(),
			cooldown_until = CASE
				WHEN max_failures > 0 AND consecutive_failures + 1 >= max_failures
				THEN now() + make_interval(secs => GREATEST(cooldown_seconds, 1))
				ELSE cooldown_until END,
			updated_at = now()
		WHERE id = $1`, id, errMsg)
	return err
}

// RecordProviderSuccess 成功：连续失败清零、解除冷却。
func (s *Store) RecordProviderSuccess(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE providers SET consecutive_failures = 0, cooldown_until = NULL, updated_at = now()
		WHERE id = $1 AND (consecutive_failures <> 0 OR cooldown_until IS NOT NULL)`, id)
	return err
}

// ResetProviderHealth 手动立即恢复。
func (s *Store) ResetProviderHealth(ctx context.Context, id int64) error {
	ct, err := s.Pool.Exec(ctx, `
		UPDATE providers SET consecutive_failures = 0, cooldown_until = NULL,
			last_error = '', last_error_at = NULL, updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SaveProviderBalance(ctx context.Context, id int64, balance json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE providers SET last_balance = $2, balance_checked_at = now() WHERE id = $1`,
		id, balance)
	return err
}

// Now 便于测试注入时间。目前直接使用数据库时间，仅作为占位保留。
func Now() time.Time { return time.Now().UTC() }
