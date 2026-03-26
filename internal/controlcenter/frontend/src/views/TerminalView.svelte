<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import { WebLinksAddon } from '@xterm/addon-web-links'
  import { RotateCw } from 'lucide-svelte'
  import { toasts } from '../stores/toast.svelte'
  import { theme } from '../stores/theme.svelte'
  import '@xterm/xterm/css/xterm.css'

  // --- Terminal Themes ---
  const termThemes: Record<string, any> = {
    dark: {
      background: '#1e1e2e',
      foreground: '#cdd6f4',
      cursor: '#f5e0dc',
      selectionBackground: '#585b7066',
      black: '#45475a', red: '#f38ba8', green: '#a6e3a1', yellow: '#f9e2af',
      blue: '#89b4fa', magenta: '#f5c2e7', cyan: '#94e2d5', white: '#bac2de',
      brightBlack: '#585b70', brightRed: '#f38ba8', brightGreen: '#a6e3a1',
      brightYellow: '#f9e2af', brightBlue: '#89b4fa', brightMagenta: '#f5c2e7',
      brightCyan: '#94e2d5', brightWhite: '#a6adc8',
    },
    dracula: {
      background: '#282a36',
      foreground: '#f8f8f2',
      cursor: '#f8f8f2',
      selectionBackground: '#44475a',
      black: '#21222c', red: '#ff5555', green: '#50fa7b', yellow: '#f1fa8c',
      blue: '#bd93f9', magenta: '#ff79c6', cyan: '#8be9fd', white: '#f8f8f2',
      brightBlack: '#6272a4', brightRed: '#ff6e6e', brightGreen: '#69ff94',
      brightYellow: '#ffffa5', brightBlue: '#d6acff', brightMagenta: '#ff92df',
      brightCyan: '#a4ffff', brightWhite: '#ffffff',
    },
    'tokyo-night': {
      background: '#1a1b26',
      foreground: '#c0caf5',
      cursor: '#c0caf5',
      selectionBackground: '#33467c',
      black: '#15161e', red: '#f7768e', green: '#9ece6a', yellow: '#e0af68',
      blue: '#7aa2f7', magenta: '#bb9af7', cyan: '#7dcfff', white: '#a9b1d6',
      brightBlack: '#414868', brightRed: '#f7768e', brightGreen: '#9ece6a',
      brightYellow: '#e0af68', brightBlue: '#7aa2f7', brightMagenta: '#bb9af7',
      brightCyan: '#7dcfff', brightWhite: '#c0caf5',
    },
    solarized: {
      background: '#002b36',
      foreground: '#839496',
      cursor: '#839496',
      selectionBackground: '#073642',
      black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
      blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
      brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#586e75',
      brightYellow: '#657b83', brightBlue: '#839496', brightMagenta: '#6c71c4',
      brightCyan: '#93a1a1', brightWhite: '#fdf6e3',
    },
    nord: {
      background: '#2e3440',
      foreground: '#d8dee9',
      cursor: '#d8dee9',
      selectionBackground: '#434c5e',
      black: '#3b4252', red: '#bf616a', green: '#a3be8c', yellow: '#ebcb8b',
      blue: '#81a1c1', magenta: '#b48ead', cyan: '#88c0d0', white: '#e5e9f0',
      brightBlack: '#4c566a', brightRed: '#bf616a', brightGreen: '#a3be8c',
      brightYellow: '#ebcb8b', brightBlue: '#81a1c1', brightMagenta: '#b48ead',
      brightCyan: '#8fbcbb', brightWhite: '#eceff4',
    },
    github: {
      background: '#0d1117',
      foreground: '#c9d1d9',
      cursor: '#c9d1d9',
      selectionBackground: '#264f78',
      black: '#484f58', red: '#ff7b72', green: '#3fb950', yellow: '#d29922',
      blue: '#58a6ff', magenta: '#bc8cff', cyan: '#39c5cf', white: '#b1bac4',
      brightBlack: '#6e7681', brightRed: '#ffa198', brightGreen: '#56d364',
      brightYellow: '#e3b341', brightBlue: '#79c0ff', brightMagenta: '#d2a8ff',
      brightCyan: '#56d4dd', brightWhite: '#f0f6fc',
    },
    sage: {
      background: '#f4f6f3',
      foreground: '#3c4841',
      cursor: '#3c4841',
      selectionBackground: '#c8d5c3',
      black: '#3c4841', red: '#b4463a', green: '#4a7c59', yellow: '#8a6d3b',
      blue: '#4a6fa5', magenta: '#8b5e83', cyan: '#3d8a8a', white: '#d6ddd3',
      brightBlack: '#5c6b5e', brightRed: '#c9564a', brightGreen: '#5d9a6f',
      brightYellow: '#a0834b', brightBlue: '#5c85bf', brightMagenta: '#a37499',
      brightCyan: '#52a3a3', brightWhite: '#edf1eb',
    },
    studio: {
      background: '#1b1b2f',
      foreground: '#c5c8d6',
      cursor: '#e8e8ff',
      selectionBackground: '#2e2e52',
      black: '#16162a', red: '#e06c75', green: '#98c379', yellow: '#e5c07b',
      blue: '#61afef', magenta: '#c678dd', cyan: '#56b6c2', white: '#abb2bf',
      brightBlack: '#3e4058', brightRed: '#e88388', brightGreen: '#a8d08d',
      brightYellow: '#ebd09c', brightBlue: '#7ec0f5', brightMagenta: '#d190e4',
      brightCyan: '#6fc5d0', brightWhite: '#d0d4e0',
    },
    'catppuccin-latte': {
      background: '#eff1f5',
      foreground: '#4c4f69',
      cursor: '#dc8a78',
      selectionBackground: '#acb0be',
      black: '#5c5f77', red: '#d20f39', green: '#40a02b', yellow: '#df8e1d',
      blue: '#1e66f5', magenta: '#ea76cb', cyan: '#179299', white: '#acb0be',
      brightBlack: '#6c6f85', brightRed: '#d20f39', brightGreen: '#40a02b',
      brightYellow: '#df8e1d', brightBlue: '#1e66f5', brightMagenta: '#ea76cb',
      brightCyan: '#179299', brightWhite: '#bcc0cc',
    },
    'catppuccin-mocha': {
      background: '#1e1e2e',
      foreground: '#cdd6f4',
      cursor: '#f5e0dc',
      selectionBackground: '#585b7066',
      black: '#45475a', red: '#f38ba8', green: '#a6e3a1', yellow: '#f9e2af',
      blue: '#89b4fa', magenta: '#f5c2e7', cyan: '#94e2d5', white: '#bac2de',
      brightBlack: '#585b70', brightRed: '#f38ba8', brightGreen: '#a6e3a1',
      brightYellow: '#f9e2af', brightBlue: '#89b4fa', brightMagenta: '#f5c2e7',
      brightCyan: '#94e2d5', brightWhite: '#a6adc8',
    },
  }

  // Map palette names to terminal theme keys (some have light/dark variants)
  const themeMapping: Record<string, [string, string]> = {
    sage:          ['sage', 'dark'],
    studio:        ['studio', 'studio'],
    catppuccin:    ['catppuccin-latte', 'catppuccin-mocha'],
    dracula:       ['dracula', 'dracula'],
    solarized:     ['solarized', 'solarized'],
    'tokyo-night': ['tokyo-night', 'tokyo-night'],
    github:        ['github', 'github'],
    nord:          ['nord', 'nord'],
  }

  function getTermTheme(): any {
    const [lightKey, darkKey] = themeMapping[theme.palette] || ['dark', 'dark']
    const key = theme.isDark ? darkKey : lightKey
    return termThemes[key] || termThemes.dark
  }

  let termContainer: HTMLDivElement
  let term: Terminal | null = null
  let fitAddon: FitAddon | null = null
  let ws: WebSocket | null = null
  let resizeObserver: ResizeObserver | null = null

  // SSH mode: parsed from URL hash ?ssh=service-name
  let sshService = $state<string | null>(null)

  // URL bar overlay state
  let urlBarVisible = $state(false)
  let urlBarValue = $state('')

  // Mobile input bar
  let isMobile = $state(false)
  let mobileInput = $state('')

  function detectMobile() {
    isMobile = window.innerWidth <= 768 || 'ontouchstart' in window
  }

  function connect() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = sshService
      ? `${proto}//${location.host}/api/ssh/${encodeURIComponent(sshService)}/session`
      : `${proto}//${location.host}/api/terminal`
    ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      // Send initial resize
      if (term && fitAddon) {
        fitAddon.fit()
        sendResize(term.cols, term.rows)
      }
      // Default to data directory (local terminal only)
      if (!sshService) {
        setTimeout(() => sendInput('cd /home/alf/data && clear\n'), 100)
      }
    }

    ws.onmessage = (ev) => {
      if (term) {
        const data = ev.data instanceof ArrayBuffer
          ? new TextDecoder().decode(ev.data)
          : ev.data
        term.write(data)
        checkForUrl(data)
      }
    }

    ws.onclose = () => {
      if (term) {
        term.write('\r\n\x1b[33m[Session ended. Refresh to reconnect.]\x1b[0m\r\n')
      }
    }

    ws.onerror = () => {
      toasts.show('Terminal connection failed', 'error')
    }
  }

  function sendResize(cols: number, rows: number) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    // Binary resize protocol: [0x01, cols_hi, cols_lo, rows_hi, rows_lo]
    const buf = new Uint8Array(5)
    buf[0] = 0x01
    buf[1] = (cols >> 8) & 0xff
    buf[2] = cols & 0xff
    buf[3] = (rows >> 8) & 0xff
    buf[4] = rows & 0xff
    ws.send(buf.buffer)
  }

  function sendInput(data: string) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(data)
  }

  function handleMobileSend() {
    if (mobileInput.trim()) {
      sendInput(mobileInput + '\n')
      mobileInput = ''
    }
  }

  async function handleMobilePaste() {
    try {
      const text = await navigator.clipboard.readText()
      sendInput(text)
    } catch {
      toasts.show('Clipboard access denied', 'error')
    }
  }

  function newSession() {
    if (ws) {
      ws.close()
      ws = null
    }
    if (term) {
      term.clear()
      term.reset()
    }
    connect()
  }

  function handleMobileCopy() {
    if (term) {
      const sel = term.getSelection()
      if (sel) {
        navigator.clipboard.writeText(sel)
        toasts.show('Copied to clipboard', 'success')
      }
    }
  }

  // URL bar: detect long URLs in terminal output for OAuth flows
  function checkForUrl(data: string) {
    const urlMatch = data.match(/(https?:\/\/[^\s\x1b]{80,})/)
    if (urlMatch) {
      urlBarValue = urlMatch[1]
      urlBarVisible = true
    }
  }

  onMount(() => {
    detectMobile()
    window.addEventListener('resize', detectMobile)

    // Parse SSH service from URL hash: #/terminal?ssh=service-name
    const hash = window.location.hash
    const sshMatch = hash.match(/[?&]ssh=([^&]+)/)
    if (sshMatch) {
      sshService = decodeURIComponent(sshMatch[1])
    }

    const theme = getTermTheme()

    term = new Terminal({
      cursorBlink: true,
      fontSize: isMobile ? 12 : 14,
      fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
      theme,
      allowProposedApi: true,
      scrollback: 5000,
    })

    fitAddon = new FitAddon()
    term.loadAddon(fitAddon)

    const webLinksAddon = new WebLinksAddon((_, uri) => {
      window.open(uri, '_blank')
    })
    term.loadAddon(webLinksAddon)

    term.open(termContainer)
    fitAddon.fit()

    term.onData((data) => {
      sendInput(data)
    })

    // ResizeObserver for auto-fit (debounced to avoid rapid-fire during drag resize)
    let resizeTimer: ReturnType<typeof setTimeout> | undefined
    resizeObserver = new ResizeObserver(() => {
      clearTimeout(resizeTimer)
      resizeTimer = setTimeout(() => {
        if (fitAddon && term) {
          fitAddon.fit()
          sendResize(term.cols, term.rows)
        }
      }, 50)
    })
    resizeObserver.observe(termContainer)

    connect()
  })

  // Reactively sync terminal theme when CC palette or dark mode changes.
  $effect(() => {
    // Access reactive deps so Svelte tracks them.
    const _ = theme.palette
    const __ = theme.isDark
    if (term) {
      term.options.theme = getTermTheme()
    }
  })

  onDestroy(() => {
    window.removeEventListener('resize', detectMobile)
    if (ws) {
      ws.close()
      ws = null
    }
    if (resizeObserver) {
      resizeObserver.disconnect()
      resizeObserver = null
    }
    if (term) {
      term.dispose()
      term = null
    }
    fitAddon = null
  })
