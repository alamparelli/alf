import { api } from '../lib/api'

class SoundNotifier {
  private ctx: AudioContext | null = null
  private buffer: AudioBuffer | null = null
  private unlocked = false
  private bufferLoaded = false
  enabled = $state(true)

  constructor() {
    this.loadSetting()
    if (typeof document !== 'undefined') {
      // Mobile browsers require AudioContext created + resumed during a user gesture.
      // We keep listening until we successfully unlock — a single failed attempt
      // (e.g. iOS sometimes refuses the first touchstart) shouldn't stop retrying.
      const unlock = () => {
        try {
          if (!this.ctx) this.ctx = new AudioContext()
          const done = () => {
            if (!this.unlocked) {
              this.unlocked = true
              this.loadBuffer()
              removeListeners()
            }
          }
          if (this.ctx.state === 'suspended') {
            this.ctx.resume()
            // Fallback: poll state — resume() promise can hang on iOS webviews
            const check = setInterval(() => {
              if (this.ctx && this.ctx.state === 'running') {
                clearInterval(check)
                done()
              }
            }, 50)
            this.ctx.resume().then(() => {
              clearInterval(check)
              done()
            }).catch(() => { /* will retry on next gesture */ })
            setTimeout(() => clearInterval(check), 5000)
          } else {
            done()
          }
        } catch {
          // AudioContext constructor failed — will retry next gesture
        }
      }
      const removeListeners = () => {
        document.removeEventListener('click', unlock)
        document.removeEventListener('touchstart', unlock)
        document.removeEventListener('keydown', unlock)
      }
      document.addEventListener('click', unlock)
      document.addEventListener('touchstart', unlock)
      document.addEventListener('keydown', unlock)
    }
  }

  private async loadSetting() {
    try {
      const cfg = await api<any>('GET', '/api/config')
      this.enabled = cfg.notification_sound !== false
    } catch {
      // default on
    }
  }

  private async loadBuffer() {
    if (this.bufferLoaded || !this.ctx) return
    try {
      const resp = await fetch('/static/notification.wav')
      if (!resp.ok) {
        console.warn('[sound] failed to fetch notification.wav:', resp.status)
        return
      }
      const data = await resp.arrayBuffer()
      this.buffer = await this.ctx.decodeAudioData(data)
      this.bufferLoaded = true
    } catch (e) {
      console.warn('[sound] buffer load failed:', e)
    }
  }

  async play() {
    if (!this.enabled || !this.unlocked) return
    if (!this.ctx) return

    // Ensure context is running (iOS can suspend it when tab is backgrounded)
    if (this.ctx.state === 'suspended') {
      try { await this.ctx.resume() } catch { return }
    }

    // Load buffer if not yet loaded (race: unlock happened but fetch was slow)
    if (!this.buffer) {
      await this.loadBuffer()
      if (!this.buffer) return
    }

    const src = this.ctx.createBufferSource()
    src.buffer = this.buffer
    const gain = this.ctx.createGain()
    gain.gain.value = 0.5
    src.connect(gain)
    gain.connect(this.ctx.destination)
    src.start()
  }

  async toggle() {
    this.enabled = !this.enabled
    await this.persist()
  }

  async persist() {
    try {
      const cfg = await api<any>('GET', '/api/config')
      cfg.notification_sound = this.enabled
      delete cfg.backends
      await api('PUT', '/api/config', cfg)
    } catch {
      // silent
    }
  }
}

export const sound = new SoundNotifier()
