# flash-llm-router API 文档

flash-llm-router 是一个 LLM API 中转网关：**对外一个兼容 OpenAI 的接口，内部按优先级路由到多个上游 Provider**，失败自动切换，并按 token 用量精确计费。

- **Base URL**：`{{BASE_URL}}`
- **协议兼容**：OpenAI Chat Completions / Completions / Responses / Models
- **计费币种**：CNY（人民币）

---

## 认证

所有 `/v1/*` 接口都需要在请求头携带 API Key，支持两种写法：

```bash
-H "Authorization: Bearer {{API_KEY}}"
# 或
-H "x-api-key: {{API_KEY}}"
```

网关中存在两类 Key：

| 类型 | 获取方式 | 钱包/限额 |
|---|---|---|
| **调用方 Key** | 管理员在「LLM API 详情 → 调用方 / 钱包」中创建 | 受该调用方的钱包余额与日/周/月限额约束 |
| **主 Key** | 创建 LLM API 时生成（页面顶部「对外凭证」） | 不受限制，供管理/自测使用 |

> 请求会先用 Key 匹配调用方凭证，未匹配时才回退到主 Key。

---

## 接口速查

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/v1/chat/completions` | 对话补全（支持流式 `stream: true`） |
| POST | `/v1/completions` | 文本补全（Legacy，部分上游支持） |
| POST | `/v1/responses` | Responses API（仅路由给声明支持的 Provider） |
| GET | `/v1/models` | 模型列表（返回配置的模型清单） |
| GET | `/v1/models/{model}` | 单个模型信息 |

---

## POST /v1/chat/completions

OpenAI 兼容的对话补全接口。网关会将请求转发到优先级最高且可用的上游 Provider；任一 Provider 返回非 2xx 会自动切换到下一个。

### 请求参数

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | string | ✅ | 模型名，需与配置的模型清单一致 |
| `messages` | array | ✅ | 对话消息数组，元素含 `role`、`content` |
| `stream` | boolean | ⭕ | 是否流式返回，默认 `false` |
| `temperature` | number | ⭕ | 采样温度（上游支持时透传） |
| `top_p` | number | ⭕ | 核采样 |
| `max_tokens` / `max_completion_tokens` | integer | ⭕ | 最大生成 token 数 |
| `stream_options.include_usage` | boolean | ⭕ | 流式结束时附带 usage；不传时网关会自行注入用于计费并过滤该事件 |
| `tools` / `tool_choice` | array/object | ⭕ | 工具调用（上游支持时透传） |
| 其他字段 | any | ⭕ | 未列出的字段原样透传给上游 |

`messages[].role` 可选值：`system`、`user`、`assistant`、`tool`。

### 请求示例

```bash
curl {{BASE_URL}}/v1/chat/completions \
  -H "Authorization: Bearer {{API_KEY}}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-flash",
    "messages": [
      {"role": "system", "content": "你是一个简洁的助手"},
      {"role": "user", "content": "你好"}
    ],
    "max_tokens": 100
  }'
```

### 响应示例

```json
{
  "id": "cmpl-xxxxxxxx",
  "object": "chat.completion",
  "created": 1792001443,
  "model": "deepseek-flash",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "你好！有什么可以帮你的？"},
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 38,
    "completion_tokens": 11,
    "total_tokens": 49,
    "prompt_tokens_details": {"cached_tokens": 0}
  }
}
```

### 流式响应

请求体加 `"stream": true`，返回 `text/event-stream`，逐行 `data: {chunk}`，以 `data: [DONE]` 结束：

```bash
curl -N {{BASE_URL}}/v1/chat/completions \
  -H "Authorization: Bearer {{API_KEY}}" \
  -H "Content-Type: application/json" \
  -d '{"model": "deepseek-flash", "stream": true, "messages": [{"role": "user", "content": "数 1 到 5"}]}'
```

```text
data: {"id":"...","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"1"}}]}

data: {"id":"...","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":", 2"}}]}