</script>

<div class="terminal-view">
  <div class="term-header">
    <span class="term-title">{sshService ? `SSH: ${sshService}` : 'Terminal'}</span>
    <div class="term-header-actions">
      {#if sshService}
        <a class="term-btn" href="#/terminal" onclick={() => { sshService = null; newSession() }}>
          Local Terminal
        </a>
      {/if}
      <button class="term-btn" onclick={newSession} title="New session">
        <RotateCw size={13} /> New Session
      </button>
    </div>
  </div>

  <!-- URL Bar overlay -->
  {#if urlBarVisible}
    <div class="url-bar">
      <input type="text" readonly value={urlBarValue} class="url-input"
        onclick={(e) => (e.target as HTMLInputElement).select()} />
      <a href={urlBarValue} target="_blank" rel="noopener" class="url-open">Open</a>
      <button class="url-close" onclick={() => urlBarVisible = false}>×</button>
    </div>
  {/if}

  <div id="terminalContainer" class="term-container" bind:this={termContainer}></div>

  <!-- Mobile input bar -->
  {#if isMobile}
    <div class="mobile-input-bar">
      <input
        type="text"
        bind:value={mobileInput}
        placeholder="Type command..."
        class="mobile-input"
        onkeydown={(e) => { if (e.key === 'Enter') handleMobileSend() }}
      />
      <button class="mobile-btn" onclick={handleMobileSend}>Send</button>
      <button class="mobile-btn" onclick={handleMobilePaste}>Paste</button>
      <button class="mobile-btn" onclick={handleMobileCopy}>Copy</button>
    </div>
  {/if}
</div>

<style>
  .terminal-view {
    display: flex;
    flex-direction: column;
    height: calc(100vh - 60px);
    position: relative;
  }

  .term-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 8px;
    flex-shrink: 0;
  }

  .term-title {
    font-size: 0.85rem;
    font-weight: 500;
    color: var(--text);
  }

  .term-btn {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 4px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.75rem;
    font-weight: 500;
    cursor: pointer;
    white-space: nowrap;
  }

  .term-header-actions {
    display: flex;
    gap: 6px;
    align-items: center;
  }

  .term-btn:hover { background: var(--border); }

  .term-container {
    flex: 1;
    min-height: 0;
    overflow: hidden;
    border-radius: var(--radius, 8px);
    padding: 4px;
  }

  /* URL Bar */
  .url-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    margin-bottom: 4px;
  }

  .url-input {
    flex: 1;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 4px 8px;
    color: var(--text);
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.75rem;
    cursor: text;
  }

  .url-open {
    color: var(--accent);
    text-decoration: none;
    font-size: 0.8rem;
    font-weight: 500;
    white-space: nowrap;
  }

  .url-close {
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    font-size: 1.1rem;
    padding: 0 4px;
  }

  /* Mobile input */
  .mobile-input-bar {
    display: flex;
    gap: 6px;
    padding: 8px;
    background: var(--bg-card);
    border-top: 1px solid var(--border);
  }

  .mobile-input {
    flex: 1;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
  }

  .mobile-btn {
    padding: 6px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-size: 0.75rem;
    font-weight: 500;
    cursor: pointer;
    white-space: nowrap;
  }

  .mobile-btn:active {
    background: var(--border);
  }
</style>
