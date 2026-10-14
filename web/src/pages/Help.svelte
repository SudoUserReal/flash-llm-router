<script>
  import { marked } from 'marked'
  import helpRaw from '../lib/help.md?raw'
  import { copyText } from '../lib/format.js'
  import { toastOk } from '../lib/toast.svelte.js'

  const baseURL = location.origin
  const apiKeyPlaceholder = 'sk-flr-xxxxxxxx'

  const html = marked.parse(helpRaw.replaceAll('{{BASE_URL}}', baseURL).replaceAll('{{API_KEY}}', apiKeyPlaceholder), {
    async: false,
  })

  async function copyDoc() {
    await copyText(helpRaw.replaceAll('{{BASE_URL}}', baseURL).replaceAll('{{API_KEY}}', apiKeyPlaceholder))
    toastOk('已复制整篇文档（Markdown）')
  }

  async function copyBaseURL() {
    await copyText(baseURL)
    toastOk('已复制 Base URL')
  }
</script>

<div class="mx-auto max-w-4xl space-y-5">
  <!-- 工具条 -->
  <div class="card flex flex-wrap items-center justify-between gap-3 p-4">
    <div class="min-w-0">
      <div class="text-sm font-bold text-ink">API 调用文档</div>
      <div class="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted">
        <code class="rounded-md bg-surface px-2 py-0.5 font-mono">{baseURL}</code>
        <button class="btn btn-ghost !px-1.5 !py-0 text-xs" onclick={copyBaseURL}>复制 Base URL</button>
        <span class="text-muted/60">·</span>
        <span>鉴权：<code class="font-mono">Authorization: Bearer &lt;API Key&gt;</code> 或 <code class="font-mono">x-api-key</code></span>
      </div>
    </div>
    <button class="btn btn-primary shrink-0" onclick={copyDoc}>复制文档</button>
  </div>

  <!-- 文档正文 -->
  <article class="card p-6">
    <!-- eslint-disable-next-line svelte/no-at-html-tags — 内容为项目内置的静态 Markdown -->
    <div class="prose prose-slate max-w-none prose-headings:font-bold prose-a:text-accent prose-code:before:content-none prose-code:after:content-none prose-pre:bg-deep-panel prose-pre:text-[#d9d9e3]">
      {@html html}
    </div>
  </article>

  <p class="pb-4 text-center text-xs text-muted">
    文档版本随服务发布更新 · 接口行为语义详见项目 README「行为语义」章节
  </p>
</div>
