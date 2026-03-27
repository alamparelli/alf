const STORAGE_KEY = 'spotlight:shortcut'
const DEFAULT_KEY = 'g'

class SpotlightSettings {
  shortcutKey = $state(DEFAULT_KEY)

  constructor() {
    try {
      const saved = localStorage.getItem(STORAGE_KEY)
      if (saved && saved.length === 1) this.shortcutKey = saved
    } catch { /* silent */ }
  }

  setKey(key: string) {
    if (!key || key.length !== 1) return
    this.shortcutKey = key.toLowerCase()
    try {
      localStorage.setItem(STORAGE_KEY, this.shortcutKey)
    } catch { /* silent */ }
  }
}

export const spotlightSettings = new SpotlightSettings()
