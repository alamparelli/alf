import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'

describe('postMessage security (#96)', () => {
  it('App.svelte does not use wildcard origin in postMessage', () => {
    const src = readFileSync(resolve(__dirname, '../App.svelte'), 'utf-8')
    // Match postMessage calls with '*' as the origin argument.
    const wildcardPattern = /\.postMessage\([^)]+,\s*['"]\\*['"]\s*\)/
    expect(src).not.toMatch(wildcardPattern)
  })

  it('App.svelte uses location.origin for postMessage', () => {
    const src = readFileSync(resolve(__dirname, '../App.svelte'), 'utf-8')
    expect(src).toContain('location.origin')
  })
})

describe('SDK v4 security', () => {
  const sdk = readFileSync(resolve(__dirname, '../../public/alf-app-sdk.js'), 'utf-8')

  it('SDK does not use parent.postMessage with wildcard', () => {
    expect(sdk).not.toMatch(/parent\.postMessage/)
  })

  it('SDK does not access localStorage (blocked by sandbox)', () => {
    expect(sdk).not.toContain('localStorage')
  })

  it('SDK does not use credentials: same-origin (cookies blocked by sandbox)', () => {
    expect(sdk).not.toContain("'same-origin'")
    expect(sdk).not.toContain('"same-origin"')
  })

  it('SDK uses Bearer token authentication', () => {
    expect(sdk).toContain("'Bearer '")
    expect(sdk).toContain("'Authorization'")
  })
})
