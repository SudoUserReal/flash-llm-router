<script>
  import { get, post, patch, del } from '../lib/api.js'
  import { session } from '../lib/session.svelte.js'
  import { toastOk, toastErr } from '../lib/toast.svelte.js'
  import { formatDateTime } from '../lib/format.js'
  import Modal from '../components/Modal.svelte'

  let users = $state([])

  let showCreate = $state(false)
  let form = $state({ username: '', password: '', role: 'viewer' })
  let saving = $state(false)

  let pwdTarget = $state(null)
  let newPassword = $state('')

  async function load() {
    try {
      const d = await get('/api/users')
      users = d.users
    } catch (e) {
      toastErr(e.message)
    }
  }
  load()

  async function create() {
    saving = true
    try {
      await post('/api/users', { username: form.username.trim(), password: form.password, role: form.role })
      toastOk('用户已创建')
      showCreate = false
      form = { username: '', password: '', role: 'viewer' }
      load()
    } catch (e) {
      toastErr(e.message)
    } finally {
      saving = false
    }
  }

  async function setRole(u, role) {
    try {
      await patch(`/api/users/${u.id}`, { role })
      toastOk('角色已更新')
      load()
    } catch (e) {
      toastErr(e.message)
      load()
    }
  }

  async function toggleEnabled(u) {
    try {
      await patch(`/api/users/${u.id}`, { enabled: !u.enabled })
      toastOk(u.enabled ? '已禁用' : '已启用')
      load()
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function resetPassword() {
    try {
      await patch(`/api/users/${pwdTarget.id}`, { password: newPassword })
      toastOk('密码已重置（该用户旧会话已失效）')
      pwdTarget = null
      newPassword = ''
    } catch (e) {
      toastErr(e.message)
    }
  }

  async function remove(u) {
    if (!confirm(`确定删除用户「${u.username}」？`)) return
    try {
      await del(`/api/users/${u.id}`)
      toastOk('已删除')
      load()
    } catch (e) {
      toastErr(e.message)
    }
  }

  const roleLabel = { admin: '管理员', operator: '运维', viewer: '只读' }
</script>

<div class="space-y-4">
  <div class="flex items-center justify-between">
    <p class="text-sm text-muted">
      权限：<b>管理员</b>可管理一切；<b>运维</b>可管理 LLM API / Provider；<b>只读</b>仅可查看。
    </p>
    <button class="btn btn-primary" onclick={() => (showCreate = true)}>＋ 新建用户</button>
  </div>

  <div class="card overflow-hidden">
    <table class="w-full">
      <thead class="border-b border-line bg-surface/70">
        <tr>
          <th class="table-th">用户名</th>
          <th class="table-th">角色</th>
          <th class="table-th">状态</th>
          <th class="table-th">最后登录</th>
          <th class="table-th">创建时间</th>
          <th class="table-th text-right">操作</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-slate-100">
        {#each users as u (u.id)}
          <tr class="hover:bg-surface/60">
            <td class="table-td font-medium">
              {u.username}
              {#if u.id === session.user?.id}<span class="ml-1 text-xs text-muted/80">（我）</span>{/if}
            </td>
            <td class="table-td">
              <select
                class="input !w-28 !py-1 text-xs"
                value={u.role}
                onchange={(e) => setRole(u, e.target.value)}
                disabled={u.id === session.user?.id}
              >
                <option value="admin">{roleLabel.admin}</option>
                <option value="operator">{roleLabel.operator}</option>
                <option value="viewer">{roleLabel.viewer}</option>
              </select>
            </td>
            <td class="table-td">
              {#if u.enabled}
                <span class="badge badge-success">启用</span>
              {:else}
                <span class="badge badge-neutral">已禁用</span>
              {/if}
            </td>
            <td class="table-td text-xs text-muted">{u.last_login_at ? formatDateTime(u.last_login_at) : '从未登录'}</td>
            <td class="table-td text-xs text-muted/80">{formatDateTime(u.created_at)}</td>
            <td class="table-td">
              <div class="flex justify-end gap-1">
                <button class="btn btn-secondary !px-2.5 !py-1 text-xs" onclick={() => (pwdTarget = u)}>重置密码</button>
                <button class="btn btn-secondary !px-2.5 !py-1 text-xs" disabled={u.id === session.user?.id} onclick={() => toggleEnabled(u)}>
                  {u.enabled ? '禁用' : '启用'}
                </button>
                <button class="btn btn-danger !px-2.5 !py-1 text-xs" disabled={u.id === session.user?.id} onclick={() => remove(u)}>删除</button>
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<Modal open={showCreate} title="新建用户" onclose={() => (showCreate = false)}>
  <div class="space-y-4">
    <div>
      <label class="label" for="u-name">用户名 *</label>
      <input id="u-name" class="input" bind:value={form.username} />
    </div>
    <div>
      <label class="label" for="u-pwd">密码 *（至少 8 位，含字母和数字）</label>
      <input id="u-pwd" class="input" type="password" bind:value={form.password} />
    </div>
    <div>
      <label class="label" for="u-role">角色</label>
      <select id="u-role" class="input" bind:value={form.role}>
        <option value="viewer">只读</option>
        <option value="operator">运维</option>
        <option value="admin">管理员</option>
      </select>
    </div>
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (showCreate = false)}>取消</button>
    <button class="btn btn-primary" disabled={saving} onclick={create}>创建</button>
  {/snippet}
</Modal>

<Modal open={!!pwdTarget} title={`重置密码：${pwdTarget?.username || ''}`} onclose={() => (pwdTarget = null)}>
  <div>
    <label class="label" for="rp-pwd">新密码（至少 8 位，含字母和数字）</label>
    <input id="rp-pwd" class="input" type="password" bind:value={newPassword} />
    <p class="mt-2 text-xs text-muted/80">重置后该用户所有会话立即失效。</p>
  </div>
  {#snippet footer()}
    <button class="btn btn-secondary" onclick={() => (pwdTarget = null)}>取消</button>
    <button class="btn btn-primary" onclick={resetPassword}>重置</button>
  {/snippet}
</Modal>
