export type ChatFontSize = 'compact' | 'default' | 'large' | 'x-large'

export interface ChatFontPreset {
  label: string
  bodyPx: number
  metaPx: number
  monoScale: number
}

const STORAGE_KEY = 'alf-chat-font-size'
const DEFAULT_CHAT_FONT_SIZE: ChatFontSize = 'default'

export const CHAT_FONT_PRESETS: Record<ChatFontSize, ChatFontPreset> = {
  compact: { label: 'Compact', bodyPx: 12, metaPx: 10, monoScale: 0.82 },
  default: { label: 'Default', bodyPx: 13, metaPx: 11, monoScale: 0.82 },
  large: { label: 'Large', bodyPx: 15, metaPx: 12, monoScale: 0.84 },
  'x-large': { label: 'Extra Large', bodyPx: 17, metaPx: 13, monoScale: 0.86 },
}

class ChatUIStore {
  fontSize = $state<ChatFontSize>(DEFAULT_CHAT_FONT_SIZE)

  constructor() {
    const saved = localStorage.getItem(STORAGE_KEY) as ChatFontSize | null
    this.fontSize = saved && CHAT_FONT_PRESETS[saved] ? saved : DEFAULT_CHAT_FONT_SIZE
    this.apply()
  }

  setFontSize(size: ChatFontSize) {
    this.fontSize = CHAT_FONT_PRESETS[size] ? size : DEFAULT_CHAT_FONT_SIZE
    localStorage.setItem(STORAGE_KEY, this.fontSize)
    this.apply()
  }

  private apply() {
    if (typeof document === 'undefined') return
    const preset = CHAT_FONT_PRESETS[this.fontSize]
    const root = document.documentElement
    root.style.setProperty('--alf-chat-font-size', `${preset.bodyPx}px`)
    root.style.setProperty('--alf-chat-meta-font-size', `${preset.metaPx}px`)
    root.style.setProperty('--alf-chat-mono-scale', String(preset.monoScale))
  }
}

export const chatUI = new ChatUIStore()
