import { describe, it, expect, vi, beforeEach } from 'vitest'

// Mock fetch globally
const mockFetch = vi.fn()
Object.defineProperty(globalThis, 'fetch', { value: mockFetch, writable: true })

// Mock localStorage
Object.defineProperty(globalThis, 'localStorage', {
  value: {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
    clear: () => {},
  },
})

// Mock matchMedia
Object.defineProperty(globalThis, 'matchMedia', {
  value: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }),
})

describe('api()', () => {
  beforeEach(() => {
    mockFetch.mockReset()
  })

  it('adds CSRF header to requests', async () => {
    mockFetch.mockResolvedValue({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ ok: true }),
    })

    const { api } = await import('../lib/api')
    await api('/api/status')

    expect(mockFetch).toHaveBeenCalledWith(
      '/api/status',
      expect.objectContaining({
        headers: expect.objectContaining({
          'X-Requested-With': 'XMLHttpRequest',
        }),
        credentials: 'same-origin',
      })
    )
  })

  it('throws on 401 responses', async () => {
    mockFetch.mockResolvedValue({
      ok: false,
      status: 401,
      json: () => Promise.resolve({}),
    })

    const { api } = await import('../lib/api')
    await expect(api('/api/test')).rejects.toThrow('401')
  })

  it('esc() escapes HTML entities', async () => {
    const { esc } = await import('../lib/api')
    expect(esc('<script>alert("xss")</script>')).toBe('&lt;script&gt;alert("xss")&lt;/script&gt;')
    expect(esc('normal text')).toBe('normal text')
    expect(esc('a & b')).toBe('a &amp; b')
  })
})

describe('AlfSDK (public/alf-app-sdk.js)', () => {
  it('SDK file contains all required methods', async () => {
    const { readFileSync } = await import('fs')
    const { resolve } = await import('path')
    const sdk = readFileSync(resolve(__dirname, '../../public/alf-app-sdk.js'), 'utf-8')

    const requiredMethods = ['init', 'api', 'bash', 'tool', 'navigate', 'toast', 'getTheme']
    for (const method of requiredMethods) {
      expect(sdk).toContain(`${method}:`)
    }
  })

  it('SDK uses Bearer token auth', async () => {
    const { readFileSync } = await import('fs')
    const { resolve } = await import('path')
    const sdk = readFileSync(resolve(__dirname, '../../public/alf-app-sdk.js'), 'utf-8')
    expect(sdk).toContain("'Bearer '")
    expect(sdk).toContain("'X-Requested-With'")
  })

  it('SDK uses MessageChannel port for communication', async () => {
    const { readFileSync } = await import('fs')
    const { resolve } = await import('path')
    const sdk = readFileSync(resolve(__dirname, '../../public/alf-app-sdk.js'), 'utf-8')
    expect(sdk).toContain('_port.postMessage')
    expect(sdk).toContain('alf-handshake')
  })
})
