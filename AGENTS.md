# AGENTS.md

本文件为 AI 编码代理（Codex / OpenCode / 其他）的协作约定与项目关键上下文。修改代码前请先阅读。

## 前端设计系统

- **设计风格：ClickUp**
- **规范来源（必读）：https://designmd-store.com/packs/clickup**
- 任何 UI 改动（新增页面、组件、状态色）都必须遵循该规范；token 已落地在 `web/src/app.css` 的 `@theme` 中。

### 品牌图标（原创）

- 项目图标为**原创设计，由 Codex 生成**：`web/src/assets/icon.svg`
  - 设计语汇：一个路由节点 + 两条无交叉的分流通道（表达"请求分流/汇聚"），紫 → 蓝线性渐变，圆角端点，24px 起清晰可辨。
- favicon：`web/public/favicon.svg`（与 icon.svg 同文件的副本，修改图标时需同步）。
- 引用方式：统一经 `web/src/components/Logo.svelte`（`import iconUrl from '../assets/icon.svg'`）。
- **禁止**在图标或 UI 中模仿/复用任何第三方品牌标识（如 ClickUp 双箭头、OpenAI 花环等）。

### 核心规则（摘自 ClickUp 规范）

1. **主 CTA 用黑色**（`#202020`，`.btn-primary`），不要用紫色或彩色做主按钮。
2. **紫色（`#7612FA`）只做强调**：选中态、次级按钮文字、图表、链接性操作。
3. 卡片 = 白底 + 细灰边（`--color-line`）+ 微阴影 + 大圆角（`rounded-2xl`）。
4. 标题用 Plus Jakarta Sans（`var(--font-display)`，负字距），正文用 Inter，标签/代码用 Sometype Mono（`var(--font-mono)`）。
5. 状态标签统一走 `.badge-*` 变体（success / error / warning / info / accent / neutral），不要手写色值。
6. 紫色渐变只属于 logo 与图表语汇（`logo-purple/blue/pink/orange`），不要给普通卡片加渐变。
7. 产品宽度感：用 `.chip`（mono 字体信息片）、badge 表达"能力广度"，而不是大段文字。

### 颜色 token（`web/src/app.css`）

| token | 值 | 用途 |
|---|---|---|
| `primary` / `deep-panel` | `#202020` / `#1E1E1E` | 主 CTA、深色块 |
| `accent` | `#7612FA` | 紫色强调、选中态、图表 |
| `surface` / `surface-alt` | `#F8F9FA` / `#F1F1F9` | 页面底 / 次级按钮与激活导航底 |
| `line` / `edge` | `#E9E9EF` / `#CFCFCF` | 细分割线 / 输入框边框 |
| `logo-purple/blue/pink/orange` | `#6647F0 / #0091FF / #FF02F0 / #F76808` | 图标与图表渐变配色 |
| `pale-purple/pink/blue/orange` | `#EFEDFD / #FFF1FE / #E9E9F6 / #FFECE5` | 大面积浅色块（激活项、提示条） |
| `success / warning / error / link` | `#30A46C / #FFC53D / #E5484D / #0091FF` | 语义状态 |

### 排版

- 字体经 Google Fonts 在 `web/index.html` 加载（Plus Jakarta Sans / Inter / Sometype Mono），离线环境自动回退系统字体。
- 数据大数字（金额、token）用 `font-bold`；表头用 `.table-th`（uppercase 小字）。

## 项目约定

- 后端：Go 单二进制，前端产物经 `//go:embed web/dist` 打包；`internal/store/schema.sql` 是唯一 schema 来源（启动时幂等迁移）。
- 计费：CNY / 百万 tokens，Provider 默认价 + 模型级覆盖价；日志记录"当时价格"快照。
- 代理语义（勿随意更改，README「行为语义」有完整说明）：
  - 非 2xx 一律视为 Provider 失败并自动切换；
  - 流式请求开始输出后不可切换；
  - 流式计费注入 `stream_options.include_usage`，并过滤由此产生的纯 usage 事件；
  - 优先级的数字越小越优先，成功一次清除连续失败计数。
- 调用方（钱包）语义：`api_clients` 持 Key，余额 ≤ 0 或超日/周/月限额时拒绝请求；成功后按实际费用条件扣费；LLM API 主 Key 不受限。改鉴权逻辑时注意保留主 Key 回退路径。
- 修改前端后必须：`cd web && npm run build`，然后重新 `go build`（`go:embed` 需要新产物）。

## 常用命令

```bash
make build            # 前端 + 后端完整构建
make test             # 全部测试（单元 + 集成，需 PostgreSQL）
make test-unit        # 仅单元测试（无数据库依赖）
make docker           # docker compose up --build（app + postgres）
```
