// Global SSE event bus. Single EventSource connection to /api/events.
// Views subscribe to typed events and reload their data on push.

type EventCallback = (data?: string) => void

class EventBus {
  private es: EventSource | null = null
  private subs = new Map<string, Set<EventCallback>>()
  private reconnectTimer: ReturnType<typeof setTimeout> | undefined
  private reconnectDelay = 1000
  connected = $state(false)

  connect() {
    if (this.es) return
    const es = new EventSource('/api/events')

    es.addEventListener('ping', () => {
      this.connected = true
      this.reconnectDelay = 1000
    })

    // Register listeners for all known event types.
    const types = [
      'schedules', 'tasks', 'firewall', 'apps',
      'marketplace', 'vault', 'config', 'tiers',
      'tools', 'skills', 'agents', 'new_message', 'active_conv', 'avatar',
      'claude_models'
    ]
    for (const type of types) {
      es.addEventListener(type, (e: MessageEvent) => this.dispatch(type, e.data))
    }

    es.onerror = () => {
      this.connected = false
      es.close()
      this.es = null
      this.reconnectTimer = setTimeout(
        () => this.connect(),
        this.reconnectDelay
      )
      this.reconnectDelay = Math.min(this.reconnectDelay * 2, 30_000)
    }

    this.es = es
  }

  disconnect() {
    if (this.es) {
      this.es.close()
      this.es = null
    }
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = undefined
    }
    this.connected = false
  }

  /** Subscribe to an event type. Returns an unsubscribe function. */
  subscribe(type: string, cb: EventCallback): () => void {
    if (!this.subs.has(type)) this.subs.set(type, new Set())
    this.subs.get(type)!.add(cb)
    return () => { this.subs.get(type)?.delete(cb) }
  }

  private dispatch(type: string, data?: string) {
    const cbs = this.subs.get(type)
    if (cbs) for (const cb of cbs) cb(data)
  }
}

export const events = new EventBus()
