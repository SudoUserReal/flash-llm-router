<script>
  import { toasts } from '../lib/toast.svelte.js'

  const styles = {
    success: 'bg-success',
    error: 'bg-error',
    info: 'bg-primary',
  }
</script>

<div class="pointer-events-none fixed top-4 right-4 z-[100] flex w-80 flex-col gap-2">
  {#each toasts as t (t.id)}
    <div
      class="{styles[t.type] || styles.info} pointer-events-auto rounded-[14px] px-4 py-2.5 text-sm font-medium text-white shadow-[0_10px_25px_rgba(18,43,165,0.18)]"
    >
      {t.message}
    </div>
  {/each}
</div>
