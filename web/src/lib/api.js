import { session, clearSession } from './session.svelte.js'
import { navigate } from './router.svelte.js'

export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

export async function api(path, { method = 'GET', body } = {}) {
  const headers = {}
  if (session.token) headers['Authorization'] = `Bearer ${session.token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  const resp = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  if (resp.status === 401 && !path.startsWith('/api/auth/')) {
    clearSession()
    navigate('#/login')
    throw new ApiError('会话已过期，请重新登录', 401)
  }

  if (!resp.ok) {
    let msg = `请求失败 (HTTP ${resp.status})`
    try {
      const j = await resp.json()
      if (j && j.error) msg = typeof j.error === 'string' ? j.error : j.error.message || msg
    } catch {
      /* ignore */
    }
    throw new ApiError(msg, resp.status)
  }
  if (resp.status === 204) return null
  const ct = resp.headers.get('content-type') || ''
  return ct.includes('application/json') ? resp.json() : null
}

export const get = (path) => api(path)
export const post = (path, body) => api(path, { method: 'POST', body })
export const put = (path, body) => api(path, { method: 'PUT', body })
export const patch = (path, body) => api(path, { method: 'PATCH', body })
export const del = (path) => api(path, { method: 'DELETE' })
