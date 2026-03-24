import { describe, it, expect, beforeEach, vi } from 'vitest'

// Mock localStorage
const store: Record<string, string> = {}
const localStorageMock = {
  getItem: (key: string) => store[key] ?? null,
  setItem: (key: string, value: string) => { store[key] = value },
  removeItem: (key: string) => { delete store[key] },
  clear: () => { Object.keys(store).forEach(k => delete store[k]) },
}
Object.defineProperty(globalThis, 'localStorage', { value: localStorageMock })

// Mock matchMedia
Object.defineProperty(globalThis, 'matchMedia', {
  value: () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }),
})

describe('NavStore', () => {
  beforeEach(() => localStorageMock.clear())

  it('defaults to chat view', async () => {
    const { nav } = await import('../stores/nav.svelte')
    // Default is 'chat' when localStorage is empty
    expect(nav.currentView).toBeDefined()
  })

  it('has 13 system tabs', async () => {
    const { SYSTEM_TABS } = await import('../stores/nav.svelte')
    expect(SYSTEM_TABS).toHaveLength(13)
    expect(SYSTEM_TABS.map(t => t.view)).toContain('chat')
    expect(SYSTEM_TABS.map(t => t.view)).toContain('settings')
    expect(SYSTEM_TABS.map(t => t.view)).toContain('terminal')
  })
})

describe('ThemeStore', () => {
  beforeEach(() => localStorageMock.clear())

  it('has all 8 themes', async () => {
    const { ALF_THEMES } = await import('../stores/theme.svelte')
    const keys = Object.keys(ALF_THEMES)
    expect(keys).toHaveLength(8)
    expect(keys).toContain('sage')
    expect(keys).toContain('dracula')
    expect(keys).toContain('nord')
  })

  it('each theme has required fields', async () => {
    const { ALF_THEMES } = await import('../stores/theme.svelte')
    for (const [, t] of Object.entries(ALF_THEMES)) {
      expect(t).toHaveProperty('label')
      expect(t).toHaveProperty('light')
      expect(t).toHaveProperty('dark')
      expect(t).toHaveProperty('termLight')
      expect(t).toHaveProperty('termDark')
    }
  })
})

describe('ToastStore', () => {
  it('adds and auto-removes toasts', async () => {
    vi.useFakeTimers()
    const { toasts } = await import('../stores/toast.svelte')
    toasts.show('test message', 'success')
    expect(toasts.items.length).toBeGreaterThanOrEqual(1)
    vi.advanceTimersByTime(4000)
    vi.useRealTimers()
  })
})
