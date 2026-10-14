<script>
  import { get, put } from '../lib/api.js'
  import { canWrite } from '../lib/session.svelte.js'
  import { toastOk, toastErr } from '../lib/toast.svelte.js'
  import { formatNumber, formatCost, formatLatency, formatDateTime, rangePreset } from '../lib/format.js'

  let apis = $state([])
  let apiId = $state('')
  let success = $state('')
  let modelQuery = $state('')
  let preset = $state('7d')

  let logs = $state([])
  let total = $state(0)
  let page = $state(1)
  let pageSize = 20
  let loading = $state(false)
  let expanded = $state(0)

  get('/api/llm-apis')
    .then((d) => (apis = d.llm_apis))
    .catch((e) => toastErr(e.message))

  async function load() {
    loading = true
    try {
      const qs = new URLSearchParams()
      if (apiId) qs.set('llm_api_id', apiId)
      if (success) qs.set('success', success)
      if (modelQuery.trim()) qs.set('model', modelQuery.trim())
      const { from, to } = rangePreset(preset)
      qs.set('from', from)
      qs.set('to', to)
      qs.set('page', String(page))
      qs.set('page_size', String(pageSize))
      const d = await get('/api/logs?' + qs.toString())
      logs = d.logs
      total = d.total
    } catch (e) {
      toastErr(e.message)
    } finally {
      loading = false
    }
  }
  load()

  // 日志保留设置
  let retentionDays = $state(30)
  get('/api/settings')
    .then((d) => (retentionDays = d.log_retention_days))
    .catch((e) => toastErr(e.message))

  async function saveRetention() {
    try {
      const d = await put('/api/settings', { log_retention_days: Number(retentionDays) })
      retentionDays = d.log_retention_days
      toastOk(`日志保留已设为 ${retentionDays === 0 ? '永久保留' : retentionDays + ' 天'}（每小时自动清理）`)
    } catch (e) {
      toastErr(e.message)
    }
  }

  const totalPages = $derived(Math.max(1, Math.ceil(total / pageSize)))

  function search() {
    page = 1
    load()
  }

  function goPage(p) {
    if (p < 1 || p > totalPages) return
    page = p
    load()
  }

  const endpointLabel = {
    'chat.completions': 'Chat',
    completions: 'Completions',
    responses: 'Responses',
  }
</script>

