// 极简 hash 路由：#/apis/3 等
export const router = $state({ hash: location.hash || '#/' })

window.addEventListener('hashchange', () => {
  router.hash = location.hash || '#/'
})

export function navigate(path) {
  if (location.hash === path) return
  location.hash = path
}

// 解析当前路由：{ name, params }
export function parseRoute(hash) {
  const parts = hash.replace(/^#\/?/, '').split('/').filter(Boolean)
  if (parts.length === 0) return { name: 'dashboard' }
  switch (parts[0]) {
    case 'login':
      return { name: 'login' }
    case 'apis':
      return parts.length > 1 ? { name: 'api-detail', id: Number(parts[1]) } : { name: 'apis' }
    case 'stats':
      return { name: 'stats' }
    case 'logs':
      return { name: 'logs' }
    case 'users':
      return { name: 'users' }
    case 'help':
      return { name: 'help' }
    default:
      return { name: 'dashboard' }
  }
}
