<script>
  import { get, post, del } from '../lib/api.js'
  import { navigate } from '../lib/router.svelte.js'
  import { canWrite } from '../lib/session.svelte.js'
  import { toastOk, toastErr } from '../lib/toast.svelte.js'
  import { formatDateTime } from '../lib/format.js'
  import Modal from '../components/Modal.svelte'

  let apis = $state([])
  let loading = $state(true)

  // 新建表单
  let showCreate = $state(false)
  let form = $state({ name: '', description: '', modelsText: '', api_key: '', supports_vision: true, supports_video: false })
  let saving = $state(false)

  async function load() {
    loading = true
    try {
      const d = await get('/api/llm-apis')
      apis = d.llm_apis
    } catch (e) {
      toastErr(e.message)
    } finally {
      loading = false
    }
  }
  load()

  function openCreate() {
    form = { name: '', description: '', modelsText: '', api_key: '', supports_vision: true, supports_video: false }
    showCreate = true
  }

  async function create() {
    if (!form.name.trim()) return toastErr('请填写名称')
    saving = true
    try {
      const created = await post('/api/llm-apis', {
        name: form.name.trim(),
        description: form.description.trim(),
        api_key: form.api_key.trim(),
        models: form.modelsText
          .split(/[\n,，]/)
          .map((s) => s.trim())
          .filter(Boolean),
        supports_vision: form.supports_vision,
        supports_video: form.supports_video,
      })
      toastOk('创建成功，API Key 已生成')
      showCreate = false
      navigate(`#/apis/${created.id}`)
    } catch (e) {
      toastErr(e.message)
    } finally {
      saving = false
    }
  }

  async function remove(api) {
    if (!confirm(`确定删除「${api.name}」？其 Providers 与配置将一并删除，请求日志保留。`)) return
    try {
      await del(`/api/llm-apis/${api.id}`)
      toastOk('已删除')
      load()
    } catch (e) {
      toastErr(e.message)
    }
  }
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between">
    <p class="text-sm text-muted">
      每个 LLM API 是对外服务实体：拥有独立 API Key，可挂多个 Provider 并按优先级自动故障切换。
    </p>
    {#if canWrite()}
      <button class="btn btn-primary" onclick={openCreate}>＋ 新建 LLM API</button>
    {/if}
  </div>

  <div class="card overflow-hidden">
    <table class="w-full">
      <thead class="border-b border-line bg-surface/70">
        <tr>
          <th class="table-th">名称</th>
          <th class="table-th">模型</th>
          <th class="table-th">Provider</th>
          <th class="table-th">能力</th>
          <th class="table-th">状态</th>
          <th class="table-th">创建时间</th>
          <th class="table-th text-right">操作</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-slate-100">
        {#if loading}
          <tr><td class="table-td text-muted/80" colspan="7">加载中…</td></tr>
        {:else if apis.length === 0}
          <tr><td class="table-td text-muted/80" colspan="7">暂无数据，点击右上角按钮创建</td></tr>
        {/if}
        {#each apis as api (api.id)}
          <tr class="hover:bg-surface/60">
            <td class="table-td">
              <button class="font-medium text-accent hover:underline" onclick={() => navigate(`#/apis/${api.id}`)}>
                {api.name}
              </button>
              {#if api.description}<div class="mt-0.5 max-w-xs truncate text-xs text-muted/80">{api.description}</div>{/if}
            </td>
            <td class="table-td text-body/70">{api.models?.length || 0}</td>
            <td class="table-td text-body/70">{api.provider_count}</td>
            <td class="table-td">
              <div class="flex gap-1">
                {#if api.supports_vision}<span class="badge badge-info">图片</span>{/if}
                {#if api.supports_video}<span class="badge badge-accent">视频</span>{/if}
                {#if !api.supports_vision && !api.supports_video}<span class="text-xs text-muted/80">纯文本</span>{/if}
              </div>
            </td>
            <td class="table-td">
              {#if api.enabled}
                <span class="badge badge-success">启用</span>
              {:else}
                <span class="badge badge-neutral">停用</span>
              {/if}
            </td>
            <td class="table-td text-xs text-muted/80">{formatDateTime(api.created_at)}</td>
            <td class="table-td">
              <div class="flex justify-end gap-1">
                <button class="btn btn-secondary !px-2.5 !py-1 text-xs" onclick={() => navigate(`#/apis/${api.id}`)}>管理</button>
                {#if canWrite()}
                  <button class="btn btn-danger !px-2.5 !py-1 text-xs" onclick={() => remove(api)}>删除</button>
                {/if}
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<Modal open={showCreate} title="新建 LLM API" onclose={() => (showCreate = false)}>
  <div class="space-y-4">
    <div>
      <label class="label" for="api-name">名称 *</label>
      <input id="api-name" class="input" bind:value={form.name} placeholder="例如：生产环境对话服务" />
    </div>
    <div>
      <label class="label" for="api-desc">描述</label>
      <input id="api-desc" class="input" bind:value={form.description} placeholder="可选项" />
    </div>
    <div>
      <label class="label" for="api-key">API Key（可选，留空自动生成）</label>
      <input id="api-key" class="input font-mono text-xs" bind:value={form.api_key} placeholder="sk-flr-…（留空自动生成）" />
    </div>
    <div>
      <label class="label" for="api-models">模型清单（每行一个，用于对外 /v1/models 与文档展示）</label>
      <textarea
        id="api-models"
        class="input h-24 font-mono text-xs"
        bind:value={form.modelsText}
        placeholder={'deepseek-flash\ndeepseek-v4-pro'}
      ></textarea>
    </div>
    <div class="flex gap-6">
      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={form.supports_vision} />
        支持图片
      </label>
      <label class="flex items-center gap-2 text-sm">
        <input type="checkbox" class="h-4 w-4 accent-[#7612fa]" bind:checked={form.supports_video} />
        支持视频
      </label>
    </div>
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (showCreate = false)}>取消</button>
    <button class="btn btn-primary" disabled={saving} onclick={create}>{saving ? '创建中…' : '创建'}</button>
  {/snippet}
</Modal>
