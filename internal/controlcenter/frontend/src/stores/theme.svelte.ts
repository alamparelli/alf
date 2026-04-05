export interface ThemeEntry {
  label: string
  light: string
  dark: string
  lightBorder: string
  darkBorder: string
  termLight: string
  termDark: string
}

export const ALF_THEMES: Record<string, ThemeEntry> = {
  sage:          { label: 'Sage',        light: '#f0f3ec', dark: '#222822', lightBorder: '#bcc5b8', darkBorder: '#3a4638', termLight: 'Sage Light',        termDark: 'Sage Dark' },
  studio:        { label: 'Studio',      light: '#f5f3f0', dark: '#1c1c1c', lightBorder: '#d6d3cf', darkBorder: '#333',    termLight: 'Studio Light',      termDark: 'Studio Dark' },
  catppuccin:    { label: 'Catppuccin',  light: '#eff1f5', dark: '#1e1e2e', lightBorder: '#ccd0da', darkBorder: '#45475a', termLight: 'Catppuccin Latte',  termDark: 'Catppuccin Mocha' },
  dracula:       { label: 'Dracula',     light: '#f8f8f2', dark: '#282a36', lightBorder: '#d8d8d0', darkBorder: '#44475a', termLight: 'Dracula',           termDark: 'Dracula' },
  solarized:     { label: 'Solarized',   light: '#fdf6e3', dark: '#002b36', lightBorder: '#d3cbb7', darkBorder: '#073642', termLight: 'Solarized Light',   termDark: 'Solarized Dark' },
  'tokyo-night': { label: 'Tokyo Night', light: '#d5d6db', dark: '#1a1b26', lightBorder: '#b8b9be', darkBorder: '#292e42', termLight: 'Tokyo Night',       termDark: 'Tokyo Night' },
  github:        { label: 'GitHub',      light: '#ffffff', dark: '#0d1117', lightBorder: '#d0d7de', darkBorder: '#30363d', termLight: 'GitHub Dark',       termDark: 'GitHub Dark' },
  nord:          { label: 'Nord',        light: '#eceff4', dark: '#2e3440', lightBorder: '#c8cdd5', darkBorder: '#434c5e', termLight: 'Nord',              termDark: 'Nord' },
}

class ThemeStore {
  palette = $state(localStorage.getItem('alf-palette') || 'sage')
  isDark = $state(window.matchMedia('(prefers-color-scheme: dark)').matches)

  // Port registry for MessageChannel-based theme sync with sandboxed iframes
  private _ports = new Map<string, MessagePort>()

  constructor() {
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', (e) => {
      this.isDark = e.matches
    })
  }

  apply(palette: string) {
    if (!ALF_THEMES[palette]) palette = 'sage'
    this.palette = palette
    localStorage.setItem('alf-palette', palette)
    localStorage.removeItem('alf-term-theme')

    const link = document.getElementById('alf-theme-link') as HTMLLinkElement
    if (link) {
      const base = link.href.replace(/theme-[^/]+\.css$/, '')
      link.href = base + 'theme-' + palette + '.css'
    }

    this.syncAllPorts()
  }

  /** Register an app's MessagePort for theme sync and event broadcast */
  registerPort(slug: string, port: MessagePort) {
    this._ports.set(slug, port)
  }

  /** Unregister an app's port on destroy */
  unregisterPort(slug: string) {
    this._ports.delete(slug)
  }

  /** Sync theme to a specific app's port */
  syncPort(slug: string) {
    const port = this._ports.get(slug)
    port?.postMessage({ type: 'alf', action: 'theme', palette: this.palette, dark: this.isDark })
  }

  /** Sync theme to all registered app ports */
  syncAllPorts() {
    const msg = { type: 'alf', action: 'theme', palette: this.palette, dark: this.isDark }
    for (const port of this._ports.values()) {
      port.postMessage(msg)
    }
  }

  /** Broadcast a message to all app ports except the sender */
  broadcastToOtherApps(senderSlug: string, msg: any) {
    for (const [s, port] of this._ports) {
      if (s !== senderSlug) {
        port.postMessage(msg)
      }
    }
  }
}

export const theme = new ThemeStore()