<div class="space-y-4">
  <!-- 筛选 -->
  <div class="card flex flex-wrap items-end gap-4 p-4">
    <div>
      <label class="label" for="lg-api">LLM API</label>
      <select id="lg-api" class="input !w-48" bind:value={apiId} onchange={search}>
        <option value="">全部</option>
        {#each apis as a (a.id)}
          <option value={a.id}>{a.name}</option>
        {/each}
      </select>
    </div>
    <div>
      <label class="label" for="lg-result">结果</label>
      <select id="lg-result" class="input !w-28" bind:value={success} onchange={search}>
        <option value="">全部</option>
        <option value="true">成功</option>
        <option value="false">失败</option>
      </select>
    </div>
    <div>
      <label class="label" for="lg-model">模型</label>
      <input id="lg-model" class="input !w-44" bind:value={modelQuery} placeholder="模糊搜索" onkeydown={(e) => e.key === 'Enter' && search()} />
    </div>
    <div>
      <label class="label" for="lg-preset">时间范围</label>
      <select id="lg-preset" class="input !w-36" bind:value={preset} onchange={search}>
        <option value="today">今天</option>
        <option value="yesterday">昨天</option>
        <option value="24h">最近 24 小时</option>
        <option value="7d">最近 7 天</option>
        <option value="30d">最近 30 天</option>
      </select>
    </div>
    <button class="btn btn-secondary" onclick={search}>查询</button>
    <div class="ml-auto flex items-center gap-3">
      <div class="flex items-center gap-2" title="超过保留期的日志每小时自动滚动删除">
        <span class="text-xs text-muted">日志保留</span>
        {#if canWrite()}
          <select class="input !w-28 !py-1 text-xs" bind:value={retentionDays} onchange={saveRetention}>
            <option value={7}>7 天</option>
            <option value={14}>14 天</option>
            <option value={30}>30 天</option>
            <option value={60}>60 天</option>
            <option value={90}>90 天</option>
            <option value={180}>180 天</option>
            <option value={365}>365 天</option>
            <option value={0}>永久保留</option>
          </select>
        {:else}
          <span class="chip !py-0.5 !text-[11px]">{retentionDays === 0 ? '永久保留' : retentionDays + ' 天'}</span>
        {/if}
      </div>
      <span class="text-xs text-muted/80">共 {formatNumber(total)} 条</span>
    </div>
  </div>

  <!-- 日志表 -->
  <div class="card overflow-hidden">
    <div class="overflow-x-auto">
      <table class="w-full">
        <thead class="border-b border-line bg-surface/70">
          <tr>
            <th class="table-th">时间</th>
            <th class="table-th">LLM API</th>
            <th class="table-th">Provider</th>
            <th class="table-th">端点 / 模型</th>
            <th class="table-th">状态</th>
            <th class="table-th text-right">Tokens（入/缓存/出）</th>
            <th class="table-th text-right">费用</th>
            <th class="table-th text-right">延迟</th>
            <th class="table-th text-right">尝试</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-100">
          {#if loading}
            <tr><td class="table-td text-muted/80" colspan="9">加载中…</td></tr>
          {:else if logs.length === 0}
            <tr><td class="table-td text-muted/80" colspan="9">暂无日志</td></tr>
          {/if}
          {#each logs as l (l.id)}
            <tr class="cursor-pointer hover:bg-surface/60" onclick={() => (expanded = expanded === l.id ? 0 : l.id)}>
              <td class="table-td text-xs whitespace-nowrap text-muted">{formatDateTime(l.created_at)}</td>
              <td class="table-td text-xs">{l.llm_api_name || '-'}</td>
              <td class="table-td text-xs">{l.provider_name || '-'}</td>
              <td class="table-td">
                <div class="flex items-center gap-1.5 text-xs">
                  <span class="badge badge-neutral">{endpointLabel[l.endpoint] || l.endpoint}</span>
                  {#if l.stream}<span class="badge badge-info">流式</span>{/if}
                  <span class="font-mono">{l.model}</span>
                </div>
              </td>
              <td class="table-td">
                {#if l.success}
                  <span class="badge badge-success">{l.status_code}</span>
                {:else}
                  <span class="badge badge-error">{l.status_code || 'ERR'}</span>
                {/if}
              </td>
              <td class="table-td text-right font-mono text-xs">
                {formatNumber(l.prompt_tokens)} / {formatNumber(l.cached_tokens)} / {formatNumber(l.completion_tokens)}
              </td>
              <td class="table-td text-right">
                {#if l.priced}
                  <span class="text-accent">{formatCost(l.cost)}</span>
                {:else}
                  <span class="text-xs text-muted/80" title="上游未返回 usage">—</span>
                {/if}
              </td>
              <td class="table-td text-right text-xs text-muted">{formatLatency(l.latency_ms)}</td>
              <td class="table-td text-right text-xs text-muted">{l.attempts}</td>
            </tr>
            {#if expanded === l.id}
              <tr class="bg-surface/80">
                <td class="px-4 py-3" colspan="9">
                  <div class="space-y-2 text-xs">
                    <div class="flex flex-wrap gap-x-6 gap-y-1 text-muted">
                      <span>请求体大小：{(l.request_bytes / 1024).toFixed(1)} KB</span>
                      <span>客户端 IP：{l.client_ip || '-'}</span>
                      <span>日志 ID：#{l.id}</span>
                    </div>
                    {#if l.error}
                      <div class="rounded-lg bg-[#fdecec] px-3 py-2 font-mono text-error ring-1 ring-[#f5c6c8]">{l.error}</div>
                    {/if}
                    {#if l.attempts_detail?.length}
                      <div>
                        <div class="mb-1 font-medium text-body/70">Provider 尝试记录（按顺序）</div>
                        <div class="space-y-1">
                          {#each l.attempts_detail as a, i (i)}
                            <div class="flex items-center gap-3 rounded-[12px] bg-canvas px-3 py-1.5 ring-1 ring-line">
                              <span class="w-4 text-center text-muted/80">{i + 1}</span>
                              <span class="font-medium">{a.provider}</span>
                              {#if a.status >= 200 && a.status < 300}
                                <span class="badge badge-success">{a.status}</span>
                              {:else if a.status}
                                <span class="badge badge-error">{a.status}</span>
                              {:else}
                                <span class="badge badge-warning">网络错误</span>
                              {/if}
                              <span class="text-muted/80">{formatLatency(a.latency_ms)}</span>
                              {#if a.error}<span class="min-w-0 flex-1 truncate font-mono text-error">{a.error}</span>{/if}
                            </div>
                          {/each}
                        </div>
                      </div>
                    {/if}
                  </div>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
    <!-- 分页 -->
    <div class="flex items-center justify-between border-t border-line px-4 py-3 text-sm">
      <span class="text-xs text-muted/80">第 {page} / {totalPages} 页</span>
      <div class="flex gap-2">
        <button class="btn btn-secondary !px-3 !py-1 text-xs" disabled={page <= 1} onclick={() => goPage(page - 1)}>上一页</button>
        <button class="btn btn-secondary !px-3 !py-1 text-xs" disabled={page >= totalPages} onclick={() => goPage(page + 1)}>下一页</button>
      </div>
    </div>
  </div>
</div>
