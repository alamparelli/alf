import { describe, it, expect } from 'vitest'
import { execSync } from 'child_process'
import { existsSync, readFileSync } from 'fs'
import { resolve } from 'path'

const webDir = resolve(__dirname, '../../../web')

describe('Vite build output', () => {
  it('produces index.html', () => {
    expect(existsSync(resolve(webDir, 'index.html'))).toBe(true)
  })

  it('index.html contains app mount point', () => {
    const html = readFileSync(resolve(webDir, 'index.html'), 'utf-8')
    expect(html).toContain('id="app"')
  })

  it('index.html contains theme-init script', () => {
    const html = readFileSync(resolve(webDir, 'index.html'), 'utf-8')
    expect(html).toContain('alf-theme-link')
    expect(html).toContain('alf-palette')
  })

  it('includes theme CSS files', () => {
    const themes = ['sage', 'studio', 'catppuccin', 'dracula', 'solarized', 'tokyo-night', 'github', 'nord']
    for (const t of themes) {
      expect(existsSync(resolve(webDir, `theme-${t}.css`))).toBe(true)
    }
  })

  it('includes alf-app-sdk.js', () => {
    expect(existsSync(resolve(webDir, 'alf-app-sdk.js'))).toBe(true)
  })

  it('alf-app-sdk.js exposes AlfSDK global', () => {
    const sdk = readFileSync(resolve(webDir, 'alf-app-sdk.js'), 'utf-8')
    expect(sdk).toContain('global.AlfSDK')
    expect(sdk).toContain('init:')
    expect(sdk).toContain('tool:')
    expect(sdk).toContain('navigate:')
    expect(sdk).toContain('toast:')
  })

  it('includes debug-tools.html for Go embed', () => {
    expect(existsSync(resolve(webDir, 'debug-tools.html'))).toBe(true)
  })

  it('produces JS and CSS bundles in assets/', () => {
    const assetsDir = resolve(webDir, 'assets')
    expect(existsSync(assetsDir)).toBe(true)
    const { readdirSync } = require('fs')
    const files = readdirSync(assetsDir)
    expect(files.some((f: string) => f.endsWith('.js'))).toBe(true)
    expect(files.some((f: string) => f.endsWith('.css'))).toBe(true)
  })

  it('includes marked.min.js and purify.min.js for Petite Vue apps', () => {
    expect(existsSync(resolve(webDir, 'marked.min.js'))).toBe(true)
    expect(existsSync(resolve(webDir, 'purify.min.js'))).toBe(true)
  })
})
