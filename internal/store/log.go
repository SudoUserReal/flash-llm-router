package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// InsertLog 直接写入一条完整日志（用于无需"进行中"状态的即时场景）。
func (s *Store) InsertLog(ctx context.Context, in LogInput) error {
	detail, _ := json.Marshal(in.AttemptsDetail)
	if detail == nil {
		detail = []byte("[]")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO request_logs (llm_api_id, llm_api_name, provider_id, provider_name, endpoint, model,
			stream, status_code, success, error, request_bytes, latency_ms, prompt_tokens, cached_tokens,
			completion_tokens, total_tokens, cost, priced, attempts, attempts_detail, client_ip,
			client_id, client_name, in_progress)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		in.LLMAPIID, in.LLMAPIName, in.ProviderID, in.ProviderName, in.Endpoint, in.Model,
		in.Stream, in.StatusCode, in.Success, in.Error, in.RequestBytes, in.LatencyMS,
		in.PromptTokens, in.CachedTokens, in.CompletionTokens, in.TotalTokens, in.Cost,
		in.Priced, in.Attempts, detail, in.ClientIP, in.ClientID, in.ClientName, in.InProgress)
	return err
}

// BeginLog 请求进入时先落一条"进行中"日志（in_progress=true），返回日志 ID。
// 请求结束后由 FinishLog 更新为终态，使长耗时请求（如流式）在日志中可见。
func (s *Store) BeginLog(ctx context.Context, in LogInput) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO request_logs (llm_api_id, llm_api_name, endpoint, model, stream,
			request_bytes, client_ip, client_id, client_name, in_progress, success, priced)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,true,false,false)
		RETURNING id`,
		in.LLMAPIID, in.LLMAPIName, in.Endpoint, in.Model, in.Stream,
		in.RequestBytes, in.ClientIP, in.ClientID, in.ClientName).Scan(&id)
	return id, err
}

// FinishLog 将进行中的日志更新为终态。
func (s *Store) FinishLog(ctx context.Context, id int64, f LogFinish) error {
	detail, _ := json.Marshal(f.AttemptsDetail)
	if detail == nil {
		detail = []byte("[]")
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE request_logs SET
			provider_id = $2, provider_name = $3, status_code = $4, success = $5, error = $6,
			latency_ms = $7, prompt_tokens = $8, cached_tokens = $9, completion_tokens = $10,
			total_tokens = $11, cost = $12, priced = $13, attempts = $14, attempts_detail = $15,
			in_progress = false
		WHERE id = $1`,
		id, f.ProviderID, f.ProviderName, f.StatusCode, f.Success, f.Error,
		f.LatencyMS, f.PromptTokens, f.CachedTokens, f.CompletionTokens,
		f.TotalTokens, f.Cost, f.Priced, f.Attempts, detail)
	return err
}

// MarkStaleInProgress 把超时仍处于"进行中"的日志标记为中断（进程异常退出等场景）。
func (s *Store) MarkStaleInProgress(ctx context.Context, before time.Time) (int64, error) {
	ct, err := s.Pool.Exec(ctx, `
		UPDATE request_logs SET
			in_progress = false, success = false, status_code = 499,
			error = CASE WHEN error = '' THEN '请求中断（服务重启或超时未完成）' ELSE error END
		WHERE in_progress AND created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
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
	if f.ExcludeInProgress {
		conds = append(conds, "in_progress = false")
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
			client_id, client_name, in_progress
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
			&l.Cost, &l.Priced, &l.Attempts, &detail, &l.ClientIP, &l.ClientID, &l.ClientName,
			&l.InProgress); err != nil {
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
