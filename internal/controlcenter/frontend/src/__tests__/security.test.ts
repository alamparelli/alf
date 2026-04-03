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
