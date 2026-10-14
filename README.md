# Flash-LLM-Router

LLM API 中转与计费网关。对外暴露 OpenAI 兼容接口（Chat Completions / Completions / Responses / Models），
对内把一次请求分发到多个上游 Provider，按优先级自动降级、熔断保护，并按 token 用量精确计费。
前端管理界面使用 **Svelte 5 + TailwindCSS v4**（无 SvelteKit），后端 **Go**，数据（配置 + 日志）全部存 **PostgreSQL**。

<p align="center">
  <img src="docs/screenshots/dashboard.png" alt="总览" width="100%">
</p>
<p align="center">
  <img src="docs/screenshots/api-detail.png" alt="LLM API 详情：Provider 与调用方钱包" width="49.5%">
  <img src="docs/screenshots/stats.png" alt="统计分析" width="49.5%">
</p>

## 功能总览

- **对外服务实体（LLM API）**：每个服务独立命名、独立 API Key，可手动维护对外模型清单，声明是否支持图片/视频
- **多 Provider 聚合**：一个 LLM API 下可挂任意多个上游，按优先级（数字越小越优先）依次尝试
- **优先级调整**：管理界面支持拖拽排序，或直接输入数字
- **自动故障切换**：任一 Provider 失败自动切到下一个，直到成功或全部不可用
- **熔断保护**：可配置「连续失败 N 次后暂停 X 秒」；冷却到期自动恢复；也可一键立即恢复
- **超时控制**：非流式=整请求总超时；流式=连接+首字节超时，长回答不被掐断
- **请求体过滤**：请求体超过 Provider 配置的大小时，本次请求跳过该 Provider（不消费它）
- **Token 计费**：按 Provider 单价（支持模型级覆盖、缓存命中价）对每次请求计费，币种 CNY/百万 tokens
- **用量统计**：按小时/天聚合，可过滤时间范围；下钻查看区间内每个 Provider 的花费与 token
- **请求日志**：记录时间/模型/tokens/费用/状态/耗时/切换尝试链（仅元数据，不存请求正文）
- **DeepSeek 增强**：一键查询账户余额（官方接口）、一键填入官方价目表（可选高峰/空闲时段价）
- **调用方与钱包**：每个使用方持有独立 API Key，可设置钱包余额（耗尽即拒绝）与日/周/月消费限额
- **多用户与权限**：admin / operator / viewer 三级角色，登录带图片验证码
- **交付形态**：单二进制（前端 go:embed）+ Dockerfile + docker-compose（内含 PostgreSQL）

## 界面与设计系统

管理界面遵循 **ClickUp 设计系统**（规范来源：<https://designmd-store.com/packs/clickup>）：
ink-black 主 CTA、紫色强调（`#7612FA`）、白卡细边大圆角，标题用 Plus Jakarta Sans、正文用 Inter、标签用 Sometype Mono；
全部设计 token 定义在 `web/src/app.css` 的 `@theme` 中，组件约定（`.btn-*` / `.badge-*` / `.chip` / `.card`）在 `AGENTS.md` 中有完整说明。

品牌图标为**原创设计**（`web/src/assets/icon.svg`，由 Codex 生成：路由节点 + 双通道分流，紫→蓝渐变），favicon 位于 `web/public/favicon.svg`。

> 修改任何 UI 之前请先阅读 [AGENTS.md](AGENTS.md)。

## 快速开始

### Docker Compose（推荐）

```bash
cp .env.example .env   # 按需修改数据库密码、管理员密码
docker compose up --build -d
# 打开 http://localhost:8080
```

首次启动会自动创建管理员。若 `.env` 中 `ADMIN_PASSWORD` 留空，随机密码会打印在容器日志里：

```bash
docker compose logs app | grep 管理员
```

### 本地运行

```bash
# 需要可用的 PostgreSQL
export DATABASE_URL="postgres://user:pass@localhost:5432/flash_llm_router"
export ADMIN_PASSWORD="your-password-here"

make build   # 构建前端并编译单二进制（前端产物经 go:embed 打入）
./flr
```

开发模式（前后端分离热更新）：

```bash
make dev-api   # 或直接 ./flr，后端在 :8080
make dev-web   # Vite 在 :5173，自动代理 /api 与 /v1 到 8080
```

### 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `DATABASE_URL` | `postgres://localhost:5432/flash_llm_router` | PostgreSQL DSN |
| `ADDR` | `:8080` | 监听地址 |
| `ADMIN_USERNAME` | `admin` | 首次启动创建的管理员用户名 |
| `ADMIN_PASSWORD` | （空） | 管理员密码；留空则随机生成并打印到日志 |
| `SESSION_TTL_HOURS` | `168` | 登录会话有效期（小时） |
| `STATS_TZ` | `Asia/Shanghai` | 统计聚合时区（存储恒为 UTC） |
| `LOG_LEVEL` | `info` | 日志级别 |

