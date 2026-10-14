<script>
  import { router, parseRoute, navigate } from './lib/router.svelte.js'
  import { session, setUser, clearSession, isAdmin } from './lib/session.svelte.js'
  import { get, post } from './lib/api.js'
  import Toasts from './components/Toasts.svelte'
  import Logo from './components/Logo.svelte'
  import Login from './pages/Login.svelte'
  import Dashboard from './pages/Dashboard.svelte'
  import APIList from './pages/APIList.svelte'
  import APIDetail from './pages/APIDetail.svelte'
  import Stats from './pages/Stats.svelte'
  import Logs from './pages/Logs.svelte'
  import Users from './pages/Users.svelte'
  import Help from './pages/Help.svelte'

  const route = $derived(parseRoute(router.hash))

  // 启动时校验已保存的 token
  $effect(() => {
    if (session.token && !session.ready) {
      get('/api/auth/me')
        .then((u) => setUser(u))
        .catch(() => clearSession())
        .finally(() => (session.ready = true))
    }
  })

  const navItems = $derived([
    { href: '#/', label: '总览', icon: '▦', match: 'dashboard' },
    { href: '#/apis', label: 'LLM API', icon: '◆', match: ['apis', 'api-detail'] },
    { href: '#/stats', label: '统计分析', icon: '◔', match: 'stats' },
    { href: '#/logs', label: '请求日志', icon: '≣', match: 'logs' },
    ...(isAdmin() ? [{ href: '#/users', label: '用户管理', icon: '◉', match: 'users' }] : []),
    { href: '#/help', label: '帮助文档', icon: '?', match: 'help' },
  ])

  const activeNav = (m) => (Array.isArray(m) ? m.includes(route.name) : m === route.name)

  const title = $derived(
    {
      dashboard: '总览',
      apis: 'LLM API',
      'api-detail': 'LLM API 详情',
      stats: '统计分析',
      logs: '请求日志',
      users: '用户管理',
      help: 'API 帮助文档',
    }[route.name] || '',
  )

  async function logout() {
    try {
      await post('/api/auth/logout')
    } catch {
      /* ignore */
    }
    clearSession()
    navigate('#/login')
  }

  const roleLabel = { admin: '管理员', operator: '运维', viewer: '只读' }
</script>

{#if !session.token || !session.ready}
  {#if !session.token}
    <Login />
  {:else}
    <div class="flex min-h-screen items-center justify-center text-muted">正在加载…</div>
  {/if}
{:else}
  <div class="flex min-h-screen">
    <!-- 侧边栏 -->
    <aside class="fixed inset-y-0 left-0 z-20 flex w-56 flex-col border-r border-line bg-canvas">
      <div class="flex items-center gap-2.5 px-5 py-5">
        <Logo size={32} />
        <div class="leading-tight">
          <div class="text-sm font-extrabold text-ink" style="font-family: var(--font-display)">flash-llm-router</div>
          <div class="text-[11px] text-muted">LLM API 中转网关</div>
        </div>
      </div>
      <nav class="mt-2 flex-1 space-y-1 px-3">
        {#each navItems as item (item.href)}
          <a
            href={item.href}
            class="flex items-center gap-2.5 rounded-[14px] px-3 py-2 text-sm transition-colors {activeNav(item.match)
              ? 'bg-pale-purple font-semibold text-ink'
              : 'text-muted hover:bg-surface hover:text-body'}"
          >
            <span class="w-4 text-center text-xs {activeNav(item.match) ? 'text-accent' : 'opacity-60'}">{item.icon}</span>
            {item.label}
          </a>
        {/each}
      </nav>
      <div class="border-t border-line p-4">
        <div class="mb-2 flex items-center gap-2">
          <div
            class="flex h-7 w-7 items-center justify-center rounded-full text-xs font-bold text-white"
            style="background: linear-gradient(135deg, #6647f0 0%, #0091ff 100%)"
          >
            {session.user?.username?.[0]?.toUpperCase() || '?'}
          </div>
          <div class="min-w-0 leading-tight">
            <div class="truncate text-sm font-medium text-ink">{session.user?.username}</div>
            <div class="text-[11px] text-muted">{roleLabel[session.user?.role] || session.user?.role}</div>
          </div>
        </div>
        <button class="btn btn-ghost w-full !justify-start" onclick={logout}>退出登录</button>
      </div>
    </aside>

    <!-- 主区域 -->
    <main class="ml-56 min-w-0 flex-1">
      <header class="sticky top-0 z-10 border-b border-line bg-surface/85 px-6 py-3.5 backdrop-blur">
        <h1 class="text-lg font-bold text-ink">{title}</h1>
      </header>
      <div class="p-6">
        {#if route.name === 'dashboard'}
          <Dashboard />
        {:else if route.name === 'apis'}
          <APIList />
        {:else if route.name === 'api-detail'}
          {#key route.id}
            <APIDetail apiId={route.id} />
          {/key}
        {:else if route.name === 'stats'}
          <Stats />
        {:else if route.name === 'logs'}
          <Logs />
        {:else if route.name === 'users'}
          <Users />
        {:else if route.name === 'help'}
          <Help />
        {:else}
          <Dashboard />
        {/if}
      </div>
    </main>
  </div>
{/if}

<Toasts />
