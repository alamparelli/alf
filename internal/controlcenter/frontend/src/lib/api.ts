import { toasts } from '../stores/toast.svelte'

/**
 * Authenticated API call with CSRF protection and 401 handling.
 */
export async function api<T = any>(pathOrMethod: string, optsOrPath?: RequestInit | string, body?: any): Promise<T> {
  // Support both signatures:
  //   api('/path', opts?)           — standard
  //   api('GET', '/path', body?)    — method-first (compat)
  let path: string
  let opts: RequestInit = {}

  if (typeof optsOrPath === 'string') {
    // Method-first: api('POST', '/api/foo', body?)
    const method = pathOrMethod.toUpperCase()
    path = optsOrPath
    opts = { method }
    if (body !== undefined) {
      opts.headers = { 'Content-Type': 'application/json' }
      opts.body = JSON.stringify(body)
    }
  } else {
    path = pathOrMethod
    opts = optsOrPath || {}
  }

  const method = (opts.method || 'GET').toUpperCase()
  const headers: Record<string, string> = {
    'X-Requested-With': 'XMLHttpRequest',
    ...(opts.headers as Record<string, string> || {}),
  }

  const res = await fetch(path, { ...opts, headers, credentials: 'same-origin' })

  if (res.status === 401) {
    toasts.show('Session expired — send /login to your bot', 'error')
    throw new Error('401')
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw body
  }

  return res.json()
}

/**
 * Escape HTML entities for safe rendering.
 */
export function esc(s: string): string {
  const d = document.createElement('div')
  d.textContent = s
  return d.innerHTML
}

/**
 * Poll /api/status until daemon is back online, then reload.
 */
export function waitForDaemonAndReload(onStatus?: (msg: string) => void) {
  let dots = 0
  let elapsed = 0
  const interval = 1500
  const maxWait = 120000

  if (onStatus) onStatus('Restarting...')

  const timer = setInterval(async () => {
    elapsed += interval
    dots = (dots + 1) % 4
    if (onStatus) onStatus('Waiting for daemon' + '.'.repeat(dots + 1) + ' (' + Math.round(elapsed / 1000) + 's)')

    if (elapsed >= maxWait) {
      clearInterval(timer)
      if (onStatus) onStatus('Daemon did not come back. Check logs.')
      return
    }

    try {
      const res = await fetch('/api/status', { credentials: 'same-origin' })
      if (res.ok) {
        clearInterval(timer)
        window.location.reload()
      }
    } catch {
      // expected while restarting
    }
  }, interval)
}
