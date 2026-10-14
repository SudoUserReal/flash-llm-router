<script>
  import { get } from '../lib/api.js'
  import { toastErr } from '../lib/toast.svelte.js'
  import { formatNumber, formatCompact, formatCost, rangePreset, formatLatency } from '../lib/format.js'
  import BarChart from '../components/BarChart.svelte'

  let apis = $state([])
  let apiId = $state('')
  let preset = $state('7d')
  let granularity = $state('day')
  let customFrom = $state('')
  let customTo = $state('')

  let points = $state([])
  let providerStats = $state([])
  let totals = $state(null)
  let timezone = $state('')
  let loading = $state(false)
  let metric = $state('cost') // cost | requests | tokens

  get('/api/llm-apis')
    .then((d) => (apis = d.llm_apis))
    .catch((e) => toastErr(e.message))

  function currentRange() {
    if (preset === 'custom') {
      return {
        from: customFrom ? new Date(customFrom).toISOString() : null,
        to: customTo ? new Date(customTo).toISOString() : null,
      }
    }
    return rangePreset(preset)
  }

  async function load() {
    const { from, to } = currentRange()
    loading = true
    try {
      const qs = new URLSearchParams()
      if (apiId) qs.set('llm_api_id', apiId)
      if (from) qs.set('from', from)
      if (to) qs.set('to', to)
      qs.set('granularity', granularity)

      const [ts, ps] = await Promise.all([
        get('/api/stats/timeseries?' + qs.toString()),
        get(
          '/api/stats/providers?' +
            (() => {
              const p = new URLSearchParams()
              if (apiId) p.set('llm_api_id', apiId)
              if (from) p.set('from', from)
              if (to) p.set('to', to)
              return p.toString()
            })(),
        ),
      ])
      points = ts.points
      timezone = ts.timezone
      providerStats = ps.providers
      totals = ps.totals
    } catch (e) {
      toastErr(e.message)
    } finally {
      loading = false
    }
  }

  // 预设切换时自动选择合理粒度
  function onPresetChange(v) {
    preset = v
    if (v === '1h' || v === '24h') granularity = 'hour'
    else if (v !== 'custom') granularity = 'day'
    load()
  }

  load()

  const pad = (x) => String(x).padStart(2, '0')

  const chartPoints = $derived(
    points.map((p) => {
      const d = new Date(p.bucket)
      const label =
        granularity === 'hour'
          ? `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}时`
          : `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
      let value = 0
      if (metric === 'cost') value = p.cost
      else if (metric === 'requests') value = p.requests
      else value = p.total_tokens
      return { label, tooltip: label, value }
    }),
  )

  const metricLabel = $derived({ cost: '费用', requests: '请求数', tokens: 'Tokens' }[metric])

  const successRate = $derived(totals && totals.requests > 0 ? ((totals.success / totals.requests) * 100).toFixed(1) : '-')
</script>

<div class="space-y-5">
  <!-- 筛选栏 -->
  <div class="card flex flex-wrap items-end gap-4 p-4">
    <div>
      <label class="label" for="st-api">LLM API</label>
      <select id="st-api" class="input !w-56" bind:value={apiId} onchange={load}>
        <option value="">全部</option>
        {#each apis as a (a.id)}
          <option value={a.id}>{a.name}</option>
        {/each}
      </select>
    </div>
    <div>
      <label class="label" for="st-preset">时间范围</label>
      <select id="st-preset" class="input !w-36" value={preset} onchange={(e) => onPresetChange(e.target.value)}>
        <option value="today">今天</option>
        <option value="yesterday">昨天</option>
        <option value="1h">最近 1 小时</option>
        <option value="24h">最近 24 小时</option>
        <option value="7d">最近 7 天</option>
        <option value="30d">最近 30 天</option>
        <option value="custom">自定义</option>
      </select>
    </div>
    {#if preset === 'custom'}
      <div>
        <label class="label" for="st-from">开始</label>
        <input id="st-from" class="input !w-52" type="datetime-local" bind:value={customFrom} onchange={load} />
      </div>
      <div>
        <label class="label" for="st-to">结束</label>
        <input id="st-to" class="input !w-52" type="datetime-local" bind:value={customTo} onchange={load} />
      </div>
    {/if}
    <div>
      <label class="label" for="st-gran">粒度</label>
      <select id="st-gran" class="input !w-28" bind:value={granularity} onchange={load}>
        <option value="hour">按小时</option>
        <option value="day">按天</option>
      </select>
    </div>
  </div>

  <!-- 汇总卡片 -->
  {#if totals}
    <div class="grid grid-cols-2 gap-4 xl:grid-cols-4">
      <div class="card p-4">
        <div class="text-xs text-muted">请求数</div>
        <div class="mt-1 text-2xl font-bold">{formatNumber(totals.requests)}</div>
        <div class="mt-1 text-xs text-muted/80">成功 {formatNumber(totals.success)} · 成功率 {successRate}%</div>
      </div>
      <div class="card p-4">
        <div class="text-xs text-muted">费用</div>
        <div class="mt-1 text-2xl font-bold text-accent">{formatCost(totals.cost)}</div>
        <div class="mt-1 text-xs text-muted/80">CNY · 时区 {timezone}</div>
      </div>
      <div class="card p-4">
        <div class="text-xs text-muted">Tokens</div>
        <div class="mt-1 text-2xl font-bold">{formatCompact(totals.total_tokens)}</div>
        <div class="mt-1 text-xs text-muted/80">
          缓存命中 {formatCompact(totals.cached_tokens)}（占输入 {totals.prompt_tokens > 0 ? ((totals.cached_tokens / totals.prompt_tokens) * 100).toFixed(0) : 0}%）
        </div>
      </div>
      <div class="card p-4">
        <div class="text-xs text-muted">输入 / 输出 Tokens</div>
        <div class="mt-1 text-2xl font-bold">{formatCompact(totals.prompt_tokens)} <span class="text-base text-muted/80">/</span> {formatCompact(totals.completion_tokens)}</div>
        <div class="mt-1 text-xs text-muted/80">按 Provider 明细见下方表格</div>
      </div>
    </div>
  {/if}

  <!-- 图表 -->
  <div class="card p-5">
    <div class="mb-4 flex items-center justify-between">
      <h3 class="text-sm font-semibold text-body">用量趋势（{metricLabel}）</h3>
      <div class="flex rounded-lg bg-surface p-0.5 text-xs">
        {#each [{ k: 'cost', l: '费用' }, { k: 'requests', l: '请求数' }, { k: 'tokens', l: 'Tokens' }] as m (m.k)}
          <button
            class="rounded-md px-3 py-1 font-medium transition-colors {metric === m.k ? 'bg-canvas text-ink shadow-[0_1px_3px_rgba(0,0,0,0.1)]' : 'text-muted hover:text-body'}"
            onclick={() => (metric = m.k)}
          >{m.l}</button>
        {/each}
      </div>
    </div>
    {#if loading}
      <div class="py-16 text-center text-sm text-muted/80">加载中…</div>
    {:else}
      <BarChart
        points={chartPoints}
        colorClass={metric === 'cost' ? 'bg-accent' : metric === 'requests' ? 'bg-pale-blue0' : 'bg-logo-purple'}
        valueFmt={metric === 'cost' ? formatCost : formatCompact}
      />
    {/if}
  </div>

  <!-- Provider 明细 -->
  <div class="card overflow-hidden">
    <div class="border-b border-line px-5 py-3">
      <h3 class="text-sm font-semibold text-body">Provider 用量与费用明细（当前筛选时间段）</h3>
    </div>
    <table class="w-full">
      <thead class="border-b border-line bg-surface/70">
        <tr>
          <th class="table-th">Provider</th>
          <th class="table-th text-right">请求</th>
          <th class="table-th text-right">成功率</th>
          <th class="table-th text-right">输入</th>
          <th class="table-th text-right">缓存命中</th>
          <th class="table-th text-right">输出</th>
          <th class="table-th text-right">总 Tokens</th>
          <th class="table-th text-right">费用</th>
          <th class="table-th text-right">平均延迟</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-slate-100">
        {#if providerStats.length === 0}
          <tr><td class="table-td text-muted/80" colspan="9">该时间段暂无数据</td></tr>
        {/if}
        {#each providerStats as p (p.provider_id + '-' + p.provider_name)}
          <tr class="hover:bg-surface/60">
            <td class="table-td font-medium">{p.provider_name || '（未命中 Provider）'}</td>
            <td class="table-td text-right">{formatNumber(p.requests)}</td>
            <td class="table-td text-right">{p.requests > 0 ? ((p.success / p.requests) * 100).toFixed(1) : '-'}%</td>
            <td class="table-td text-right">{formatCompact(p.prompt_tokens)}</td>
            <td class="table-td text-right text-success">{formatCompact(p.cached_tokens)}</td>
            <td class="table-td text-right">{formatCompact(p.completion_tokens)}</td>
            <td class="table-td text-right">{formatCompact(p.total_tokens)}</td>
            <td class="table-td text-right font-medium text-accent">{formatCost(p.cost)}</td>
            <td class="table-td text-right text-muted">{formatLatency(Math.round(p.avg_latency_ms))}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>
