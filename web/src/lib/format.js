// 展示格式化工具

export function formatNumber(n) {
  if (n === null || n === undefined) return '-'
  return Number(n).toLocaleString('zh-CN')
}

export function formatCompact(n) {
  n = Number(n || 0)
  if (n >= 1e9) return (n / 1e9).toFixed(2) + 'B'
  if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M'
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k'
  return String(n)
}

export function formatCost(n) {
  n = Number(n || 0)
  if (n === 0) return '¥0'
  if (n < 1) {
    // 小额费用：定长小数显示，去尾零（如 ¥0.000086）
    let s = n.toFixed(6).replace(/0+$/, '').replace(/\.$/, '')
    if (s === '0') {
      s = n.toFixed(8).replace(/0+$/, '').replace(/\.$/, '')
      if (s === '0') return '<¥0.00000001'
    }
    return '¥' + s
  }
  return '¥' + n.toFixed(4).replace(/0+$/, '').replace(/\.$/, '')
}

export function formatDateTime(iso) {
  if (!iso) return '-'
  const d = new Date(iso)
  const pad = (x) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

export function formatTimeShort(iso) {
  if (!iso) return '-'
  const d = new Date(iso)
  const pad = (x) => String(x).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function formatLatency(ms) {
  ms = Number(ms || 0)
  if (ms >= 1000) return (ms / 1000).toFixed(2) + 's'
  return ms + 'ms'
}

export function timeAgo(iso) {
  if (!iso) return '-'
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return s + ' 秒前'
  if (s < 3600) return Math.floor(s / 60) + ' 分钟前'
  if (s < 86400) return Math.floor(s / 3600) + ' 小时前'
  return Math.floor(s / 86400) + ' 天前'
}

export function cooldownRemaining(until) {
  if (!until) return null
  const ms = new Date(until).getTime() - Date.now()
  if (ms <= 0) return null
  const s = Math.ceil(ms / 1000)
  if (s < 60) return s + ' 秒'
  if (s < 3600) return Math.ceil(s / 60) + ' 分钟'
  return Math.ceil(s / 3600) + ' 小时'
}

export function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(text)
  }
  // 回退方案（http 环境）
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  document.execCommand('copy')
  document.body.removeChild(ta)
  return Promise.resolve()
}

// 时间范围预设 → {from, to}（RFC3339）
export function rangePreset(key) {
  const now = new Date()
  const to = new Date(now)
  let from = new Date(now)
  switch (key) {
    case '1h':
      from.setHours(from.getHours() - 1)
      break
    case '24h':
      from.setDate(from.getDate() - 1)
      break
    case '7d':
      from.setDate(from.getDate() - 7)
      break
    case '30d':
      from.setDate(from.getDate() - 30)
      break
    case 'today':
      from.setHours(0, 0, 0, 0)
      break
    case 'yesterday': {
      from.setDate(from.getDate() - 1)
      from.setHours(0, 0, 0, 0)
      to.setHours(0, 0, 0, 0)
      break
    }
    default:
      from.setDate(from.getDate() - 7)
  }
  return { from: from.toISOString(), to: to.toISOString() }
}