## 使用指南

### 1. 创建 LLM API（对外服务）

「LLM API」页 → 新建：填写名称、描述、模型清单（每行一个，用于对外 `/v1/models` 展示）、图片/视频能力声明。
创建后自动生成对外 API Key（`sk-flr-…`，可随时重置/复制）。

### 2. 配置 Provider（上游通道）

进入服务详情页 → 添加 Provider：

| 配置项 | 说明 |
|---|---|
| 类型 | OpenAI 兼容 / DeepSeek（余额+填价）/ 自定义 |
| Base URL | 不带 `/v1` 亦可（自动拼接），如 `https://api.deepseek.com` |
| API Key | 上游密钥 |
| 优先级 | 数字越小越优先；支持拖拽排序或直接输入 |
| 超时（秒） | 非流式=总超时；流式=连接+首字节超时 |
| 请求体上限（MB） | 超过则本次请求跳过该 Provider，0=不限 |
| 连续失败熔断（次） | 连续失败达到 N 次进入冷却；成功一次清零；0=不熔断 |
| 冷却时长（秒） | 熔断后暂停时长，到期自动恢复 |
| 价格 | CNY/百万 tokens：输入（缓存未命中）/ 输出 / 缓存命中，可按模型覆盖 |
| 原生 Responses | 勾选后才会接收 `/v1/responses` 流量（仅直通） |
| 流式计费注入 | 流式请求自动注入 `stream_options.include_usage` 以获取 usage |

DeepSeek 类型额外提供：**余额查询**（调用官方 `/user/balance`，结果缓存展示）与**自动配置价格**（内置官方价目表，可选高峰/空闲时段价一键填入）。

### 3. 对外 API

对外接口与 OpenAI 兼容，使用 LLM API 的 Key 认证（`Authorization: Bearer` 或 `x-api-key`）：

```bash
# Chat Completions（含流式）
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-flr-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-flash","messages":[{"role":"user","content":"你好"}]}'

# Responses API（仅转发给声明了「原生 Responses」的 Provider）
curl http://localhost:8080/v1/responses \
  -H "Authorization: Bearer sk-flr-xxx" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-flash","input":"你好"}'

# 兼容端点
POST /v1/completions        # Legacy Completions
GET  /v1/models             # 模型列表（返回手动维护的清单）
GET  /v1/models/{model}
```

### 4. 调用方 / 钱包与限额

「LLM API 详情」页底部的**调用方 / 钱包**区块，为每个使用方发放独立 API Key：

- **钱包余额**：调用方拥有独立余额（CNY），余额 ≤ 0 时拒绝其请求（`429 insufficient_balance`）；可随时充值或扣减
- **消费限额**：可分别设置每日 / 每周 / 每月消费上限（留空 = 不限），按统计时区（`STATS_TZ`）的自然日、周一至周日、自然月计算；超额返回 `429 daily_limit_exceeded` 等，下个周期自动恢复
- **扣费**：每次成功调用按实际费用实时从钱包扣除，日志归属到调用方；扣费使用条件更新防止并发超支
- 服务主 Key（页面顶部「对外凭证」）不受钱包与限额约束，供管理/自测使用；创建 LLM API 时可自定义 API Key（留空自动生成）

### 5. 统计与日志

- 「统计分析」：选择 LLM API、时间范围（含自定义）、粒度（小时/天），查看请求数/费用/tokens 趋势图，
  以及**区间内每个 Provider 的花费、token、成功率、平均延迟明细**
- 「请求日志」：按服务、结果、模型、时间过滤；展开可查看该次请求的 Provider 尝试链（哪个通道失败、为何失败、最终落在哪）

## 行为语义（设计说明）

以下行为为明确的设计决策，便于排查与对账：

