<script>
  import { get } from '../lib/api.js'
  import { navigate } from '../lib/router.svelte.js'
  import { formatNumber, formatCompact, formatCost } from '../lib/format.js'

  let data = $state(null)
  let error = $state('')

  async function load() {
    try {
      data = await get('/api/overview')
    } catch (e) {
      error = e.message
    }
  }
  load()
</script>

{#if error}
  <div class="card p-6 text-sm text-error">{error}</div>
{:else if !data}
  <div class="text-sm text-muted/80">加载中…</div>
{:else}
  <div class="space-y-6">
    <!-- 统计卡片 -->
    <div class="grid grid-cols-2 gap-4 xl:grid-cols-4">
      <div class="card p-4">
        <div class="text-xs text-muted">今日请求</div>
        <div class="mt-1 text-2xl font-bold">{formatNumber(data.today.requests)}</div>
        <div class="mt-1 text-xs text-muted/80">
          成功 {formatNumber(data.today.success)} · 共 {formatNumber(data.total.requests)}
        </div>
      </div>
      <div class="card p-4">
        <div class="text-xs text-muted">今日费用</div>
        <div class="mt-1 text-2xl font-bold text-accent">{formatCost(data.today.cost)}</div>
        <div class="mt-1 text-xs text-muted/80">近 7 日 {formatCost(data.last_7d.cost)}</div>
      </div>
      <div class="card p-4">
        <div class="text-xs text-muted">今日 Tokens</div>
        <div class="mt-1 text-2xl font-bold">{formatCompact(data.today.total_tokens)}</div>
        <div class="mt-1 text-xs text-muted/80">
          输入 {formatCompact(data.today.prompt_tokens)} · 输出 {formatCompact(data.today.completion_tokens)}
        </div>
      </div>
      <div class="card p-4">
        <div class="text-xs text-muted">累计费用</div>
        <div class="mt-1 text-2xl font-bold">{formatCost(data.total.cost)}</div>
        <div class="mt-1 text-xs text-muted/80">
          累计 Tokens {formatCompact(data.total.total_tokens)} · 时区 {data.timezone}
        </div>
      </div>
    </div>

    <!-- LLM API 卡片 -->
    <div>
      <div class="mb-3 flex items-center justify-between">
        <h2 class="text-base font-bold text-ink">LLM API 服务</h2>
        <button class="btn btn-secondary" onclick={() => navigate('#/apis')}>管理全部 →</button>
      </div>
      {#if data.llm_apis.length === 0}
        <div class="card flex flex-col items-center gap-3 p-10 text-center">
          <p class="text-muted">还没有创建任何 LLM API 服务</p>
          <button class="btn btn-primary" onclick={() => navigate('#/apis')}>去创建第一个</button>
        </div>
      {:else}
        <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {#each data.llm_apis as api (api.id)}
            <button
              class="card p-4 text-left transition-shadow hover:shadow-md"
              onclick={() => navigate(`#/apis/${api.id}`)}
            >
              <div class="flex items-center justify-between">
                <div class="truncate font-medium text-ink">{api.name}</div>
                {#if api.enabled}
                  <span class="badge badge-success">启用</span>
                {:else}
                  <span class="badge badge-neutral">停用</span>
                {/if}
              </div>
              <div class="mt-1 flex gap-2 text-xs text-muted/80">
                <span>{api.provider_count} 个 Provider</span>
                <span>·</span>
                <span>{api.models?.length || 0} 个模型</span>
                {#if api.supports_vision}<span>·</span><span>🖼 图片</span>{/if}
                {#if api.supports_video}<span>·</span><span>🎬 视频</span>{/if}
              </div>
              <div class="mt-3 grid grid-cols-3 gap-2 border-t border-line pt-3 text-xs">
                <div>
                  <div class="text-muted/80">今日请求</div>
                  <div class="mt-0.5 font-medium text-body">{formatNumber(api.today.requests)}</div>
                </div>
                <div>
                  <div class="text-muted/80">今日 Tokens</div>
                  <div class="mt-0.5 font-medium text-body">{formatCompact(api.today.total_tokens)}</div>
                </div>
                <div>
                  <div class="text-muted/80">今日费用</div>
                  <div class="mt-0.5 font-medium text-accent">{formatCost(api.today.cost)}</div>
                </div>
              </div>
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{/if}
