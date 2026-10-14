// 会话状态（token 存 localStorage，用户信息内存缓存）
const TOKEN_KEY = 'flr_token'

export const session = $state({
  token: localStorage.getItem(TOKEN_KEY) || '',
  user: null,
  ready: false, // 是否已完成初始校验
})

export function setSession(token, user) {
  session.token = token
  session.user = user
  localStorage.setItem(TOKEN_KEY, token)
}

export function setUser(user) {
  session.user = user
}

export function clearSession() {
  session.token = ''
  session.user = null
  localStorage.removeItem(TOKEN_KEY)
}

export function canWrite() {
  return session.user?.role === 'admin' || session.user?.role === 'operator'
}

export function isAdmin() {
  return session.user?.role === 'admin'
}
