// Per-conversation runtime state for chat streams (#310).
//
// Prior to this store, ChatView held streaming state (jobId, blocks, text,
// abortController, etc.) as a single set of fields on the component, which
// meant switching tabs or sending on a different conv would overwrite state
// belonging to an in-flight stream. This caused the two symptoms on #310:
//  - the "running" indicator stuck or flipped to the wrong tab
//  - duplicate SSE reconnects on tab switch
//
// This store keeps one record per conv_id so streams run independently.

export interface ContentBlock {
  type: string
  text?: string
  thinking?: string
  name?: string
  input?: any
  content?: string
}

export interface RuntimeState {
  jobId: string | null
  blocks: ContentBlock[]
  text: string
  sending: boolean
  abortController: AbortController | null
  readerActive: boolean
  stoppedByUser: boolean
}

function emptyRuntime(): RuntimeState {
  return {
    jobId: null,
    blocks: [],
    text: '',
    sending: false,
    abortController: null,
    readerActive: false,
    stoppedByUser: false,
  }
}

class ChatRuntimesStore {
  // Keyed by convId. Using a plain object so $state reactivity triggers
  // on replacement assignments (we re-create the map on every write).
  runtimes = $state<Record<string, RuntimeState>>({})

  get(convId: string): RuntimeState {
    return this.runtimes[convId] ?? emptyRuntime()
  }

  ensure(convId: string): RuntimeState {
    if (!this.runtimes[convId]) {
      this.runtimes = { ...this.runtimes, [convId]: emptyRuntime() }
    }
    return this.runtimes[convId]
  }

  update(convId: string, patch: Partial<RuntimeState>) {
    const current = this.runtimes[convId] ?? emptyRuntime()
    this.runtimes = { ...this.runtimes, [convId]: { ...current, ...patch } }
  }

  clear(convId: string) {
    const { [convId]: _, ...rest } = this.runtimes
    this.runtimes = rest
  }

  /** Reset streaming artifacts for a conv but keep the record so reactive
   *  readers don't churn. Used when a stream completes. */
  resetStream(convId: string) {
    this.update(convId, {
      jobId: null,
      blocks: [],
      text: '',
      sending: false,
      abortController: null,
      readerActive: false,
      stoppedByUser: false,
    })
  }

  isSending(convId: string): boolean {
    return !!this.runtimes[convId]?.sending
  }

  hasActiveReader(convId: string): boolean {
    return !!this.runtimes[convId]?.readerActive
  }

  /** IDs of every conv currently streaming. Drives tab spinners. */
  activeConvIds(): string[] {
    return Object.keys(this.runtimes).filter(id => this.runtimes[id]?.sending)
  }
}

export const chatRuntimes = new ChatRuntimesStore()
