<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte'
  import { X, MessageCircle, RotateCw, Play, ChevronsDownUp, ChevronsUpDown } from 'lucide-svelte'
  import ChatMessageComponent from '../components/chat/ChatMessage.svelte'
  import ChatInput from '../components/chat/ChatInput.svelte'
  import ConversationTabs from '../components/chat/ConversationTabs.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import Toggle from '../components/shared/Toggle.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { nav } from '../stores/nav.svelte'
  import { sound } from '../stores/sound.svelte'
  import { chatUI } from '../stores/chat-ui.svelte'
  import { events } from '../stores/events.svelte'
  import { convStore } from '../stores/conversations.svelte'
  import { chatRuntimes, type ContentBlock } from '../stores/chat-runtimes.svelte'

  // --- Types ---
  interface ChatMsg {
    id: string
    role: string
    text: string
    ts: string
    seq?: number
    model?: string
    tier?: string
    cost_usd?: number
    duration_ms?: number
    skills?: string[]
    conv_id?: string
    media?: any[]
    reactions?: any[]
    content_blocks?: any[]
  }

  interface Tier {
    name: string
    model: string
    enabled?: boolean
    force_command?: boolean
  }

  // --- Conversation state (from store) ---
  let convId = $derived(convStore.activeConvId)
  let loadGen = $state(0) // generation counter to ignore stale loads

  // --- Messages ---
  let messages = $state<ChatMsg[]>([])

  // Per-conv streaming state (#310). sending, streaming blocks, abort, etc.
  // all live in chatRuntimes so a stream on conv A can't bleed into conv B.
  let sending = $derived(chatRuntimes.isSending(convId ?? ''))

  function setSending(cid: string, value: boolean) {
    if (!cid) return
    chatRuntimes.update(cid, { sending: value })
  }
  function clearSending(cid?: string) {
    const target = cid ?? convId ?? ''
    if (!target) return
    chatRuntimes.update(target, { sending: false })
  }
  let tiers = $state<Tier[]>([])
  let messagesContainer: HTMLDivElement
  let selectedTier = $state(localStorage.getItem('alf-chat-tier') || '')
  let collapseBlocks = $state(localStorage.getItem('alf-chat-collapse') !== 'false')

  // Block visibility filter (#196)
  type ChatFilter = 'all' | 'clean' | 'thinking' | 'tools'
  const savedChatFilter = localStorage.getItem('alf-chat-filter') as ChatFilter | null
  const isValidChatFilter = (value: string | null): value is ChatFilter => (
    value === 'all' || value === 'clean' || value === 'thinking' || value === 'tools'
  )
  let chatFilter = $state<ChatFilter>(isValidChatFilter(savedChatFilter) ? savedChatFilter : 'clean')
  let hideThinking = $derived(chatFilter === 'clean' || chatFilter === 'tools')
  let hideTools = $derived(chatFilter === 'clean' || chatFilter === 'thinking')

  function onFilterChange(e: Event) {
    const detail = (e as CustomEvent).detail
    if (detail?.value) {
      chatFilter = detail.value as ChatFilter
      localStorage.setItem('alf-chat-filter', chatFilter)
    }
  }

  function bindFilterGroup(node: HTMLElement) {
    node.addEventListener('alf-change', onFilterChange)
    return { destroy() { node.removeEventListener('alf-change', onFilterChange) } }
  }
  // Streaming blocks come from the per-conv runtime. Rendering always picks
  // the runtime of the currently-viewed conv, so tab switches never mutate
  // another conv's in-flight stream (#310).
  let streamingBlocks = $derived<ContentBlock[]>(chatRuntimes.get(convId ?? '').blocks)
  let visibleStreamingBlocks = $derived(streamingBlocks.filter(b => {
    if (b.type === 'thinking' && hideThinking) return false
    if ((b.type === 'tool_use' || b.type === 'tool_result') && hideTools) return false
    if (b.type === 'text') return !!(b.text && b.text.trim())
    return true
  }))
  let streamingText = $derived(chatRuntimes.get(convId ?? '').text)
  let showStreaming = $derived(sending && chatRuntimes.get(convId ?? '').jobId !== null)
  let pollTimer: ReturnType<typeof setInterval> | null = null
  type QueueItem = { message: string; mediaFiles: MediaFile[]; model: string }
  let allQueues = $state<Record<string, QueueItem[]>>(loadQueues())
  let messageQueue = $derived(allQueues[convId ?? ''] || [])
  let drafts = $state<Record<string, string>>({})
  let draft = $derived(drafts[convId ?? ''] ?? '')

  function loadQueues(): Record<string, QueueItem[]> {
    sessionStorage.removeItem('alf-chat-queue') // migrate old key
    try { return JSON.parse(sessionStorage.getItem('alf-chat-queues') || '{}') } catch { return {} }
  }
  function persistQueue() {
    sessionStorage.setItem('alf-chat-queues', JSON.stringify(allQueues))
  }
  function pushToQueue(cid: string, item: QueueItem) {
    allQueues = { ...allQueues, [cid]: [...(allQueues[cid] || []), item] }
    persistQueue()
  }
  function shiftQueue(cid: string): QueueItem | undefined {
    const q = allQueues[cid] || []
    if (q.length === 0) return undefined
    const [first, ...rest] = q
    allQueues = { ...allQueues, [cid]: rest }
    persistQueue()
    return first
  }
  function clearQueue(cid: string) {
    const { [cid]: _, ...rest } = allQueues
    allQueues = rest
    persistQueue()
  }
  function removeFromQueue(cid: string, idx: number) {
    allQueues = { ...allQueues, [cid]: (allQueues[cid] || []).filter((_, i) => i !== idx) }
    persistQueue()
  }
  let activeSkills = $state<string[]>([])

  async function loadActiveSkills() {
    try {
      const cid = convId ?? ''
      const data = await api<{ skills: string[] }>(`/api/chat/skills?conv_id=${encodeURIComponent(cid)}`)
      activeSkills = data.skills || []
    } catch { /* silent */ }
  }

  async function dismissSkill(name: string) {
    try {
      const cid = convId ?? ''
      await api(`/api/chat/skills?conv_id=${encodeURIComponent(cid)}&name=${encodeURIComponent(name)}`, { method: 'DELETE' })
      activeSkills = activeSkills.filter(s => s !== name)
    } catch { /* silent */ }
  }

  function updateDraft(text: string) {
    drafts[convId ?? ''] = text
  }

  // Send to agents modal
  let showAgentModal = $state(false)
  let agentModalPrompt = $state('')
  let agentModalTeam = $state('')
  let agentModalValidation = $state(false)
  let agentModalTeams = $state<{ name: string; description?: string }[]>([])
  let agentModalLaunching = $state(false)

  function openAgentModal(text: string) {
    agentModalPrompt = text
    agentModalTeam = ''
    agentModalValidation = false
    agentModalLaunching = false
    // Load teams
    api<{ teams: { name: string; description?: string }[] }>('/api/teams')
      .then(data => { agentModalTeams = data?.teams || [] })
      .catch(() => { agentModalTeams = [] })
    showAgentModal = true
  }

  async function launchAgentTask() {
    agentModalLaunching = true
    const message = agentModalTeam
      ? `[Use team: ${agentModalTeam}]\n${agentModalPrompt}`
      : agentModalPrompt
    try {
      await api('POST', '/api/tasks', { message, need_validation: agentModalValidation })
      toasts.show('Task launched')
      showAgentModal = false
    } catch {
      toasts.show('Failed to launch task', 'error')
    } finally {
      agentModalLaunching = false
    }
  }

  function sortMessages(msgs: ChatMsg[]): ChatMsg[] {
    return msgs.sort((a, b) => {
      // Primary: seq (guaranteed unique per conv). Fallback: timestamp.
      if (a.seq != null && b.seq != null && a.seq !== b.seq) return a.seq - b.seq
      return a.ts.localeCompare(b.ts)
    })
  }

  function setMessages(msgs: ChatMsg[]) {
    messages = sortMessages(msgs)
  }

  function appendMessage(msg: ChatMsg) {
    messages = sortMessages([...messages, msg])
  }

  async function scrollToBottom() {
    await tick()
    if (messagesContainer) {
      requestAnimationFrame(() => {
        messagesContainer.scrollTop = messagesContainer.scrollHeight
      })
    }
  }

  // Tracks whether the user is pinned near the bottom of the thread.
  // Streaming chunks only auto-scroll when true, so scrolling up to read
  // earlier messages during a long response is not interrupted.
  let userAtBottom = $state(true)
  const bottomStickThreshold = 80 // px

  async function scrollToBottomIfFollowing() {
    if (userAtBottom) await scrollToBottom()
  }

  let loadingOlder = $state(false)
  let hasOlderMessages = $state(false)

  async function loadHistory() {
    if (!convId) {
      setMessages([])
      return
    }
    const gen = ++loadGen
    try {
      const data = await api<ChatMsg[]>(`/api/chat?limit=50&conv_id=${convId}`)
      if (gen !== loadGen) return // stale response from rapid switching
      setMessages(data || [])
      hasOlderMessages = (data?.length || 0) >= 50
      scrollToBottom()
    } catch {
      if (gen === loadGen) setMessages([])
    }
  }

  async function loadOlderMessages() {
    if (loadingOlder || !convId || messages.length === 0) return
    loadingOlder = true
    try {
      const oldest = messages[0]
      const data = await api<ChatMsg[]>(`/api/chat?limit=50&conv_id=${convId}&before=${oldest.ts}`)
      if (data && data.length > 0) {
        const container = messagesContainer
        const prevHeight = container?.scrollHeight || 0
        messages = sortMessages([...data, ...messages])
        await tick()
        if (container) {
          container.scrollTop = container.scrollHeight - prevHeight
        }
      }
      hasOlderMessages = (data?.length || 0) >= 50
    } catch { /* ignore */ }
    loadingOlder = false
  }

  function onMessagesScroll(e: Event) {
    const el = e.target as HTMLDivElement
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight
    userAtBottom = distanceFromBottom <= bottomStickThreshold
    if (el.scrollTop < 100 && hasOlderMessages) {
      loadOlderMessages()
    }
  }

  async function loadTiers() {
    try {
      const data = await api<any>('/api/tiers')
      tiers = (data.tiers || []).map((t: any) => ({
        name: t.name,
        model: t.model,
        enabled: t.enabled !== false,
        force_command: !!t.force_command,
      }))
    } catch {
      tiers = []
    }
  }

  // Check for active job on load (reconnect to stream). Dedupes against the
  // per-conv reader so a tab switch doesn't open a duplicate SSE (#310).
  async function checkActiveJob(targetConvId?: string) {
    const cid = targetConvId ?? convId ?? ''
    if (!cid) return
    if (chatRuntimes.hasActiveReader(cid)) return // already streaming
    try {
      const data = await api<any>(`/api/chat/job?conv_id=${cid}`)
      if (data.active && data.job_id) {
        chatRuntimes.update(cid, { jobId: data.job_id, sending: true })
        reconnectToStream(cid, data.job_id, 0)
      }
    } catch { /* no active job */ }
  }

  interface MediaFile {
    upload_id: string
    file_name: string
    mime_type: string
  }

  // --- Send message ---
  async function handleSend(message: string, mediaFiles: MediaFile[], model: string) {
    drafts[convId ?? ''] = ''

    // Client-side command handling
    const trimmed = message.trim()
    if (trimmed === '/new') {
      await newConversation()
      return
    }
    if (trimmed === '/skills') {
      nav.navigateTo('skills')
      return
    }

    // Queue if already sending
    if (sending) {
      pushToQueue(convId ?? '', { message, mediaFiles, model })
      return
    }

    await doSend(message, mediaFiles, model)
  }

  async function doSend(message: string, mediaFiles: MediaFile[], model: string) {
    const sendConvId = convId ?? ''
    if (!sendConvId) return
    const ac = new AbortController()
    chatRuntimes.update(sendConvId, {
      sending: true,
      stoppedByUser: false,
      blocks: [],
      text: '',
      abortController: ac,
    })

    const mediaIds = mediaFiles.map(f => f.upload_id)

    // Add user message optimistically. The backend now persists the user
    // msg synchronously on POST, so refresh before the first token still
    // shows it from history (#310).
    if (message || mediaFiles.length > 0) {
      const maxSeq = messages.reduce((max, m) => Math.max(max, m.seq ?? 0), 0)
      const userMsg: ChatMsg = {
        id: 'temp-' + Date.now(),
        role: 'user',
        text: message,
        ts: new Date().toISOString(),
        seq: maxSeq + 1,
        conv_id: sendConvId,
        media: mediaFiles.map(f => ({ upload_id: f.upload_id, type: f.mime_type?.startsWith('image/') ? 'photo' : 'document', file_name: f.file_name, mime_type: f.mime_type })),
      }
      appendMessage(userMsg)
      scrollToBottom()
    }

    try {
      const body: any = { message, conv_id: sendConvId }
      if (mediaIds.length > 0) body.media_ids = mediaIds
      if (model) body.model = model

      // POST /api/chat returns SSE stream
      const res = await fetch('/api/chat', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Requested-With': 'XMLHttpRequest',
        },
        body: JSON.stringify(body),
        credentials: 'same-origin',
        signal: ac.signal,
      })

      if (res.status === 401) {
        toasts.show('Session expired', 'error')
        clearSending(sendConvId)
        return
      }

      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: 'Send failed' }))
        toasts.show(err.error || 'Send failed', 'error')
        clearSending(sendConvId)
        return
      }

      await readStream(sendConvId, res)
    } catch (e: any) {
      toasts.show(e.message || 'Send failed', 'error')
      clearSending(sendConvId)
    }
  }

  // readStream is scoped to a specific conv so events from a stream started
  // in tab A always write into chatRuntimes[A], even while the user views B.
  async function readStream(cid: string, res: Response) {
    const reader = res.body?.getReader()
    if (!reader) { clearSending(cid); return }

    chatRuntimes.update(cid, { readerActive: true })

    const decoder = new TextDecoder()
    let buffer = ''

    // Seed from existing runtime state so a reconnect doesn't drop prior blocks.
    let currentBlocks: any[] = [...chatRuntimes.get(cid).blocks]
    let currentText = chatRuntimes.get(cid).text
    let pendingEvent = ''

    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (line === '') { pendingEvent = ''; continue }
          if (line.startsWith('event: ')) { pendingEvent = line.slice(7).trim(); continue }
          if (!line.startsWith('data: ')) continue
          const dataStr = line.slice(6)
          let data: any
          try { data = JSON.parse(dataStr) } catch { continue }

          const eventType = pendingEvent || data.type || ''

          if (eventType === 'job') {
            chatRuntimes.update(cid, { jobId: data.job_id })
            continue
          }

          if (eventType === 'error') {
            toasts.show(data.error || data.text || 'Stream error', 'error')
            continue
          }

          if (eventType === 'system') {
            if (data.text && cid === (convId ?? '')) {
              appendMessage({
                id: 'sys-' + Date.now(),
                role: 'system',
                text: data.text,
                ts: new Date().toISOString(),
              })
            }
            continue
          }

          if (eventType === 'done') {
            // Apply metadata only if the user is currently viewing this conv.
            if (cid === (convId ?? '')) {
              const lastAssistant = [...messages].reverse().find(m => m.role === 'assistant')
              if (lastAssistant && data) {
                if (data.model) lastAssistant.model = data.model
                if (data.tier) lastAssistant.tier = data.tier
                if (data.cost_usd) lastAssistant.cost_usd = data.cost_usd
                if (data.duration_ms) lastAssistant.duration_ms = data.duration_ms
                if (data.skills) lastAssistant.skills = data.skills
                messages = [...messages]
              }
              loadActiveSkills()
            }
            continue
          }

          data.type = data.type || eventType
          handleStreamEvent(data, currentBlocks, (updated) => {
            currentBlocks = updated
          }, (text) => {
            currentText = text
          })
        }

        chatRuntimes.update(cid, {
          blocks: [...currentBlocks],
          text: currentText,
        })
        if (cid === (convId ?? '')) scrollToBottomIfFollowing()
      }
    } catch {
      // Stream ended or errored
    } finally {
      const runtime = chatRuntimes.get(cid)
      const finalText = runtime.text
      const wasStopped = runtime.stoppedByUser

      chatRuntimes.update(cid, { readerActive: false, abortController: null })

      // If stopCall cleaned up already, don't double-work.
      if (wasStopped) {
        chatRuntimes.resetStream(cid)
        return
      }

      chatRuntimes.resetStream(cid)
      // Small delay so the server has committed the assistant msg.
      await new Promise(r => setTimeout(r, 100))
      // Only reload the visible conv's history; other convs will refresh
      // when the user switches to them.
      if (cid === (convId ?? '')) {
        await loadHistory()
        scrollToBottom()
      }

      // Notifications: only chime for the foreground conv; background
      // convs just bump the tab spinner + unread badge via other events.
      const isForeground = cid === (convId ?? '') && !document.hidden
      if (document.hidden && 'Notification' in window && Notification.permission === 'granted' && finalText) {
        new Notification('ALF', { body: finalText.slice(0, 100) })
      }
      if (finalText && isForeground) sound.play()
      if (nav.currentView !== 'chat') {
        nav.incrementBadge('chat')
      }

      // Process queue for this conv only.
      const next = shiftQueue(cid)
      if (next) {
        doSend(next.message, next.mediaFiles, next.model)
      }
    }
  }

  async function reconnectToStream(cid: string, jobId: string, offset: number) {
    if (chatRuntimes.hasActiveReader(cid)) return // dedupe
    try {
      const res = await fetch(`/api/chat/job?stream=${jobId}&offset=${offset}`, {
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
      })
      if (!res.ok) {
        clearSending(cid)
        return
      }
      await readStream(cid, res)
    } catch {
      clearSending(cid)
    }
  }

  function handleStreamEvent(
    data: any,
    blocks: any[],
    setBlocks: (b: any[]) => void,
    setText: (t: string) => void,
  ) {
    // The SSE stream sends events like:
    // event: content_block_start → data: { type, index, content_block: { type, ... } }
    // event: content_block_delta → data: { type, index, delta: { type, text } }
    // event: content_block_stop → data: { type, index }
    // event: message_stop → data: { ... done data }
    // event: error → data: { error: "..." }

    const type = data.type

    if (type === 'content_block_start') {
      const block = data.content_block || {}
      blocks.push({
        type: block.type || 'text',
        text: '',
        name: block.name || '',
        input: block.input || null,
        thinking: '',
      })
      setBlocks([...blocks])
    } else if (type === 'content_block_delta') {
      const idx = data.index ?? (blocks.length - 1)
      if (idx >= 0 && idx < blocks.length) {
        const delta = data.delta || {}
        if (delta.type === 'text_delta' && delta.text) {
          blocks[idx].text = (blocks[idx].text || '') + delta.text
        } else if (delta.type === 'thinking_delta' && delta.thinking) {
          blocks[idx].thinking = (blocks[idx].thinking || '') + delta.thinking
        } else if (delta.type === 'input_json_delta' && delta.partial_json) {
          const prev = typeof blocks[idx].input === 'string' ? blocks[idx].input : ''
          blocks[idx].input = prev + delta.partial_json
        }
        setBlocks([...blocks])
      }
    } else if (type === 'content_block_stop') {
      // Try to parse JSON input for tool_use blocks
      const idx = data.index ?? (blocks.length - 1)
      if (idx >= 0 && idx < blocks.length && blocks[idx].type === 'tool_use') {
        try {
          if (typeof blocks[idx].input === 'string') {
            blocks[idx].input = JSON.parse(blocks[idx].input)
          }
        } catch { /* keep as string */ }
      }
      setBlocks([...blocks])
    } else if (type === 'text_delta') {
      // Normalized event from comms pipeline (Codex, CLI, etc.)
      const delta = data.text || data.data?.text || ''
      if (delta) {
        if (blocks.length === 0 || blocks[blocks.length - 1].type !== 'text') {
          blocks.push({ type: 'text', text: '', thinking: '' })
        }
        blocks[blocks.length - 1].text += delta
        setBlocks([...blocks])
      }
    } else if (type === 'text') {
      // Final text event from comms pipeline — only use it if no blocks
      // were built during streaming (text_delta events already populated
      // text blocks). Adding it when blocks exist would duplicate content
      // and may expose raw tool-call XML (#124).
      const text = data.text || data.data?.text || ''
      if (text && blocks.length === 0) {
        blocks.push({ type: 'text', text: '', thinking: '' })
        blocks[blocks.length - 1].text = text
        setBlocks([...blocks])
      }
    } else if (type === 'thinking') {
      // Normalized thinking event from comms pipeline
      const text = data.text || data.data?.text || ''
      if (text) {
        if (blocks.length === 0 || blocks[blocks.length - 1].type !== 'thinking') {
          blocks.push({ type: 'thinking', text: '', thinking: '' })
        }
        blocks[blocks.length - 1].thinking += text
        setBlocks([...blocks])
      }
    } else if (type === 'tool_use') {
      // Normalized tool_use start from comms pipeline
      blocks.push({ type: 'tool_use', text: '', name: data.name || '', input: '', thinking: '' })
      setBlocks([...blocks])
    } else if (type === 'tool_input') {
      // Normalized tool input chunk from comms pipeline
      const last = blocks[blocks.length - 1]
      if (last && last.type === 'tool_use') {
        last.input = (typeof last.input === 'string' ? last.input : '') + (data.chunk || '')
        setBlocks([...blocks])
      }
    } else if (type === 'tool_result') {
      // Normalized tool result from comms pipeline
      const last = blocks[blocks.length - 1]
      if (last && last.type === 'tool_use') {
        try {
          if (typeof last.input === 'string') last.input = JSON.parse(last.input)
        } catch { /* keep as string */ }
      }
      setBlocks([...blocks])
    } else if (type === 'message_delta' || type === 'message_stop') {
      // Done event — may contain cost, model, etc.
      // No action needed; stream will end and we reload history
    } else if (type === 'error' || data.error) {
      toasts.show(data.error || data.text || 'Stream error', 'error')
    }

    // Build aggregate text for simple display
    const fullText = blocks
      .filter(b => b.type === 'text')
      .map(b => b.text)
      .join('')
    setText(fullText)
  }

  // --- Stop active call (instant) ---
  // Only affects the conv currently viewed — other convs keep streaming.
  // The pending queue is preserved so the user's prepared follow-ups aren't
  // silently lost when they cancel a single response (#310 UX).
  function stopCall() {
    const cid = convId ?? ''
    if (!cid) return
    const runtime = chatRuntimes.get(cid)
    runtime.abortController?.abort()
    chatRuntimes.update(cid, {
      stoppedByUser: true,
      sending: false,
      abortController: null,
      readerActive: false,
      blocks: [],
      text: '',
      jobId: null,
    })
    api('DELETE', `/api/chat/job?conv_id=${encodeURIComponent(cid)}`)
      .catch(() => {})
      .finally(() => {
        if ((convId ?? '') === cid) loadHistory().then(() => scrollToBottom())
      })
  }

  // --- New conversation ---
  async function newConversation() {
    const id = await convStore.create()
    if (id) {
      setMessages([])
      toasts.show('New conversation started', 'success')
    } else {
      toasts.show('Failed to start new conversation', 'error')
    }
  }

  // Scroll to bottom when navigating back to chat view
  $effect(() => {
    if (nav.currentView === 'chat') {
      scrollToBottom()
    }
  })

  // React to conversation switches.
  // In-flight streams on other convs keep running inside chatRuntimes — this
  // effect only rebinds the currently-viewed conv's display (#310).
  let prevConvId = ''
  $effect(() => {
    const id = convStore.activeConvId
    if (id && id !== prevConvId && convStore.loaded) {
      prevConvId = id
      convStore.clearUnread(id)
      setMessages([]) // clear immediately to avoid stale flash
      loadActiveSkills()
      loadHistory().then(() => {
        scrollToBottom()
        checkActiveJob(id) // reconnect this conv's reader if none is active
      })
    }
  })

  // --- Lifecycle ---
  let unsubTiers: (() => void) | null = null
  let unsubNewMsg: (() => void) | null = null
  let unsubActiveConv: (() => void) | null = null

  onMount(async () => {
    chatUI.setFontSize(chatUI.fontSize)
    // Request notification permission
    if ('Notification' in window && Notification.permission === 'default') {
      Notification.requestPermission()
    }
    // Load conversations from store (sets activeConvId).
    await convStore.load()
    await loadTiers()
    loadActiveSkills()
    unsubTiers = events.subscribe('tiers', () => loadTiers())
    unsubActiveConv = events.subscribe('active_conv', (data) => {
      try {
        const parsed = JSON.parse(data || '{}')
        if (parsed.client_id === convStore.clientId) return // echo suppression
        if (parsed.conv_id && parsed.conv_id !== convStore.activeConvId) {
          convStore.switchTo(parsed.conv_id)
        }
      } catch {}
    })
    unsubNewMsg = events.subscribe('new_message', (data) => {
      // Parse JSON payload with conv_id
      let msgConvId = convId
      try {
        const parsed = JSON.parse(data || '{}')
        if (parsed.conv_id) msgConvId = parsed.conv_id
      } catch {}

      if (msgConvId !== convId) {
        // Message for a different conversation — mark unread
        convStore.markUnread(msgConvId)
        return
      }

      if (!convId) return
      // Fetch latest messages and append any new ones (safe during streaming).
      const gen = loadGen
      const fetchConvId = convId
      api<ChatMsg[]>(`/api/chat?limit=5&conv_id=${fetchConvId}`).then(recent => {
        if (gen !== loadGen) return // stale: conversation switched
        if (!recent?.length) return
        const existingIds = new Set(messages.map(m => m.id))
        const newMsgs = recent.filter(m => !existingIds.has(m.id) && m.role === 'assistant')
        if (newMsgs.length > 0) {
          messages = sortMessages([...messages, ...newMsgs])
          scrollToBottom()
        }
      }).catch(() => {})
    })
    if (convId) {
      await loadHistory()
    }
    // Rebind every active job across all convs so the tab spinners light up
    // immediately after refresh and streams resume without opening dupes (#310).
    try {
      const data = await api<{ jobs: { conv_id: string; job_id: string; events: number }[] }>('/api/chat/jobs')
      for (const j of data.jobs || []) {
        if (!j.conv_id || !j.job_id) continue
        chatRuntimes.update(j.conv_id, { jobId: j.job_id, sending: true })
        reconnectToStream(j.conv_id, j.job_id, 0)
      }
    } catch { /* fall back to single-conv check */
      await checkActiveJob()
    }

    // Poll every 2s: sync active conv from other devices + fetch new messages.
    pollTimer = setInterval(() => {
      if (!convId) return
      // Sync active conversation from server (skip if we just switched locally).
      if (Date.now() - convStore.lastLocalSwitch > 5000) {
        api<any>('/api/chat/active').then(data => {
          const serverConv = data?.active_conv_id
          if (serverConv && serverConv !== convStore.activeConvId) {
            convStore.switchTo(serverConv)
          }
        }).catch(() => {})
      }
      if (sending) return
      const gen = loadGen
      const fetchConvId = convId
      api<ChatMsg[]>(`/api/chat?limit=5&conv_id=${fetchConvId}`).then(recent => {
        if (gen !== loadGen) return // stale: conversation switched
        if (!recent?.length) return
        const existingIds = new Set(messages.map(m => m.id))
        const newMsgs = recent.filter(m => !existingIds.has(m.id))
        if (newMsgs.length > 0) {
          messages = sortMessages([...messages, ...newMsgs])
          scrollToBottom()
        }
      }).catch(() => {})
    }, 2000)
  })

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer)
    unsubTiers?.()
    unsubNewMsg?.()
    unsubActiveConv?.()
  })
