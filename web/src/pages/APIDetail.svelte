<script>
  import { get, post, put, del } from '../lib/api.js'
  import { canWrite } from '../lib/session.svelte.js'
  import { toastOk, toastErr } from '../lib/toast.svelte.js'
  import { formatDateTime, formatCost, cooldownRemaining, copyText, timeAgo } from '../lib/format.js'
  import Modal from '../components/Modal.svelte'

  let { apiId } = $props()

  let api = $state(null)
  let providers = $state([])
  let error = $state('')
  let showKey = $state(false)

  // ---------- 数据加载
  async function load() {
    try {
      const d = await get(`/api/llm-apis/${apiId}`)
      api = d.llm_api
      providers = d.providers
    } catch (e) {
      error = e.message
    }
    loadClients()
  }
  load()

  // ---------- API 基本信息编辑
  let showApiModal = $state(false)
  let aform = $state({ name: '', description: '', modelsText: '', supports_vision: true, supports_video: false, enabled: true })
  let savingApi = $state(false)

  function openApiModal() {
    aform = {
      name: api.name,
      description: api.description,
      modelsText: (api.models || []).join('\n'),
      supports_vision: api.supports_vision,
      supports_video: api.supports_video,
      enabled: api.enabled,
    }
    showApiModal = true
  }

  async function saveApi() {
    savingApi = true
    try {
      const updated = await put(`/api/llm-apis/${apiId}`, {
        name: aform.name.trim(),
        description: aform.description.trim(),
        models: aform.modelsText.split(/[\n,，]/).map((s) => s.trim()).filter(Boolean),
        supports_vision: aform.supports_vision,
        supports_video: aform.supports_video,
        enabled: aform.enabled,
      })
      api = updated
      showApiModal = false
      toastOk('已保存')
    } catch (e) {
      toastErr(e.message)
    } finally {
      savingApi = false
    }
  }

  async function rotateKey() {
    if (!confirm('重置后旧 Key 立即失效，所有使用方需要更换。确定重置？')) return
    try {
      api = await post(`/api/llm-apis/${apiId}/rotate-key`)
      showKey = true
      toastOk('API Key 已重置')
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function removeApi() {
    if (!confirm(`确定删除「${api.name}」？`)) return
    try {
      await del(`/api/llm-apis/${apiId}`)
      toastOk('已删除')
      location.hash = '#/apis'
    } catch (e) {
      toastErr(e.message)
    }
  }

  // ---------- Provider 表单
  function emptyProviderForm() {
    return {
      name: '', kind: 'openai', base_url: '', api_key: '', priority: 100,
      enabled: true, supports_responses: false, inject_stream_usage: true,
      timeout_seconds: 300, max_body_mb: 0, max_failures: 0, cooldown_seconds: 60,
      price_input: 0, price_output: 0, price_cached_input: 0,
    }
  }

  let showProviderModal = $state(false)
  let editing = $state(null)
  let pform = $state(emptyProviderForm())
  let priceRows = $state([])
  let savingProvider = $state(false)
  let autofillTier = $state('peak')

  function openCreateProvider() {
    editing = null
    pform = emptyProviderForm()
    pform.priority = (Math.max(0, ...providers.map((p) => p.priority)) || 0) + 10
    priceRows = []
    showProviderModal = true
  }

  function openEditProvider(p) {
    editing = p
    pform = {
      name: p.name, kind: p.kind, base_url: p.base_url, api_key: p.api_key,
      priority: p.priority, enabled: p.enabled, supports_responses: p.supports_responses,
      inject_stream_usage: p.inject_stream_usage, timeout_seconds: p.timeout_seconds,
      max_body_mb: p.max_body_mb, max_failures: p.max_failures, cooldown_seconds: p.cooldown_seconds,
      price_input: p.price_input, price_output: p.price_output, price_cached_input: p.price_cached_input,
    }
    priceRows = Object.entries(p.model_prices || {}).map(([model, v]) => ({
      model, input: v.input, output: v.output, cached: v.cached_input,
    }))
    autofillTier = 'peak'
    showProviderModal = true
  }

  function rowsToMap(rows) {
    const out = {}
    for (const r of rows) {
      if (!r.model?.trim()) continue
      out[r.model.trim()] = {
        input: Number(r.input) || 0,
        output: Number(r.output) || 0,
        cached_input: Number(r.cached) || 0,
      }
    }
    return out
  }

  async function saveProvider() {
    if (!pform.name.trim()) return toastErr('请填写名称')
    if (!pform.base_url.trim()) return toastErr('请填写 Base URL')
    if (!pform.api_key.trim()) return toastErr('请填写 API Key')
    savingProvider = true
    const body = { ...pform, model_prices: rowsToMap(priceRows) }
    try {
      if (editing) {
        await put(`/api/providers/${editing.id}`, body)
        toastOk('已保存')
      } else {
        await post(`/api/llm-apis/${apiId}/providers`, body)
        toastOk('Provider 已创建')
      }
      showProviderModal = false
      load()
    } catch (e) {
      toastErr(e.message)
    } finally {
      savingProvider = false
    }
  }

  // 编辑中的 DeepSeek Provider 一键填官方价目（可选高峰/空闲）
  async function autofillPrice() {
    if (!editing) return
    try {
      const d = await post(`/api/providers/${editing.id}/autofill-price`, { tier: autofillTier })
      pform.price_input = d.provider.price_input
      pform.price_output = d.provider.price_output
      pform.price_cached_input = d.provider.price_cached_input
      priceRows = Object.entries(d.provider.model_prices || {}).map(([model, v]) => ({
        model, input: v.input, output: v.output, cached: v.cached_input,
      }))
      toastOk(d.note)
      load()
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function savePriority(p, value) {
    const v = Math.max(0, Number(value) || 0)
    if (v === p.priority) return
    try {
      await put(`/api/providers/${p.id}`, providerToBody(p, { priority: v }))
      toastOk(`「${p.name}」优先级已设为 ${v}`)
      load()
    } catch (e) {
      toastErr(e.message)
      load()
    }
  }

  function providerToBody(p, overrides = {}) {
    return {
      name: p.name, kind: p.kind, base_url: p.base_url, api_key: p.api_key,
      priority: p.priority, enabled: p.enabled, supports_responses: p.supports_responses,
      inject_stream_usage: p.inject_stream_usage, timeout_seconds: p.timeout_seconds,
      max_body_mb: p.max_body_mb, max_failures: p.max_failures, cooldown_seconds: p.cooldown_seconds,
      price_input: p.price_input, price_output: p.price_output, price_cached_input: p.price_cached_input,
      model_prices: p.model_prices || {},
      ...overrides,
    }
  }

  // ---------- 拖拽排序
  let dragIndex = $state(-1)
  let overIndex = $state(-1)

  function onDragStart(i, e) {
    dragIndex = i
    e.dataTransfer.effectAllowed = 'move'
  }

  function onDragOver(i, e) {
    e.preventDefault()
    overIndex = i
  }

  async function onDrop(i, e) {
    e.preventDefault()
    const from = dragIndex
    dragIndex = -1
    overIndex = -1
    if (from === -1 || from === i) return
    const arr = [...providers]
    const [moved] = arr.splice(from, 1)
    arr.splice(i, 0, moved)
    providers = arr // 乐观更新
    try {
      const d = await post(`/api/llm-apis/${apiId}/providers/reorder`, { ids: arr.map((p) => p.id) })
      providers = d.providers
      toastOk('优先级顺序已更新')
    } catch (e2) {
      toastErr(e2.message)
      load()
    }
  }

  // ---------- Provider 操作
  let busyId = $state(0)

  async function resetHealth(p) {
    busyId = p.id
    try {
      await post(`/api/providers/${p.id}/reset-health`)
      toastOk(`「${p.name}」已恢复`)
      load()
    } catch (e) {
      toastErr(e.message)
    } finally {
      busyId = 0
    }
  }

  async function queryBalance(p) {
    busyId = p.id
    try {
      const d = await post(`/api/providers/${p.id}/balance`)
      providers = providers.map((x) => (x.id === p.id ? d.provider : x))
      toastOk('余额已更新')
    } catch (e) {
      toastErr(e.message)
    } finally {
      busyId = 0
    }
  }

  async function ping(p) {
    busyId = p.id
    try {
      const d = await post(`/api/providers/${p.id}/ping`)
      if (d.ok) toastOk(`连通正常（${d.latency_ms}ms）`)
      else toastErr(`连通失败：${d.error || 'HTTP ' + d.status}`)
    } catch (e) {
      toastErr(e.message)
    } finally {
      busyId = 0
    }
  }

  async function removeProvider(p) {
    if (!confirm(`确定删除 Provider「${p.name}」？`)) return
    try {
      await del(`/api/providers/${p.id}`)
      toastOk('已删除')
      load()
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function toggleProvider(p) {
    try {
      await put(`/api/providers/${p.id}`, providerToBody(p, { enabled: !p.enabled }))
      load()
    } catch (e) {
      toastErr(e.message)
    }
  }

  // ---------- 调用方 / 钱包
  let clients = $state([])
  let showClientModal = $state(false)
  let editingClient = $state(null)
  let cform = $state({ name: '', description: '', api_key: '', wallet_enabled: true, limit_daily: '', limit_weekly: '', limit_monthly: '' })
  let savingClient = $state(false)
  let rechargeTarget = $state(null)
  let rechargeAmount = $state('10')

  async function loadClients() {
    try {
      const d = await get(`/api/llm-apis/${apiId}/clients`)
      clients = d.clients
    } catch (e) {
      toastErr(e.message)
    }
  }

  function openCreateClient() {
    editingClient = null
    cform = { name: '', description: '', api_key: '', wallet_enabled: true, limit_daily: '', limit_weekly: '', limit_monthly: '' }
    showClientModal = true
  }

  function openEditClient(c) {
    editingClient = c
    cform = {
      name: c.name,
      description: c.description,
      api_key: '',
      wallet_enabled: c.wallet_enabled,
      limit_daily: c.limit_daily ?? '',
      limit_weekly: c.limit_weekly ?? '',
      limit_monthly: c.limit_monthly ?? '',
    }
    showClientModal = true
  }

  function limitVal(v) {
    if (v === '' || v === null || v === undefined) return null
    const n = Number(v)
    return Number.isFinite(n) && n >= 0 ? n : null
  }

  async function saveClient() {
    if (!cform.name.trim()) return toastErr('请填写名称')
    savingClient = true
    const body = {
      name: cform.name.trim(),
      description: cform.description.trim(),
      api_key: cform.api_key.trim(),
      wallet_enabled: cform.wallet_enabled,
      limit_daily: limitVal(cform.limit_daily),
      limit_weekly: limitVal(cform.limit_weekly),
      limit_monthly: limitVal(cform.limit_monthly),
    }
    try {
      if (editingClient) {
        await put(`/api/clients/${editingClient.id}`, body)
        toastOk('已保存')
      } else {
        await post(`/api/llm-apis/${apiId}/clients`, body)
        toastOk('调用方已创建，API Key 已生成')
      }
      showClientModal = false
      loadClients()
    } catch (e) {
      toastErr(e.message)
    } finally {
      savingClient = false
    }
  }

  async function toggleClient(c) {
    try {
      await put(`/api/clients/${c.id}`, { name: c.name, enabled: !c.enabled })
      loadClients()
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function rotateClientKey(c) {
    if (!confirm(`重置后旧 Key 立即失效。确定重置「${c.name}」的 Key？`)) return
    try {
      await post(`/api/clients/${c.id}/rotate-key`)
      toastOk('Key 已重置')
      loadClients()
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function removeClient(c) {
    if (!confirm(`确定删除调用方「${c.name}」？其 Key 立即失效。`)) return
    try {
      await del(`/api/clients/${c.id}`)
      toastOk('已删除')
      loadClients()
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function doRecharge() {
    const amount = Number(rechargeAmount)
    if (!Number.isFinite(amount) || amount === 0) return toastErr('请输入有效金额')
    try {
      await post(`/api/clients/${rechargeTarget.id}/recharge`, { amount })
      toastOk(amount > 0 ? `已充值 ${formatCost(amount)}` : `已扣减 ${formatCost(-amount)}`)
      rechargeTarget = null
      rechargeAmount = '10'
      loadClients()
    } catch (e) {
      toastErr(e.message)
    }
  }

  function clientState(c) {
    if (!c.enabled) return { label: '已禁用', cls: 'badge-neutral' }
    if (c.wallet_enabled && c.wallet_balance <= 0) return { label: '余额不足', cls: 'badge-error' }
    if (c.limit_daily != null && c.spent_day >= c.limit_daily) return { label: '超日限额', cls: 'badge-warning' }
    if (c.limit_weekly != null && c.spent_week >= c.limit_weekly) return { label: '超周限额', cls: 'badge-warning' }
    if (c.limit_monthly != null && c.spent_month >= c.limit_monthly) return { label: '超月限额', cls: 'badge-warning' }
    return { label: '正常', cls: 'badge-success' }
  }

  function limitRatio(spent, limit) {
    if (limit == null || limit <= 0) return 0
    return Math.min(spent / limit, 1)
  }

  function balanceText(p) {
    const infos = p.last_balance?.balance_infos
    if (!infos?.length) return null
    return infos.map((b) => `${b.currency} ${b.total_balance}`).join(' / ')
  }

  const kindLabel = { openai: 'OpenAI 兼容', deepseek: 'DeepSeek', custom: '自定义' }
</script>

{#if error}
  <div class="card p-6 text-sm text-error">{error}</div>
{:else if !api}
  <div class="text-sm text-muted/80">加载中…</div>
{:else}
  <div class="space-y-6">
    <!-- 头部 -->
    <div class="flex items-start justify-between gap-4">
      <div>
        <div class="flex items-center gap-3">
          <h2 class="text-xl font-bold text-ink">{api.name}</h2>
          {#if api.enabled}
            <span class="badge badge-success">启用</span>
          {:else}
            <span class="badge badge-neutral">停用</span>
          {/if}
          {#if api.supports_vision}<span class="badge badge-info">图片</span>{/if}
          {#if api.supports_video}<span class="badge badge-accent">视频</span>{/if}
        </div>
        {#if api.description}<p class="mt-1 text-sm text-muted">{api.description}</p>{/if}
      </div>
      {#if canWrite()}
        <div class="flex gap-2">
          <button class="btn btn-secondary" onclick={openApiModal}>编辑设置</button>
          <button class="btn btn-danger" onclick={removeApi}>删除</button>
        </div>
      {/if}
    </div>

    <div class="grid gap-4 xl:grid-cols-3">
      <!-- API Key -->
      <div class="card p-4 xl:col-span-2">
        <div class="mb-2 flex items-center justify-between">
          <div class="text-sm font-medium text-body">对外凭证</div>
          <div class="flex gap-2">
            <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => (showKey = !showKey)}>
              {showKey ? '隐藏' : '显示'}
            </button>
            <button
              class="btn btn-secondary !px-2 !py-1 text-xs"
              onclick={() => copyText(api.api_key).then(() => toastOk('已复制 API Key'))}
            >复制</button>
            {#if canWrite()}
              <button class="btn btn-danger !px-2 !py-1 text-xs" onclick={rotateKey}>重置</button>
            {/if}
          </div>
        </div>
        <code class="block rounded-lg bg-surface px-3 py-2 font-mono text-xs break-all text-body ring-1 ring-line">
          {showKey ? api.api_key : api.api_key.slice(0, 10) + '••••••••••••••••' + api.api_key.slice(-4)}
        </code>
        <div class="mt-3 text-xs text-muted">调用示例：</div>
        <code class="mt-1 block rounded-lg bg-deep-panel px-3 py-2 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-[#d9d9e3]">
curl {location.origin}/v1/chat/completions \
  -H "Authorization: Bearer {showKey ? api.api_key : 'sk-flr-...'}" \
  -H "Content-Type: application/json" \
  -d '&#123;"model":"deepseek-flash","messages":[&#123;"role":"user","content":"你好"&#125;]&#125;'</code>
      </div>

      <!-- 模型清单 -->
      <div class="card p-4">
        <div class="mb-2 flex items-center justify-between">
          <div class="text-sm font-medium text-body">模型清单（{api.models?.length || 0}）</div>
          {#if canWrite()}<button class="btn btn-ghost !px-2 !py-0.5 text-xs" onclick={openApiModal}>编辑</button>{/if}
        </div>
        {#if api.models?.length}
          <div class="flex flex-wrap gap-1">
            {#each api.models as m (m)}
              <span class="badge badge-neutral font-mono">{m}</span>
            {/each}
          </div>
        {:else}
          <p class="text-xs text-muted/80">未配置模型，对外 /v1/models 将返回空列表</p>
        {/if}
      </div>
    </div>

    <!-- Providers -->
    <div>
      <div class="mb-3 flex items-center justify-between">
        <div>
          <h3 class="text-base font-bold text-ink">Providers（{providers.length}）</h3>
          <p class="text-xs text-muted">按优先级从小到大依次尝试；失败自动切换下一个。可拖拽调整顺序，或直接修改数字。</p>
        </div>
        {#if canWrite()}
          <button class="btn btn-primary" onclick={openCreateProvider}>＋ 添加 Provider</button>
        {/if}
      </div>

      <div class="space-y-2">
        {#if providers.length === 0}
          <div class="card p-8 text-center text-sm text-muted/80">还没有 Provider。添加至少一个后服务才能转发请求。</div>
        {/if}
        {#each providers as p, i (p.id)}
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div
            class="card flex items-center gap-3 p-3 {overIndex === i && dragIndex !== -1 && dragIndex !== i ? 'ring-2 ring-accent' : ''}
                   {!p.enabled ? 'opacity-60' : ''} {dragIndex === i ? 'opacity-40' : ''}"
            draggable={canWrite()}
            ondragstart={(e) => onDragStart(i, e)}
            ondragover={(e) => onDragOver(i, e)}
            ondrop={(e) => onDrop(i, e)}
            ondragend={() => { dragIndex = -1; overIndex = -1 }}
          >
            <!-- 拖拽手柄 + 优先级 -->
            <div class="flex items-center gap-2">
              {#if canWrite()}
                <span class="cursor-grab text-[#c2c2cc] select-none hover:text-muted" title="拖拽调整顺序">⠿</span>
              {/if}
              <label class="text-[10px] text-muted/80" for="prio-{p.id}">优先级</label>
              <input
                id="prio-{p.id}"
                class="input !w-16 text-center font-mono text-xs"
                type="number"
                min="0"
                value={p.priority}
                disabled={!canWrite()}
                onchange={(e) => savePriority(p, e.target.value)}
                title="数字越小越优先"
              />
            </div>

            <!-- 名称与地址 -->
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="truncate font-medium text-ink">{p.name}</span>
                <span class="badge badge-neutral">{kindLabel[p.kind] || p.kind}</span>
                {#if p.supports_responses}<span class="badge badge-info">Responses</span>{/if}
                {#if !p.enabled}<span class="badge badge-neutral">已停用</span>{/if}
                {#if p.cooldown_until && cooldownRemaining(p.cooldown_until)}
                  <span class="badge badge-warning">熔断冷却中 · 剩 {cooldownRemaining(p.cooldown_until)}</span>
                {:else if p.consecutive_failures > 0}
                  <span class="badge badge-warning">连续失败 {p.consecutive_failures} 次</span>
                {:else if p.enabled}
                  <span class="badge badge-success">正常</span>
                {/if}
              </div>
              <div class="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted/80">
                <span class="font-mono">{p.base_url}</span>
                <span title="超时">⏱ {p.timeout_seconds}s</span>
                {#if p.max_body_mb > 0}<span title="请求体超限跳过">📦 &gt;{p.max_body_mb}MB 跳过</span>{/if}
                {#if p.max_failures > 0}<span title="连续失败熔断">⚡ {p.max_failures} 次失败熔断 {p.cooldown_seconds}s</span>{/if}
                {#if p.price_input > 0 || p.price_output > 0}
                  <span title="默认单价 / 百万 tokens">💰 ¥{p.price_input}/{p.price_output}</span>
                {/if}
                {#if p.kind === 'deepseek' && balanceText(p)}
                  <span class="text-success" title="余额（{timeAgo(p.balance_checked_at)}更新）">余额 {balanceText(p)}</span>
                {/if}
              </div>
              {#if p.last_error}
                <div class="mt-0.5 truncate text-xs text-error/80" title={p.last_error}>
                  最近错误：{p.last_error}
                </div>
              {/if}
            </div>

            <!-- 操作 -->
            <div class="flex shrink-0 items-center gap-1">
              {#if canWrite()}
                <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => ping(p)} disabled={busyId === p.id}>测试</button>
                {#if p.kind === 'deepseek'}
                  <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => queryBalance(p)} disabled={busyId === p.id}>余额</button>
                {/if}
                {#if p.cooldown_until || p.consecutive_failures > 0}
                  <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => resetHealth(p)} disabled={busyId === p.id}>恢复</button>
                {/if}
                <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => toggleProvider(p)}>{p.enabled ? '停用' : '启用'}</button>
                <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => openEditProvider(p)}>编辑</button>
                <button class="btn btn-danger !px-2 !py-1 text-xs" onclick={() => removeProvider(p)}>删除</button>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    </div>

    <!-- 调用方 / 钱包 -->
    <div>
      <div class="mb-3 flex items-center justify-between">
        <div>
          <h3 class="text-base font-bold text-ink">调用方 / 钱包（{clients.length}）</h3>
          <p class="text-xs text-muted">
            每个调用方持有独立 API Key，受钱包余额与日 / 周 / 月消费限额约束；上方「对外凭证」主 Key 不受限制。
          </p>
        </div>
        {#if canWrite()}
          <button class="btn btn-primary" onclick={openCreateClient}>＋ 添加调用方</button>
        {/if}
      </div>

      <div class="space-y-2">
        {#if clients.length === 0}
          <div class="card p-8 text-center text-sm text-muted">
            还没有调用方。添加后可为每个使用方发放独立 Key、充值余额并设置消费限额。
          </div>
        {/if}
        {#each clients as c (c.id)}
          {@const st = clientState(c)}
          <div class="card p-3 {c.enabled ? '' : 'opacity-60'}">
            <div class="flex flex-wrap items-center gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium text-ink">{c.name}</span>
                  <span class="badge {st.cls}">{st.label}</span>
                  <code class="font-mono text-[11px] text-muted">{c.api_key.slice(0, 10)}••••••••{c.api_key.slice(-4)}</code>
                  <button
                    class="btn btn-ghost !px-1.5 !py-0 text-[11px]"
                    onclick={() => copyText(c.api_key).then(() => toastOk('已复制 API Key'))}>复制</button
                  >
                </div>
                {#if c.description}<div class="mt-0.5 text-xs text-muted">{c.description}</div>{/if}
                <div class="mt-2 grid gap-2 sm:grid-cols-3">
                  {#each [{ k: '日', spent: c.spent_day, limit: c.limit_daily }, { k: '周', spent: c.spent_week, limit: c.limit_weekly }, { k: '月', spent: c.spent_month, limit: c.limit_monthly }] as row (row.k)}
                    <div>
                      <div class="flex items-center justify-between text-[11px]">
                        <span class="text-muted">{row.k}限额</span>
                        <span class="{row.limit != null && row.spent >= row.limit ? 'font-semibold text-error' : 'text-body'}">
                          {row.limit != null ? formatCost(row.spent) + ' / ' + formatCost(row.limit) : '不限'}
                        </span>
                      </div>
                      <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-surface-alt">
                        {#if row.limit != null}
                          <div
                            class="h-full rounded-full {row.spent >= row.limit ? 'bg-error' : row.spent >= row.limit * 0.8 ? 'bg-warning' : 'bg-accent'}"
                            style="width: {limitRatio(row.spent, row.limit) * 100}%"
                          ></div>
                        {/if}
                      </div>
                    </div>
                  {/each}
                </div>
              </div>

              <div class="flex shrink-0 items-center gap-4">
                <div class="text-right">
                  <div class="text-[10px] text-muted">钱包余额</div>
                  <div class="font-bold {c.wallet_enabled && c.wallet_balance <= 0 ? 'text-error' : 'text-ink'}">
                    {formatCost(c.wallet_balance)}
                  </div>
                  <div class="text-[10px] text-muted">累计消费 {formatCost(c.total_spent)}</div>
                </div>
                {#if canWrite()}
                  <div class="flex gap-1">
                    <button
                      class="btn btn-secondary !px-2 !py-1 text-xs"
                      onclick={() => {
                        rechargeTarget = c
                        rechargeAmount = '10'
                      }}>充值</button
                    >
                    <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => openEditClient(c)}>限额</button>
                    <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => toggleClient(c)}>{c.enabled ? '停用' : '启用'}</button>
                    <button class="btn btn-secondary !px-2 !py-1 text-xs" onclick={() => rotateClientKey(c)}>重置Key</button>
                    <button class="btn btn-danger !px-2 !py-1 text-xs" onclick={() => removeClient(c)}>删除</button>
                  </div>
                {/if}
              </div>
            </div>
          </div>
        {/each}
      </div>
    </div>
  </div>
{/if}

<!-- LLM API 编辑弹窗 -->
<Modal open={showApiModal} title="编辑 LLM API" onclose={() => (showApiModal = false)}>
  <div class="space-y-4">
    <div class="grid grid-cols-2 gap-4">
      <div>
        <label class="label" for="ae-name">名称 *</label>
        <input id="ae-name" class="input" bind:value={aform.name} />
      </div>
      <div>
        <label class="label" for="ae-enabled">状态</label>
        <select id="ae-enabled" class="input" bind:value={aform.enabled}>
          <option value={true}>启用</option>
          <option value={false}>停用（对外拒绝所有请求）</option>
        </select>
      </div>
    </div>
    <div>
      <label class="label" for="ae-desc">描述</label>
      <input id="ae-desc" class="input" bind:value={aform.description} />
    </div>
    <div>
      <label class="label" for="ae-models">模型清单（每行一个）</label>
      <textarea id="ae-models" class="input h-28 font-mono text-xs" bind:value={aform.modelsText}></textarea>
    </div>
    <div class="flex gap-6">
      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={aform.supports_vision} /> 支持图片
      </label>
      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={aform.supports_video} /> 支持视频
      </label>
    </div>
    <p class="text-xs text-muted/80">图片/视频为能力声明，用于对外展示与文档；不会被用于拦截请求。</p>
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (showApiModal = false)}>取消</button>
    <button class="btn btn-primary" disabled={savingApi} onclick={saveApi}>保存</button>
  {/snippet}
</Modal>

<!-- Provider 编辑弹窗 -->
<Modal open={showProviderModal} wide title={editing ? `编辑 Provider：${editing.name}` : '添加 Provider'} onclose={() => (showProviderModal = false)}>
  <div class="space-y-5">
    <!-- 基本信息 -->
    <div class="grid grid-cols-2 gap-4">
      <div>
        <label class="label" for="pf-name">名称 *</label>
        <input id="pf-name" class="input" bind:value={pform.name} placeholder="例如：DeepSeek 官方" />
      </div>
      <div>
        <label class="label" for="pf-kind">类型</label>
        <select id="pf-kind" class="input" bind:value={pform.kind}>
          <option value="openai">OpenAI 兼容</option>
          <option value="deepseek">DeepSeek（支持余额查询与自动填价）</option>
          <option value="custom">自定义</option>
        </select>
      </div>
      <div class="col-span-2 grid grid-cols-2 gap-4">
        <div>
          <label class="label" for="pf-url">Base URL *（无需带 /v1）</label>
          <input id="pf-url" class="input font-mono text-xs" bind:value={pform.base_url} placeholder="https://api.deepseek.com" />
        </div>
        <div>
          <label class="label" for="pf-key">API Key *</label>
          <input id="pf-key" class="input font-mono text-xs" type="password" bind:value={pform.api_key} />
        </div>
      </div>
      <div>
        <label class="label" for="pf-prio">优先级（数字越小越优先）</label>
        <input id="pf-prio" class="input" type="number" min="0" bind:value={pform.priority} />
      </div>
      <div class="flex items-end gap-6 pb-1">
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={pform.enabled} /> 启用
        </label>
        <label class="flex items-center gap-2 text-sm" title="仅直通：未勾选则本 Provider 不接收 /v1/responses 流量">
          <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={pform.supports_responses} /> 原生支持 Responses API
        </label>
        <label class="flex items-center gap-2 text-sm" title="流式请求自动注入 stream_options.include_usage 以获得计费数据">
          <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={pform.inject_stream_usage} /> 流式计费注入
        </label>
      </div>
    </div>

    <!-- 熔断与超时 -->
    <fieldset class="rounded-xl border border-line p-4">
      <legend class="px-1 text-xs font-medium text-muted">熔断 / 超时（失败判定：任何非 2xx 或网络错误）</legend>
      <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
        <div>
          <label class="label" for="pf-timeout">超时（秒）</label>
          <input id="pf-timeout" class="input" type="number" min="1" bind:value={pform.timeout_seconds} />
          <p class="mt-1 text-[10px] leading-tight text-muted/80">非流式=总超时；流式=连接+首字节</p>
        </div>
        <div>
          <label class="label" for="pf-body">请求体上限（MB）</label>
          <input id="pf-body" class="input" type="number" min="0" step="0.1" bind:value={pform.max_body_mb} />
          <p class="mt-1 text-[10px] leading-tight text-muted/80">超过则本次跳过此 Provider，0=不限</p>
        </div>
        <div>
          <label class="label" for="pf-fail">连续失败熔断（次）</label>
          <input id="pf-fail" class="input" type="number" min="0" bind:value={pform.max_failures} />
          <p class="mt-1 text-[10px] leading-tight text-muted/80">0=不熔断，成功一次计数清零</p>
        </div>
        <div>
          <label class="label" for="pf-cooldown">冷却时长（秒）</label>
          <input id="pf-cooldown" class="input" type="number" min="1" bind:value={pform.cooldown_seconds} />
          <p class="mt-1 text-[10px] leading-tight text-muted/80">熔断后暂停多久再参与调度</p>
        </div>
      </div>
    </fieldset>

    <!-- 价格 -->
    <fieldset class="rounded-xl border border-line p-4">
      <legend class="px-1 text-xs font-medium text-muted">价格（CNY / 百万 tokens，用于计费）</legend>
      <div class="grid grid-cols-3 gap-4">
        <div>
          <label class="label" for="pf-pin">输入（缓存未命中）</label>
          <input id="pf-pin" class="input" type="number" min="0" step="0.01" bind:value={pform.price_input} />
        </div>
        <div>
          <label class="label" for="pf-pout">输出</label>
          <input id="pf-pout" class="input" type="number" min="0" step="0.01" bind:value={pform.price_output} />
        </div>
        <div>
          <label class="label" for="pf-pcache">输入（缓存命中）</label>
          <input id="pf-pcache" class="input" type="number" min="0" step="0.01" bind:value={pform.price_cached_input} />
        </div>
      </div>

      <div class="mt-4">
        <div class="mb-1 flex items-center justify-between">
          <span class="label !mb-0">按模型覆盖单价（可选）</span>
          <button class="btn btn-secondary !px-2 !py-0.5 text-xs" onclick={() => priceRows.push({ model: '', input: 0, output: 0, cached: 0 })}>＋ 添加模型价</button>
        </div>
        {#if priceRows.length === 0}
          <p class="text-[11px] text-muted/80">未配置时使用上方默认单价</p>
        {/if}
        {#each priceRows as row, i (i)}
          <div class="mt-1.5 grid grid-cols-[1fr_auto_auto_auto_auto] items-center gap-2">
            <input class="input font-mono text-xs" placeholder="模型名，如 deepseek-v4-pro" bind:value={row.model} />
            <input class="input !w-24 text-xs" type="number" min="0" step="0.01" placeholder="输入" bind:value={row.input} />
            <input class="input !w-24 text-xs" type="number" min="0" step="0.01" placeholder="输出" bind:value={row.output} />
            <input class="input !w-24 text-xs" type="number" min="0" step="0.01" placeholder="缓存" bind:value={row.cached} />
            <button class="btn btn-ghost !px-1.5 text-error" onclick={() => priceRows.splice(i, 1)}>✕</button>
          </div>
        {/each}
      </div>

      {#if editing && pform.kind === 'deepseek'}
        <div class="mt-4 flex items-center gap-2 rounded-lg bg-pale-blue p-3 ring-1 ring-[#cce0f5]">
          <span class="text-xs text-[#0b4f8a]">DeepSeek 一键填入官方价目：</span>
          <select class="input !w-32 !py-1 text-xs" bind:value={autofillTier}>
            <option value="peak">高峰时段价</option>
            <option value="offpeak">空闲时段价（半价）</option>
          </select>
          <button class="btn btn-secondary !px-2.5 !py-1 text-xs" onclick={autofillPrice}>自动配置价格</button>
          <span class="text-[10px] text-link">空闲时段：工作日 9-12/14-18 之外（官方规则）</span>
        </div>
      {/if}
    </fieldset>
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (showProviderModal = false)}>取消</button>
    <button class="btn btn-primary" disabled={savingProvider} onclick={saveProvider}>{savingProvider ? '保存中…' : '保存'}</button>
  {/snippet}
</Modal>

<!-- 调用方编辑弹窗 -->
<Modal open={showClientModal} title={editingClient ? `编辑调用方：${editingClient.name}` : '添加调用方'} onclose={() => (showClientModal = false)}>
  <div class="space-y-4">
    <div class="grid grid-cols-2 gap-4">
      <div>
        <label class="label" for="cf-name">名称 *</label>
        <input id="cf-name" class="input" bind:value={cform.name} placeholder="例如：算法团队" />
      </div>
      <div>
        <label class="label" for="cf-key">API Key（可选，留空自动生成）</label>
        <input id="cf-key" class="input font-mono text-xs" bind:value={cform.api_key} placeholder="sk-flr-…" />
      </div>
    </div>
    <div>
      <label class="label" for="cf-desc">备注</label>
      <input id="cf-desc" class="input" bind:value={cform.description} placeholder="可选项" />
    </div>
    <label class="flex items-center gap-2 text-sm">
      <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={cform.wallet_enabled} />
      启用钱包（余额 ≤ 0 时拒绝该调用方的请求）
    </label>
    <fieldset class="rounded-xl border border-line p-4">
      <legend class="px-1 text-xs font-medium text-muted">消费限额（CNY，留空 = 不限；按统计时区自然日 / 周 / 月计算）</legend>
      <div class="grid grid-cols-3 gap-4">
        <div>
          <label class="label" for="cf-daily">每日限额</label>
          <input id="cf-daily" class="input" type="number" min="0" step="0.01" bind:value={cform.limit_daily} placeholder="不限" />
        </div>
        <div>
          <label class="label" for="cf-weekly">每周限额</label>
          <input id="cf-weekly" class="input" type="number" min="0" step="0.01" bind:value={cform.limit_weekly} placeholder="不限" />
        </div>
        <div>
          <label class="label" for="cf-monthly">每月限额</label>
          <input id="cf-monthly" class="input" type="number" min="0" step="0.01" bind:value={cform.limit_monthly} placeholder="不限" />
        </div>
      </div>
      <p class="mt-2 text-[11px] text-muted">超额后返回 429（如 daily_limit_exceeded），下个周期自动恢复。</p>
    </fieldset>
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (showClientModal = false)}>取消</button>
    <button class="btn btn-primary" disabled={savingClient} onclick={saveClient}>{savingClient ? '保存中…' : '保存'}</button>
  {/snippet}
</Modal>

<!-- 充值弹窗 -->
<Modal open={!!rechargeTarget} title={`充值 / 调整余额：${rechargeTarget?.name || ''}`} onclose={() => (rechargeTarget = null)}>
  <div>
    <label class="label" for="rc-amount">金额（CNY，正数充值，负数扣减）</label>
    <input id="rc-amount" class="input" type="number" step="0.01" bind:value={rechargeAmount} />
    <div class="mt-2 flex items-center justify-between rounded-xl bg-surface px-3 py-2 text-sm">
      <span class="text-muted">当前余额</span>
      <span class="font-bold">{formatCost(rechargeTarget?.wallet_balance ?? 0)}</span>
    </div>
    {#if !rechargeTarget?.wallet_enabled}
      <p class="mt-2 text-[11px] text-warning">该调用方未启用钱包限制，余额变化不影响其可用性。</p>
    {/if}
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (rechargeTarget = null)}>取消</button>
    <button class="btn btn-primary" onclick={doRecharge}>确认</button>
  {/snippet}
</Modal>