1. **失败判定**：任何非 2xx 响应、网络错误、超时都算 Provider 失败，触发切换并计入熔断计数（含 4xx）。
2. **选路顺序**：按 `优先级升序 → id 升序` 依次尝试；被禁用、冷却中、超出请求体上限、不支持 Responses 的 Provider 直接跳过。
3. **熔断**：连续失败达到阈值进入冷却（计数持久化在数据库，重启保留）。任意一次成功计数清零；冷却到期自动恢复；修改 Base URL 或 Key 会自动清除熔断状态。
4. **切换边界**：只发生在单次请求内部，且仅当响应尚未开始 = 非流式请求直到读完响应体前都可切换；流式请求一旦收到 2xx 响应头即开始转发，之后无法切换（如实记录中断）。
5. **超时**：非流式用总超时覆盖「请求+读取响应体」；流式只限制连接与首字节（通过 Transport 的 `ResponseHeaderTimeout`），不限总时长。
6. **流式计费**：若客户端未显式使用 `stream_options.include_usage`，网关会注入它以获得 usage 用于计费，并把产生的纯 usage 事件过滤掉，保持客户端原语义不变（客户端显式要求时则透传）。
7. **上游未返回 usage 时**：费用记 0 并标记「未计费」（日志中显示为 —）。
8. **计费快照**：费用在请求完成时按当时价格计算并写入日志，之后修改价格不影响历史账目。
9. **价格匹配**：优先取 Provider 的模型级价格覆盖，未命中则用 Provider 默认价。
10. **认证**：管理 API 用登录会话（Bearer token，存储为 SHA-256 哈希）；代理端点用 LLM API Key；图片验证码一次性消费。
11. **调用方优先级**：代理请求先用 Key 匹配调用方（钱包）凭证，命中则校验余额与周期限额并扣费；未命中才回退到 LLM API 主 Key（不限额）。
12. **限额口径**：日/周/月限额按 `STATS_TZ` 时区的自然周期累计（今日 00:00、本周一 00:00、本月 1 日 00:00 起），统计来源为请求日志中的实际费用。
13. **计费口径**：三段独立计算——`(输入-缓存)×输入价 + 缓存×缓存价 + 输出×输出价`；三段 token 全部取自上游响应的 usage（含流式 SSE 事件），不做任何本地估算。
14. **价格回退**：模型覆盖价逐字段回退 Provider 默认价（只填部分字段不会把其余字段清零）；缓存命中价缺省（0）时按输入价计费。

## 目录结构

```
.
├── main.go                  # 入口：配置、迁移、管理员初始化、优雅退出
├── embed.go                 # go:embed 前端产物
├── internal/
│   ├── config/              # 环境变量
│   ├── auth/                # bcrypt、会话 token、图片验证码（纯标准库绘制）
│   ├── store/               # PostgreSQL 数据层（schema.sql + CRUD + 统计聚合）
│   ├── gateway/             # 代理核心：选路/熔断/切换/SSE 转发/计费/DeepSeek 增强
│   └── httpapi/             # 路由、中间件、管理 API、代理端点、静态资源
├── web/                     # Svelte 5 + Vite + TailwindCSS v4（无 SvelteKit，hash 路由）
│   ├── src/assets/icon.svg  # 品牌图标（原创设计，Codex 生成）
│   ├── public/favicon.svg   # 站点 favicon（与 icon.svg 同步）
│   └── src/app.css          # ClickUp 设计 token（@theme）
├── AGENTS.md                # AI 协作约定 / 设计系统说明
├── tools/mock_upstream.py   # 开发用 mock 上游（模拟 OpenAI 兼容接口）
├── Dockerfile               # 多阶段：前端构建 → Go 构建（嵌入）→ 精简运行时
└── docker-compose.yml       # app + postgres17 + 数据卷 + healthcheck
```

## 开发与测试

```bash
make test              # 全部测试
make test-unit         # 单元测试（选路/熔断/计费/SSE 过滤/验证码等，无需数据库）
make test-integration  # 数据库集成测试（真实 PostgreSQL + httptest 上游）
```

集成测试覆盖：登录与权限、代理转发、计费精度、故障切换、熔断冷却与跳过、请求体超限跳过、
Responses 仅直通、流式注入与过滤、模型列表、密钥轮换、DeepSeek 填价、拖拽重排等。
测试数据库默认为 `postgres://localhost:5432/flash_llm_router_test`，可用 `TEST_DATABASE_URL` 覆盖；
PostgreSQL 不可用时集成测试自动跳过。

## 已知限制

- 单实例部署设计（会话/验证码在内存，业务数据在 PostgreSQL；多实例需外置会话与验证码存储）
- 流式请求开始输出后无法切换 Provider（协议限制）
- 客户端显式要求 `include_usage` 时，上游把 usage 附在最后一个内容 chunk 上的实现（如部分中转）无法剥离，会原样透传
- 限额检查与扣费非原子：极端并发下（同一调用方同时大量在途请求）可能短暂超出限额或余额，扣费阶段用条件更新兜底不允许透支
- 日志默认不清理，长期运行可按需在 PostgreSQL 中清理 `request_logs` 表

## License

[MIT](LICENSE) © 2026 SudoUserReal