data: [DONE]
```

> 部分上游（DeepSeek 等）会在 `delta.reasoning_content` 中返回思考过程，字段原样透传。

---

## POST /v1/completions

文本补全接口（Legacy Completions）。参数与响应结构与 Chat Completions 类似，`prompt` 代替 `messages`。仅在所选上游支持时可用，否则会返回上游错误或自动切换到其他 Provider。

```bash
curl {{BASE_URL}}/v1/completions \
  -H "Authorization: Bearer {{API_KEY}}" \
  -H "Content-Type: application/json" \
  -d '{"model": "deepseek-flash", "prompt": "Once upon a time", "max_tokens": 50}'
```

---

## POST /v1/responses

OpenAI Responses API 格式。**仅直通**：网关只把该请求发给在管理端勾选了「原生支持 Responses API」的 Provider，不支持协议转换；没有可用 Provider 时返回 `503`。

### 请求参数（常用）

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | string | ✅ | 模型名 |
| `input` | string \| array | ✅ | 输入内容（字符串或 input items 数组） |
| `instructions` | string | ⭕ | 系统指令 |
| `max_output_tokens` | integer | ⭕ | 最大输出 token |
| `stream` | boolean | ⭕ | 流式返回 |

### 请求示例

```bash
curl {{BASE_URL}}/v1/responses \
  -H "Authorization: Bearer {{API_KEY}}" \
  -H "Content-Type: application/json" \
  -d '{"model": "deepseek-flash", "input": "用一句话介绍你自己"}'
