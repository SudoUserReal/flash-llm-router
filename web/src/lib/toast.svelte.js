// 轻量 toast 提示
export const toasts = $state([])

let seq = 0

export function toast(message, type = 'info') {
  const id = ++seq
  toasts.push({ id, message, type })
  setTimeout(() => {
    const i = toasts.findIndex((t) => t.id === id)
    if (i >= 0) toasts.splice(i, 1)
  }, 4200)
}

export const toastOk = (m) => toast(m, 'success')
export const toastErr = (m) => toast(m, 'error')
