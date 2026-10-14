-- flash-llm-router schema
-- 所有时间统一 UTC (TIMESTAMPTZ)

CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    username      TEXT        NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL DEFAULT 'viewer'
                              CHECK (role IN ('admin', 'operator', 'viewer')),
    enabled       BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS sessions (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS llm_apis (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT        NOT NULL,
    api_key         TEXT        NOT NULL UNIQUE,
    description     TEXT        NOT NULL DEFAULT '',
    models          JSONB       NOT NULL DEFAULT '[]'::jsonb, -- 手动维护的全局模型清单 ["deepseek-chat", ...]
    supports_vision BOOLEAN     NOT NULL DEFAULT TRUE,
    supports_video  BOOLEAN     NOT NULL DEFAULT FALSE,
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS providers (
    id                     BIGSERIAL PRIMARY KEY,
    llm_api_id             BIGINT           NOT NULL REFERENCES llm_apis(id) ON DELETE CASCADE,
    name                   TEXT             NOT NULL,
    kind                   TEXT             NOT NULL DEFAULT 'openai', -- openai | deepseek | custom
    base_url               TEXT             NOT NULL,
    api_key                TEXT             NOT NULL,
    priority               INT              NOT NULL DEFAULT 100,      -- 数字越小越优先
    enabled                BOOLEAN          NOT NULL DEFAULT TRUE,
    supports_responses     BOOLEAN          NOT NULL DEFAULT FALSE,    -- 是否原生支持 Responses API
    inject_stream_usage    BOOLEAN          NOT NULL DEFAULT TRUE,     -- 流式 chat 请求注入 stream_options.include_usage
    -- 熔断 / 限流配置
    max_body_mb            DOUBLE PRECISION NOT NULL DEFAULT 0,        -- 请求体超过此大小跳过本 provider，0=不限
    max_failures           INT              NOT NULL DEFAULT 0,        -- 连续失败 N 次暂停，0=不熔断
    cooldown_seconds       INT              NOT NULL DEFAULT 60,       -- 暂停时长（秒）
    timeout_seconds        INT              NOT NULL DEFAULT 300,      -- 非流式=总超时；流式=连接+首字节超时
    -- 计费（CNY / 每 1M tokens）
    price_input            NUMERIC(14, 6)   NOT NULL DEFAULT 0,
    price_output           NUMERIC(14, 6)   NOT NULL DEFAULT 0,
    price_cached_input     NUMERIC(14, 6)   NOT NULL DEFAULT 0,
    model_prices           JSONB            NOT NULL DEFAULT '{}'::jsonb, -- {"model":{"input":..,"output":..,"cached_input":..}}
    -- 运行时健康状态（持久化，重启保留）
    consecutive_failures   INT              NOT NULL DEFAULT 0,
    cooldown_until         TIMESTAMPTZ,
    last_error             TEXT             NOT NULL DEFAULT '',
    last_error_at          TIMESTAMPTZ,
    -- DeepSeek 余额缓存
    last_balance           JSONB,
    balance_checked_at     TIMESTAMPTZ,
    created_at             TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_providers_api ON providers (llm_api_id, priority, id);

CREATE TABLE IF NOT EXISTS request_logs (
    id             BIGSERIAL PRIMARY KEY,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    llm_api_id     BIGINT,
    llm_api_name   TEXT         NOT NULL DEFAULT '',
    provider_id    BIGINT,
    provider_name  TEXT         NOT NULL DEFAULT '',
    endpoint       TEXT         NOT NULL DEFAULT '',   -- chat.completions | completions | responses | models
    model          TEXT         NOT NULL DEFAULT '',
    stream         BOOLEAN      NOT NULL DEFAULT FALSE,
    status_code    INT          NOT NULL DEFAULT 0,
    success        BOOLEAN      NOT NULL DEFAULT FALSE,
    error          TEXT         NOT NULL DEFAULT '',
    request_bytes  BIGINT       NOT NULL DEFAULT 0,
    latency_ms     BIGINT       NOT NULL DEFAULT 0,
    prompt_tokens  BIGINT       NOT NULL DEFAULT 0,
    cached_tokens  BIGINT       NOT NULL DEFAULT 0,
    completion_tokens BIGINT    NOT NULL DEFAULT 0,
    total_tokens   BIGINT       NOT NULL DEFAULT 0,
    cost           NUMERIC(16, 6) NOT NULL DEFAULT 0,
    priced         BOOLEAN      NOT NULL DEFAULT TRUE, -- false = upsteam 未返回 usage，费用无法计算
    attempts       INT          NOT NULL DEFAULT 1,
    attempts_detail JSONB       NOT NULL DEFAULT '[]'::jsonb, -- [{provider,status,error,latency_ms}]
    client_ip      TEXT         NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_logs_created ON request_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_api_created ON request_logs (llm_api_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_provider_created ON request_logs (provider_id, created_at DESC);

-- ---------------------------------------------------------------- 调用方 / 钱包
-- 每个调用方持有一个独立 API Key，绑定到某个 LLM API；可设置钱包余额与日/周/月消费限额。
CREATE TABLE IF NOT EXISTS api_clients (
    id             BIGSERIAL PRIMARY KEY,
    llm_api_id     BIGINT           NOT NULL REFERENCES llm_apis(id) ON DELETE CASCADE,
    name           TEXT             NOT NULL,
    api_key        TEXT             NOT NULL UNIQUE,
    description    TEXT             NOT NULL DEFAULT '',
    -- 钱包
    wallet_balance NUMERIC(14, 6)   NOT NULL DEFAULT 0,   -- CNY 余额
    wallet_enabled BOOLEAN          NOT NULL DEFAULT TRUE,  -- 余额耗尽后是否拒绝对外请求
    -- 消费限额（CNY / 周期；NULL = 不限）
    limit_daily    NUMERIC(14, 6),
    limit_weekly   NUMERIC(14, 6),
    limit_monthly  NUMERIC(14, 6),
    total_spent    NUMERIC(14, 6)   NOT NULL DEFAULT 0,   -- 累计消费
    enabled        BOOLEAN          NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ      NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_clients_api ON api_clients (llm_api_id, id);

-- 日志归属调用方（用于周期额度统计）
ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS client_id BIGINT;
ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS client_name TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_logs_client_created ON request_logs (client_id, created_at DESC);
