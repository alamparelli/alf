import { api } from '../lib/api'

class SoundNotifier {
  private ctx: AudioContext | null = null
  private buffer: AudioBuffer | null = null
  private ready: Promise<void> | null = null
  enabled = $state(true)

  constructor() {
    this.loadSetting()
    if (typeof document !== 'undefined') {
      // iOS PWA requires AudioContext created during a user gesture.
      // touchstart is critical — iOS doesn't always fire click on first tap.
      const unlock = () => {
        if (!this.ctx) {
          this.ctx = new AudioContext()
        }
        if (this.ctx.state === 'suspended') {
          this.ctx.resume()
        }
        this.ensureReady()
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

  private ensureReady(): Promise<void> {
    if (this.ready) return this.ready
    this.ready = (async () => {
      try {
        if (!this.ctx) this.ctx = new AudioContext()
        if (this.ctx.state === 'suspended') await this.ctx.resume()
        const resp = await fetch('/static/notification.wav')
        if (!resp.ok) {
          console.warn('[sound] failed to fetch notification.wav:', resp.status)
          this.ready = null // allow retry
          return
        }
        const data = await resp.arrayBuffer()
        this.buffer = await this.ctx.decodeAudioData(data)
      } catch (e) {
        console.warn('[sound] init failed:', e)
        this.ready = null // allow retry
      }
    })()
    return this.ready
  }

  async play() {
    if (!this.enabled) return
    await this.ensureReady()
    if (!this.ctx || !this.buffer) return
    if (this.ctx.state === 'suspended') {
      try { await this.ctx.resume() } catch { return }
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
