import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'

describe('MessageChannel sandbox migration', () => {
  const appFrame = readFileSync(resolve(__dirname, '../components/AppFrame.svelte'), 'utf-8')
  const sdk = readFileSync(resolve(__dirname, '../../public/alf-app-sdk.js'), 'utf-8')

  // ── AppFrame.svelte ──

  it('AppFrame uses MessageChannel for iframe communication', () => {
    expect(appFrame).toContain('new MessageChannel()')
    expect(appFrame).toContain('alf-handshake')
  })

  it('AppFrame does not use window message listener for app messages', () => {
    // Should not have window.addEventListener('message', handleMessage)
    // The handshake listener is in the SDK, not AppFrame
    expect(appFrame).not.toMatch(/window\.addEventListener\s*\(\s*['"]message['"]\s*,\s*handleMessage/)
  })

  it('AppFrame does not inject CSS into iframe contentDocument', () => {
    // These functions accessed frame.contentDocument which is blocked by sandbox
    expect(appFrame).not.toContain('injectUICSS')
    expect(appFrame).not.toContain('injectSafeAreas')
    expect(appFrame).not.toContain('injectSheetCSS')
    expect(appFrame).not.toContain('contentDocument')
  })

  it('AppFrame iframe has sandbox attribute', () => {
    expect(appFrame).toMatch(/sandbox\s*=\s*["']allow-scripts allow-forms["']/)
  })

  it('AppFrame fetches app token for sandboxed iframe', () => {
    expect(appFrame).toContain('/api/apps/')
    expect(appFrame).toContain('/token')
  })

  it('AppFrame sends init-context via port after handshake', () => {
    expect(appFrame).toContain('init-context')
    expect(appFrame).toContain('safeAreas')
  })

  it('AppFrame refreshes token periodically', () => {
    expect(appFrame).toContain('token-refresh')
    expect(appFrame).toContain('setInterval')
  })

  it('AppFrame registers and unregisters port in theme store', () => {
    expect(appFrame).toContain('theme.registerPort')
    expect(appFrame).toContain('theme.unregisterPort')
  })

  // ── SDK ──

  it('SDK v4 uses MessageChannel port, not parent.postMessage', () => {
    expect(sdk).toContain("VERSION: '4.0.0'")
    expect(sdk).toContain('_port.postMessage')
    expect(sdk).not.toContain("parent.postMessage")
  })

  it('SDK listens for alf-handshake to receive port', () => {
    expect(sdk).toContain('alf-handshake')
    expect(sdk).toContain('e.ports[0]')
  })

  it('SDK uses Bearer token auth, not cookies', () => {
    expect(sdk).toContain("'Authorization'")
    expect(sdk).toContain("'Bearer '")
    expect(sdk).not.toContain("'same-origin'")
    expect(sdk).not.toContain('credentials')
  })

  it('SDK does not use localStorage', () => {
    expect(sdk).not.toContain('localStorage')
  })

  it('SDK getTheme returns theme from parent handshake', () => {
    expect(sdk).toContain('_theme.palette')
    expect(sdk).toContain('_theme.dark')
  })

  it('SDK applies safe areas from parent via CSS variables', () => {
    expect(sdk).toContain('_applySafeAreas')
    expect(sdk).toContain('--safe-area-top')
    expect(sdk).toContain('--safe-area-bottom')
  })

  it('SDK handles token-refresh messages', () => {
    expect(sdk).toContain('token-refresh')
  })
})
