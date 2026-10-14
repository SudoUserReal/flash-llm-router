<script>
  import { formatCompact } from '../lib/format.js'

  let {
    points = [], // [{label, value, tooltip?}]
    colorClass = 'bg-accent',
    height = 'h-52',
    valueFmt = formatCompact,
  } = $props()

  const max = $derived(Math.max(1, ...points.map((p) => Number(p.value) || 0)))
  const nonZero = $derived(points.filter((p) => Number(p.value) > 0).length)
</script>

<div>
  <div class="flex {height} items-end gap-[3px]">
    {#each points as p, i (i)}
      <div class="group relative flex h-full min-w-0 flex-1 flex-col items-center justify-end">
        <!-- 悬停提示 -->
        <div
          class="pointer-events-none absolute bottom-full left-1/2 z-10 mb-1 hidden -translate-x-1/2 rounded-lg bg-deep-panel px-2.5 py-1.5 text-xs whitespace-nowrap text-white shadow-lg group-hover:block"
        >
          <div class="font-medium">{p.tooltip ?? p.label}</div>
          <div class="text-white/60">{valueFmt(p.value)}</div>
        </div>
        <div
          class="{colorClass} mx-auto min-h-[2px] w-full max-w-[40px] rounded-t-md transition-opacity group-hover:opacity-80"
          style="height: {Math.max((Number(p.value) || 0) / max * 100, Number(p.value) > 0 ? 1 : 0.6)}%"
        ></div>
      </div>
    {/each}
  </div>
  <div class="mt-2 flex justify-between text-[11px] text-muted/80">
    {#if points.length > 0}
      <span>{points[0]?.label}</span>
      {#if points.length > 4 && points[Math.floor(points.length / 2)]?.label !== points[0]?.label && points[Math.floor(points.length / 2)]?.label !== points[points.length - 1]?.label}
        <span>{points[Math.floor(points.length / 2)]?.label}</span>
      {/if}
      {#if points.length > 1}<span>{points[points.length - 1]?.label}</span>{/if}
    {/if}
  </div>
  {#if nonZero === 0}
    <p class="mt-1 text-center text-xs text-muted/80">该时间段暂无数据</p>
  {/if}
</div>
