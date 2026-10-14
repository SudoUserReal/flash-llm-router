<script>
  import { setSession } from '../lib/session.svelte.js'
  import { navigate } from '../lib/router.svelte.js'
  import { post, get } from '../lib/api.js'
  import Logo from '../components/Logo.svelte'

  let username = $state('')
  let password = $state('')
  let captchaCode = $state('')
  let captchaId = $state('')
  let captchaImg = $state('')
  let error = $state('')
  let loading = $state(false)

  async function loadCaptcha() {
    captchaCode = ''
    try {
      const d = await get('/api/auth/captcha')
      captchaId = d.captcha_id
      captchaImg = d.image
    } catch {
      error = '无法加载验证码，请检查服务是否正常'
    }
  }
  loadCaptcha()

  async function submit(e) {
    e.preventDefault()
    error = ''
    loading = true
    try {
      const d = await post('/api/auth/login', {
        username,
        password,
        captcha_id: captchaId,
        captcha_code: captchaCode,
      })
      setSession(d.token, d.user)
      navigate('#/')
    } catch (err) {
      error = err.message
      loadCaptcha()
    } finally {
      loading = false
    }
  }
</script>

<div class="relative flex min-h-screen items-center justify-center overflow-hidden bg-surface">
  <!-- 装饰色块（ClickUp 渐变语汇，点缀而非满屏） -->
  <div class="pointer-events-none absolute -top-40 -right-32 h-[28rem] w-[28rem] rounded-full bg-pale-purple opacity-80 blur-3xl"></div>
  <div class="pointer-events-none absolute -bottom-40 -left-32 h-[28rem] w-[28rem] rounded-full bg-pale-orange opacity-70 blur-3xl"></div>
  <div class="pointer-events-none absolute top-1/3 left-1/4 h-64 w-64 rounded-full bg-pale-blue opacity-60 blur-3xl"></div>

  <div class="relative w-full max-w-sm px-4">
    <div class="mb-6 flex flex-col items-center gap-3">
      <Logo size={52} />
      <div class="text-center">
        <div class="text-xl font-extrabold tracking-tight text-ink" style="font-family: var(--font-display)">
          flash-llm-router
        </div>
        <div class="mt-0.5 text-xs text-muted">LLM API 中转 · 多通道调度 · token 计费</div>
        <div class="mt-2.5 flex justify-center gap-1.5">
          <span class="chip !px-2.5 !py-0.5 !text-[10px]">chat completions</span>
          <span class="chip !px-2.5 !py-0.5 !text-[10px]">responses</span>
          <span class="chip !px-2.5 !py-0.5 !text-[10px]">failover</span>
        </div>
      </div>
    </div>

    <form class="card space-y-4 p-6" onsubmit={submit}>
      <div>
        <label class="label" for="login-username">用户名</label>
        <input id="login-username" class="input" bind:value={username} autocomplete="username" required />
      </div>
      <div>
        <label class="label" for="login-password">密码</label>
        <input id="login-password" class="input" type="password" bind:value={password} autocomplete="current-password" required />
      </div>
      <div>
        <label class="label" for="login-captcha">验证码</label>
        <div class="flex gap-2">
          <input id="login-captcha" class="input flex-1" bind:value={captchaCode} maxlength="4" required placeholder="输入图中字符" />
          {#if captchaImg}
            <button type="button" class="shrink-0 cursor-pointer" onclick={loadCaptcha} title="点击刷新验证码" aria-label="刷新验证码">
              <img src={captchaImg} alt="验证码" class="h-[38px] rounded-[10px] ring-1 ring-edge/60" />
            </button>
          {/if}
        </div>
      </div>

      {#if error}
        <p class="rounded-[12px] bg-[#fdecec] px-3.5 py-2 text-sm font-medium text-[#c4343a]">{error}</p>
      {/if}

      <button class="btn btn-primary w-full !py-2.5" disabled={loading}>
        {loading ? '登录中…' : '登录'}
      </button>
    </form>
    <p class="mt-4 text-center text-xs text-muted">
      初始账号见服务启动日志（或环境变量 ADMIN_USERNAME / ADMIN_PASSWORD）
    </p>
  </div>
</div>