```

### 响应示例

```json
{
  "id": "resp_xxxxxxxx",
  "object": "response",
  "status": "completed",
  "output": [
    {"type": "reasoning", "content": [{"type": "reasoning_text", "text": "..."}]},
    {"type": "message", "role": "assistant",
     "content": [{"type": "output_text", "text": "你好，我是…"}]}
  ],
  "usage": {"input_tokens": 66, "output_tokens": 22, "total_tokens": 88}
}
```

---

## GET /v1/models

返回该 LLM API 配置的模型清单（管理端手动维护）。

```bash
curl {{BASE_URL}}/v1/models -H "Authorization: Bearer {{API_KEY}}"
```

```json
{
  "object": "list",
  "data": [
    {"id": "deepseek-flash", "object": "model", "created": 1792001673, "owned_by": "flash-llm-router"},
    {"id": "deepseek-v4.1-flash", "object": "model", "created": 1792001673, "owned_by": "flash-llm-router"}
  ]
}
```

`GET /v1/models/{model}` 返回单个模型信息，模型不在清单中时返回 `404`。

---

## 计费说明

- 计费口径：`费用 = 未命中输入 tokens × 输入单价 + 缓存命中 tokens × 缓存单价 + 输出 tokens × 输出单价`（单价为 **CNY / 百万 tokens**，配置在 Provider 上，支持按模型覆盖）
- `usage.prompt_tokens_details.cached_tokens` 为缓存命中输入 tokens（DeepSeek 为 `prompt_cache_hit_tokens`），按缓存单价计费
- **token 来源**：三段 token 全部取自上游响应的 `usage`（非流式读响应体；流式读 SSE 的 usage 事件与 `response.completed`），网关不做任何本地估算
- **价格回退**：模型覆盖价逐字段回退默认价（只填部分字段不会清零其余字段）；缓存命中价留空（0）时按输入价计费
- 计费在请求完成时按**当时价格**结算并写入日志，之后修改价格不影响历史账单
- 流式请求：若客户端未显式设置 `stream_options.include_usage`，网关会自动注入以获取 usage 用于计费，并过滤掉由此产生的 usage 事件（客户端感知不到）；若上游未返回 usage，该次请求不产生费用（日志标记为未计费）

---

## 钱包与限额

调用方 Key 的请求在进入网关时会校验：

1. **钱包余额**：余额 ≤ 0 → `429 insufficient_balance`
2. **消费限额**：分别校验每日 / 每周 / 每月上限（按网关统计时区的自然周期累计）→ `429 daily_limit_exceeded` / `weekly_limit_exceeded` / `monthly_limit_exceeded`
3. 请求成功后按实际费用从钱包扣除；扣费使用条件更新，不会透支

---

## 图片转 URL（阿里云 OSS，可选）

Provider 配置中的「图片转 URL」开关（默认关闭）开启后，网关会把请求体中 `data:image/...;base64` 形式的图片上传到阿里云 OSS，并替换为公开 URL 后再转发给上游。

- **收益**：多轮对话中图片以短 URL 重复发送而非 base64（可省数 MB/轮），上游直接按 URL 拉图
- **范围**：仅处理 `data:image` 形式的 base64（外链图片不动）；支持 Chat Completions（`image_url`）与 Responses（`input_image`）两种格式
- **去重**：图片按内容 SHA-256 哈希作为对象名（`flr-images/<hash>.<ext>`），相同图片只上传一次（进程内缓存）
- **降级**：OSS 未配置完整或上传失败时，自动保留 base64 原样转发，请求不受影响
- **前置条件**：Bucket 需允许公网读取（或配置「公开访问前缀」走 CDN），否则上游无法拉取图片

OSS 配置为**全局设置**（Endpoint / Bucket / AccessKey ID / AccessKey Secret / 公开访问前缀），在任意 Provider 编辑弹窗中填写，或通过管理 API 设置：

```bash
curl -X PUT {{BASE_URL}}/api/settings \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"oss":{"endpoint":"oss-cn-hangzhou.aliyuncs.com","bucket":"my-images","access_key_id":"...","access_key_secret":"...","public_base":"https://cdn.example.com"}}'
```

## 错误响应

所有错误均为 OpenAI 兼容格式：

```json
{"error": {"code": "invalid_request_error", "message": "错误描述", "type": "invalid_request_error"}}
```

| HTTP | code / type | 含义 |
|---|---|---|
| 400 | `invalid_request_error` | 请求体不是合法 JSON、缺少 `model` 等 |
| 401 | `invalid_request_error` | 缺少 API Key，或 Key 无效 / 服务已禁用 / 调用方已禁用 |
| 403 | `forbidden` | 调用方账号已被禁用 |
| 404 | `invalid_request_error` | 请求的模型不在模型清单中（`/v1/models/{model}`） |
| 413 | `request_too_large` | 请求体超过 64MB |
| 499 | `client_closed_request` | 请求中断（客户端断开，或服务重启时残留的进行中请求） |
| 429 | `insufficient_balance` | 钱包余额不足 |
| 429 | `daily_limit_exceeded` / `weekly_limit_exceeded` / `monthly_limit_exceeded` | 超出对应周期消费限额 |
| 503 | `router_unavailable` | 没有可用 Provider（全部禁用 / 熔断冷却 / 被跳过条件过滤） |
| 502 | `upstream_error` | 所有 Provider 均失败且最后一次为网络错误 |

> 上游返回的非 2xx 会作为最后一次结果透传给客户端（保留上游状态码与错误体），便于排查。

---

## SDK 接入示例

### Python（OpenAI SDK）

```python
from openai import OpenAI

client = OpenAI(
    base_url="{{BASE_URL}}/v1",
    api_key="{{API_KEY}}",
)

resp = client.chat.completions.create(
    model="deepseek-flash",
    messages=[{"role": "user", "content": "你好"}],
)
print(resp.choices[0].message.content)

# 流式
for chunk in client.chat.completions.create(
    model="deepseek-flash",
    messages=[{"role": "user", "content": "你好"}],
    stream=True,
):
    print(chunk.choices[0].delta.content or "", end="", flush=True)
```

### Node.js（openai SDK）

```javascript
import OpenAI from "openai";

const client = new OpenAI({ baseURL: "{{BASE_URL}}/v1", apiKey: "{{API_KEY}}" });

