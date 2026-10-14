<script>
  let { open = false, title = '', wide = false, onclose = () => {}, children, footer } = $props()
</script>

{#if open}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-ink/40 p-4 pt-14 backdrop-blur-[1px]"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) onclose()
    }}
  >
    <div class="card w-full {wide ? 'max-w-3xl' : 'max-w-lg'} p-5" role="dialog" aria-modal="true" aria-label={title}>
      <div class="mb-4 flex items-center justify-between">
        <h3 class="text-base font-bold text-ink">{title}</h3>
        <button class="btn btn-ghost !px-2 !py-1 text-lg leading-none" onclick={onclose} aria-label="关闭">✕</button>
      </div>
      {@render children?.()}
      {#if footer}
        <div class="mt-5 flex justify-end gap-2">{@render footer()}</div>
      {/if}
    </div>
  </div>
{/if}
