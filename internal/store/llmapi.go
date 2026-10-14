package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// GenerateAPIKey 生成对外 API key，形如 sk-flr-<40 hex>。
func GenerateAPIKey() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "sk-flr-" + hex.EncodeToString(b)
}

const llmAPICols = `id, name, api_key, description, models, supports_vision, supports_video, enabled, created_at, updated_at`

func scanLLMAPI(row pgx.Row) (*LLMAPI, error) {
	a := &LLMAPI{}
	var models []byte
	if err := row.Scan(&a.ID, &a.Name, &a.APIKey, &a.Description, &models,
		&a.SupportsVision, &a.SupportsVideo, &a.Enabled, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(models, &a.Models)
	if a.Models == nil {
		a.Models = []string{}
	}
	return a, nil
}

type LLMAPIInput struct {
	Name           string
	Description    string
	Models         []string
	SupportsVision bool
	SupportsVideo  bool
	Enabled        bool
}

// CreateLLMAPI 创建服务；apiKey 为空时自动生成。
func (s *Store) CreateLLMAPI(ctx context.Context, in LLMAPIInput, apiKey string) (*LLMAPI, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		apiKey = GenerateAPIKey()
	}
	models, _ := json.Marshal(in.Models)
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO llm_apis (name, api_key, description, models, supports_vision, supports_video, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+llmAPICols,
		in.Name, apiKey, in.Description, models, in.SupportsVision, in.SupportsVideo, in.Enabled)
	a, err := scanLLMAPI(row)
	if err != nil {
		return nil, fmt.Errorf("create llm api: %w", err)
	}
	return a, nil
}

func (s *Store) ListLLMAPIs(ctx context.Context) ([]LLMAPI, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+llmAPICols+`,
		       (SELECT count(*) FROM providers p WHERE p.llm_api_id = llm_apis.id) AS provider_count
		FROM llm_apis ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LLMAPI{}
	for rows.Next() {
		a := LLMAPI{}
		var models []byte
		if err := rows.Scan(&a.ID, &a.Name, &a.APIKey, &a.Description, &models,
			&a.SupportsVision, &a.SupportsVideo, &a.Enabled, &a.CreatedAt, &a.UpdatedAt,
			&a.ProviderCount); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(models, &a.Models)
		if a.Models == nil {
			a.Models = []string{}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetLLMAPI(ctx context.Context, id int64) (*LLMAPI, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+llmAPICols+` FROM llm_apis WHERE id = $1`, id)
	a, err := scanLLMAPI(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) GetLLMAPIByKey(ctx context.Context, apiKey string) (*LLMAPI, error) {
	row := s.Pool.QueryRow(ctx, `SELECT `+llmAPICols+` FROM llm_apis WHERE api_key = $1 AND enabled`, apiKey)
	a, err := scanLLMAPI(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) UpdateLLMAPI(ctx context.Context, id int64, in LLMAPIInput) (*LLMAPI, error) {
	models, _ := json.Marshal(in.Models)
	row := s.Pool.QueryRow(ctx, `
		UPDATE llm_apis SET
			name = $2, description = $3, models = $4,
			supports_vision = $5, supports_video = $6, enabled = $7, updated_at = now()
		WHERE id = $1
		RETURNING `+llmAPICols,
		id, in.Name, in.Description, models, in.SupportsVision, in.SupportsVideo, in.Enabled)
	a, err := scanLLMAPI(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) DeleteLLMAPI(ctx context.Context, id int64) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM llm_apis WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RotateLLMAPIKey(ctx context.Context, id int64) (*LLMAPI, error) {
	row := s.Pool.QueryRow(ctx, `
		UPDATE llm_apis SET api_key = $2, updated_at = now() WHERE id = $1
		RETURNING `+llmAPICols, id, GenerateAPIKey())
	a, err := scanLLMAPI(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}