const resp = await client.chat.completions.create({
  model: "deepseek-flash",
  messages: [{ role: "user", content: "你好" }],
});
console.log(resp.choices[0].message.content);
```

---

## 管理 API（供 Agent / 自动化）

除对外代理接口外，网关自带一套管理 API（`/api/*`），供 AI Agent 与自动化脚本完成配置、查询与运维，无需操作界面。

### 认证流程

```bash
# 1. 获取验证码（返回 captcha_id 与验证码图片 base64）
curl {{BASE_URL}}/api/auth/captcha

# 2. 登录（验证码字符取自图片）
curl -X POST {{BASE_URL}}/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"…","captcha_id":"<id>","captcha_code":"<code>"}'
# → {"token":"sess-…","user":{…}}

# 3. 后续请求携带会话 token
curl {{BASE_URL}}/api/llm-apis -H "Authorization: Bearer <token>"
```

### 常用端点

| 方法 | 端点 | 用途 |
|---|---|---|
| GET / POST | `/api/llm-apis` | 服务列表 / 创建对外服务（可自定义 Key） |
| GET / PUT / DELETE | `/api/llm-apis/{id}` | 服务详情 / 更新 / 删除 |
| POST | `/api/llm-apis/{id}/rotate-key` | 重置服务主 Key |
| POST | `/api/llm-apis/{id}/providers` | 添加上游 Provider（优先级/熔断/超时/价格） |
| PUT / DELETE | `/api/providers/{id}` | 更新 / 删除 Provider |
| POST | `/api/llm-apis/{id}/providers/reorder` | 批量重排优先级 `{"ids":[3,1,2]}` |
| POST | `/api/providers/{id}/reset-health` | 清除熔断冷却 |
| POST | `/api/providers/{id}/balance` | DeepSeek 余额查询（仅 kind=deepseek） |
| POST | `/api/providers/{id}/autofill-price` | 官方价目一键填充 `{"tier":"peak\|offpeak"}` |
| POST | `/api/providers/{id}/ping` | 连通性测试（请求上游 /v1/models） |
| GET / POST | `/api/llm-apis/{id}/clients` | 调用方列表（含本周期用量）/ 创建（钱包+限额） |
| PUT / DELETE | `/api/clients/{id}` | 更新限额与状态 / 删除 |
| POST | `/api/clients/{id}/recharge` | 充值（正数）或扣减（负数）`{"amount":10}` |
| POST | `/api/clients/{id}/rotate-key` | 重置调用方 Key |
| GET | `/api/logs` | 请求日志（`llm_api_id` / `provider_id` / `client_id` / `success` / `from` / `to` / `page` / `page_size`） |
| GET | `/api/stats/timeseries` | 用量趋势（`granularity=hour\|day`，含 `llm_api_id` 过滤） |
| GET | `/api/stats/providers` | 区间内各 Provider 费用 / token / 延迟明细 |
| GET / PUT | `/api/settings` | 系统设置（如 `log_retention_days` 日志保留天数） |
| GET | `/api/overview` | 总览（今日 / 近 7 日 / 各服务摘要） |
| GET | `/api/users` / POST | 用户管理（仅 admin，含角色 admin/operator/viewer） |

> 管理 API 的错误格式与代理接口一致：`{"error":{"code":"...","message":"..."}}`，便于 Agent 自动判断与重试。

## 注意事项

- **自动切换**：一次请求内按 Provider 优先级依次尝试，直到成功或全部失败；流式响应一旦开始输出便无法切换
- **超时**：每个 Provider 可单独配置超时——非流式为总超时，流式为「连接 + 首字节」超时
- **熔断**：某 Provider 连续失败达到阈值会进入冷却，期间不参与调度；成功一次计数清零
- **请求体过滤**：请求体超过 Provider 配置的大小上限时，本次请求会跳过该 Provider；Provider 开启「图片转 URL」时按图片替换为 URL 后的预估大小判定（纯本地估算，超限跳过时不会发起上传）
- `model` 参数原样透传给上游（不支持模型名映射），请使用 Provider 实际支持的模型名
