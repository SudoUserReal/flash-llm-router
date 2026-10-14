package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Store) InsertLog(ctx context.Context, in LogInput) error {
	detail, _ := json.Marshal(in.AttemptsDetail)
	if detail == nil {
		detail = []byte("[]")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO request_logs (llm_api_id, llm_api_name, provider_id, provider_name, endpoint, model,
			stream, status_code, success, error, request_bytes, latency_ms, prompt_tokens, cached_tokens,
			completion_tokens, total_tokens, cost, priced, attempts, attempts_detail, client_ip,
			client_id, client_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
		in.LLMAPIID, in.LLMAPIName, in.ProviderID, in.ProviderName, in.Endpoint, in.Model,
		in.Stream, in.StatusCode, in.Success, in.Error, in.RequestBytes, in.LatencyMS,
		in.PromptTokens, in.CachedTokens, in.CompletionTokens, in.TotalTokens, in.Cost,
		in.Priced, in.Attempts, detail, in.ClientIP, in.ClientID, in.ClientName)
	return err
}

func buildLogWhere(f LogFilter) (string, []any) {
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
	if f.ClientID != nil {
		add("client_id = $%d", *f.ClientID)
	}
	if f.From != nil {
		add("created_at >= $%d", *f.From)
	}
	if f.To != nil {
		add("created_at < $%d", *f.To)
	}
	if f.Success != nil {
		add("success = $%d", *f.Success)
	}
	if f.Model != "" {
		add("model ILIKE '%%' || $%d || '%%'", f.Model)
	}
	where := ""
	for i, c := range conds {
		if i == 0 {
			where = c
		} else {
			where += " AND " + c
		}
	}
	return where, args
}

func (s *Store) ListLogs(ctx context.Context, f LogFilter) ([]RequestLog, int64, error) {
	where, args := buildLogWhere(f)

	var total int64
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM request_logs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	page, pageSize := f.Page, f.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	args = append(args, pageSize, (page-1)*pageSize)
	q := fmt.Sprintf(`
		SELECT id, created_at, llm_api_id, llm_api_name, provider_id, provider_name, endpoint, model,
			stream, status_code, success, error, request_bytes, latency_ms, prompt_tokens, cached_tokens,
			completion_tokens, total_tokens, cost::float8, priced, attempts, attempts_detail, client_ip,
			client_id, client_name
		FROM request_logs WHERE %s
		ORDER BY id DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []RequestLog{}
	for rows.Next() {
		var l RequestLog
		var detail []byte
		if err := rows.Scan(&l.ID, &l.CreatedAt, &l.LLMAPIID, &l.LLMAPIName, &l.ProviderID, &l.ProviderName,
			&l.Endpoint, &l.Model, &l.Stream, &l.StatusCode, &l.Success, &l.Error, &l.RequestBytes,
			&l.LatencyMS, &l.PromptTokens, &l.CachedTokens, &l.CompletionTokens, &l.TotalTokens,
			&l.Cost, &l.Priced, &l.Attempts, &detail, &l.ClientIP, &l.ClientID, &l.ClientName); err != nil {
			return nil, 0, err
		}
		_ = json.Unmarshal(detail, &l.AttemptsDetail)
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// DeleteLogsBefore 删除指定时间之前的日志（运维清理用）。
func (s *Store) DeleteLogsBefore(ctx context.Context, before time.Time) (int64, error) {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM request_logs WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
