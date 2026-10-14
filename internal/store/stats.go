package store

import (
	"context"
	"fmt"
	"time"
)

type RangeFilter struct {
	LLMAPIID   *int64
	ProviderID *int64
	From       *time.Time
	To         *time.Time
}

func buildRangeWhere(f RangeFilter) (string, []any) {
	conds := []string{"1=1"}
	args := []any{}
	add := func(expr string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(expr, len(args)))
	}
	if f.LLMAPIID != nil {
		add("llm_api_id = $%d", *f.LLMAPIID)
	}
	if f.ProviderID != nil {
		add("provider_id = $%d", *f.ProviderID)
	}
	if f.From != nil {
		add("created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("created_at < $%d", *f.To)
	}
	out := conds[0]
	for _, c := range conds[1:] {
		out += " AND " + c
	}
	return out, args
}

func (s *Store) Totals(ctx context.Context, f RangeFilter) (*Totals, error) {
	where, args := buildRangeWhere(f)
	t := &Totals{}
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE success),
			COALESCE(sum(prompt_tokens), 0), COALESCE(sum(cached_tokens), 0),
			COALESCE(sum(completion_tokens), 0), COALESCE(sum(total_tokens), 0),
			COALESCE(sum(cost), 0)::float8
		FROM request_logs WHERE `+where, args...,
	).Scan(&t.Requests, &t.Success, &t.PromptTokens, &t.CachedTokens, &t.CompletionTokens, &t.TotalTokens, &t.Cost)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// Timeseries 按 hour/day 粒度聚合 bucket 边界（基于指定时区的自然小时/天）。
func (s *Store) Timeseries(ctx context.Context, f RangeFilter, granularity, tz string) ([]UsagePoint, error) {
	if granularity != "hour" && granularity != "day" {
		granularity = "day"
	}
	where, args := buildRangeWhere(f)
	n := len(args)
	q := fmt.Sprintf(`
		SELECT date_trunc($%d, created_at AT TIME ZONE $%d) AT TIME ZONE $%d AS bucket,
			count(*), count(*) FILTER (WHERE success),
			COALESCE(sum(prompt_tokens), 0), COALESCE(sum(cached_tokens), 0),
			COALESCE(sum(completion_tokens), 0), COALESCE(sum(total_tokens), 0),
			COALESCE(sum(cost), 0)::float8
		FROM request_logs WHERE %s
		GROUP BY bucket ORDER BY bucket`, n+1, n+2, n+2, where)
	args = append(args, granularity, tz)

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsagePoint{}
	for rows.Next() {
		var p UsagePoint
		if err := rows.Scan(&p.Bucket, &p.Requests, &p.Success, &p.PromptTokens, &p.CachedTokens,
			&p.CompletionTokens, &p.TotalTokens, &p.Cost); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProviderUsages 返回区间内各 Provider 的花费/token 明细。
func (s *Store) ProviderUsages(ctx context.Context, f RangeFilter) ([]ProviderUsage, error) {
	where, args := buildRangeWhere(f)
	rows, err := s.Pool.Query(ctx, `
		SELECT COALESCE(provider_id, 0), provider_name, count(*), count(*) FILTER (WHERE success),
			COALESCE(sum(prompt_tokens), 0), COALESCE(sum(cached_tokens), 0),
			COALESCE(sum(completion_tokens), 0), COALESCE(sum(total_tokens), 0),
			COALESCE(sum(cost), 0)::float8, COALESCE(avg(latency_ms), 0)::float8
		FROM request_logs WHERE `+where+`
		GROUP BY provider_id, provider_name
		ORDER BY COALESCE(sum(cost), 0) DESC, count(*) DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderUsage{}
	for rows.Next() {
		var u ProviderUsage
		if err := rows.Scan(&u.ProviderID, &u.ProviderName, &u.Requests, &u.Success,
			&u.PromptTokens, &u.CachedTokens, &u.CompletionTokens, &u.TotalTokens,
			&u.Cost, &u.AvgLatencyMS); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