</script>

<div class="chat-view">
  <!-- Header -->
  <div class="chat-header">
    <button class="btn btn-ghost btn-sm" onclick={newConversation} title="New conversation">
      <RotateCw size={14} />
      New
    </button>
    <div class="chat-header-spacer"></div>
    <div use:bindFilterGroup>
      <alf-btn-group value={chatFilter}>
        <button class="btn btn-sm" data-value="all">All</button>
        <button class="btn btn-sm" data-value="clean">Clean</button>
        <button class="btn btn-sm" data-value="thinking">Thinking</button>
        <button class="btn btn-sm" data-value="tools">Tools</button>
      </alf-btn-group>
    </div>
    <button
      class="btn btn-icon btn-sm"
      onclick={() => { collapseBlocks = !collapseBlocks; localStorage.setItem('alf-chat-collapse', String(collapseBlocks)) }}
      title={collapseBlocks ? 'Expand all blocks' : 'Collapse all blocks'}
    >
      {#if collapseBlocks}
        <ChevronsUpDown size={16} />
      {:else}
        <ChevronsDownUp size={16} />
      {/if}
    </button>
  </div>

  <!-- Conversation Tabs -->
  <ConversationTabs />

  <!-- Messages -->
  <div class="chat-messages" bind:this={messagesContainer} onscroll={onMessagesScroll}>
    {#if convId}
      <div class="conv-debug-label">{convId}</div>
    {/if}
    {#if loadingOlder}
      <div class="loading-older">Loading older messages...</div>
    {/if}

    {#if messages.length === 0 && !sending}
      <div class="chat-empty">
        <MessageCircle size={32} />
        <p>No messages yet. Start a conversation.</p>
      </div>
    {/if}

    {#each messages as msg (msg.id)}
      {#if msg.role === 'assistant' && msg.content_blocks && msg.content_blocks.length > 1}
        {@const visibleBlocks = msg.content_blocks.filter(b => {
          if (b.type === 'thinking' && hideThinking) return false
          if ((b.type === 'tool_use' || b.type === 'tool_result') && hideTools) return false
          if (b.type === 'text') return b.text && b.text.trim()
          if (b.type === 'tool_result') return (b.content || b.text || '').trim()
          return true
        })}
        {#if visibleBlocks.length > 0}
        {#each visibleBlocks as block, bi (bi)}
          {@const isLast = bi === visibleBlocks.length - 1}
          <ChatMessageComponent
            msg={{
              ...msg,
              id: `${msg.id}-block-${bi}`,
              text: block.type === 'text' ? (block.text || '') : '',
              content_blocks: [block],
              // Only show footer metadata + timestamp on the last block.
              // Non-last blocks share the parent msg.ts — displaying it on
              // every bubble made them all look identical (#310).
              ts: isLast ? msg.ts : '',
              model: isLast ? msg.model : undefined,
              tier: isLast ? msg.tier : undefined,
              cost_usd: isLast ? msg.cost_usd : undefined,
              duration_ms: isLast ? msg.duration_ms : undefined,
              skills: isLast ? msg.skills : undefined,
              reactions: isLast ? msg.reactions : undefined,
            }}
            {convId}
            {collapseBlocks} {hideThinking} {hideTools}
            onSendToTask={isLast ? openAgentModal : undefined}
          />
        {/each}
        {/if}
      {:else}
        {@const isEmpty = (() => {
          const text = (msg.text || '').trim()
          const blocks = msg.content_blocks || []
          // User messages: empty if no text and no media
          if (msg.role === 'user') return !text && (!msg.media || msg.media.length === 0)
          // System messages: empty if no text
          if (msg.role === 'system') return !text
          // Assistant: empty if no visible text and all blocks are hidden/empty
          if (!text && blocks.length === 0) return true
          return blocks.length > 0 && blocks.every(b => {
            if (b.type === 'thinking' && hideThinking) return true
            if ((b.type === 'tool_use' || b.type === 'tool_result') && hideTools) return true
            if (b.type === 'text') return !b.text?.trim()
            if (b.type === 'tool_result') return !(b.content || b.text || '').trim()
            return false
          })
        })()}
        {#if !isEmpty}
          <ChatMessageComponent {msg} {convId} {collapseBlocks} {hideThinking} {hideTools} onSendToTask={openAgentModal} />
        {/if}
      {/if}
    {/each}

    <!-- Streaming response — each block rendered as its own bubble (#127) -->
    {#if showStreaming && visibleStreamingBlocks.length > 0}
      {#each visibleStreamingBlocks as block, i (i)}
        <ChatMessageComponent
          msg={{
            id: `streaming-${i}`,
            role: 'assistant',
            text: block.type === 'text' ? (block.text || '') : '',
            ts: new Date().toISOString(),
            content_blocks: [block],
          }}
          {convId}
          {collapseBlocks} {hideThinking} {hideTools}
        />
      {/each}
    {:else if showStreaming}
      <div class="chat-msg chat-msg-assistant typing-indicator">
        <span class="dot"></span>
        <span class="dot"></span>
        <span class="dot"></span>
      </div>
    {/if}

    <!-- Queued messages -->
    {#each messageQueue as queued, i}
      <div class="chat-msg chat-msg-user queued-msg">
        <div class="msg-text">{queued.message}</div>
        <div class="queued-footer">
          <span class="queued-badge">queued #{i + 1}</span>
          <button class="queued-cancel" onclick={() => removeFromQueue(convId ?? '', i)} title="Cancel queued message">
            <X size={12} /> cancel
          </button>
        </div>
      </div>
    {/each}
  </div>

  <!-- Input -->
  <ChatInput onSend={handleSend} onStop={stopCall} {sending} {tiers} {draft} onDraftChange={updateDraft} selectedModel={selectedTier} onModelChange={(m) => { selectedTier = m; localStorage.setItem('alf-chat-tier', m) }} {activeSkills} onDismissSkill={dismissSkill} {convId} />
</div>

<!-- Send to Agents Modal -->
<Modal open={showAgentModal} onclose={() => showAgentModal = false}>
  <h3 style="margin:0 0 12px">Send to Agents</h3>
  <div class="agent-modal-form">
    <div class="form-group">
      <label for="agent-prompt">Prompt</label>
      <textarea id="agent-prompt" class="input" rows="4" bind:value={agentModalPrompt}></textarea>
    </div>
    <div class="form-group">
      <label for="agent-team">Team</label>
      <select id="agent-team" class="input" bind:value={agentModalTeam}>
        <option value="">Auto (best fit)</option>
        {#each agentModalTeams as team}
          <option value={team.name}>{team.name}{team.description ? ` - ${team.description}` : ''}</option>
        {/each}
      </select>
    </div>
    <div class="form-group">
      <Toggle bind:checked={agentModalValidation} label="Review plan before execution" />
    </div>
    <div class="agent-modal-actions">
      <button class="btn btn-secondary" onclick={() => showAgentModal = false}>Cancel</button>
      <button class="btn btn-primary" onclick={launchAgentTask} disabled={agentModalLaunching}>
        <Play size={14} /> {agentModalLaunching ? 'Launching...' : 'Launch'}
      </button>
    </div>
  </div>
</Modal>

<style>
  .conv-debug-label {
    position: sticky;
    top: 0;
    left: 0;
    z-index: 5;
    padding: 2px 8px;
    font-size: 10px;
    font-family: monospace;
    color: var(--text-muted, #888);
    opacity: 0.6;
  }
  .chat-view {
    display: flex;
    flex-direction: column;
    height: 100vh;
    max-height: 100vh;
  }

  /* Header */
  .chat-header {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .chat-header-spacer {
    flex: 1;
  }

  /* Messages */
  .chat-messages {
    flex: 1;
    overflow-y: auto;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .chat-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    color: var(--text-dim);
    gap: 12px;
    padding: 40px;
  }

  .chat-empty p {
    font-size: var(--font-sm, 13px);
  }

  .loading-older {
    text-align: center;
    padding: 8px;
    color: var(--text-dim);
    font-size: var(--font-sm, 13px);
  }

  /* Typing indicator */
  .typing-indicator {
    display: flex;
    gap: 4px;
    padding: 12px 16px;
    align-self: flex-start;
    background: var(--bg-card);
    border: none;
    border-radius: 12px;
    border-bottom-left-radius: 4px;
  }

  .dot {
    width: 6px;
    height: 6px;
    background: var(--text-dim);
    border-radius: 50%;
    animation: bounce 1.4s infinite ease both;
  }

  .dot:nth-child(1) { animation-delay: -0.32s; }
  .dot:nth-child(2) { animation-delay: -0.16s; }

  @keyframes bounce {
    0%, 80%, 100% { transform: scale(0.6); opacity: 0.4; }
    40% { transform: scale(1); opacity: 1; }
  }

  /* Queued messages */
  .queued-msg {
    opacity: 0.7;
  }

  .queued-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 6px;
    padding-top: 4px;
    border-top: 1px solid color-mix(in srgb, var(--text) 15%, transparent);
    gap: 8px;
  }

  .queued-badge {
    font-size: var(--font-xs, 11px);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    opacity: 0.7;
  }

  .queued-cancel {
    display: flex;
    align-items: center;
    gap: 3px;
    background: none;
    border: none;
    color: inherit;
    opacity: 0.5;
    cursor: pointer;
    padding: 2px 6px;
    border-radius: 4px;
    font-size: var(--font-xs, 11px);
    transition: opacity 0.15s, background 0.15s;
  }

  .queued-cancel:hover {
    opacity: 1;
    background: color-mix(in srgb, var(--text) 15%, transparent);
  }

  /* Agent modal */
  .agent-modal-form {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .agent-modal-form .form-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .agent-modal-form label {
    font-size: var(--font-sm, 13px);
    font-weight: 500;
  }

  .agent-modal-form textarea,
  .agent-modal-form select {
    width: 100%;
    box-sizing: border-box;
  }

  .agent-modal-form .checkbox-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
  }

  .agent-modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 4px;
  }

  @media (max-width: 768px) {
    .chat-view {
      height: calc(100dvh - 36px - env(safe-area-inset-top, 0px));
      max-height: calc(100dvh - 36px - env(safe-area-inset-top, 0px));
      margin-bottom: 0;
    }

    .chat-messages {
      padding: 8px;
    }
  }
</style>
