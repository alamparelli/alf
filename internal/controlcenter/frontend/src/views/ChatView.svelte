<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte'
  import { Plus, X, MessageCircle, RotateCw, ListX, Play } from 'lucide-svelte'
  import ChatMessageComponent from '../components/chat/ChatMessage.svelte'
  import ChatInput from '../components/chat/ChatInput.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { nav } from '../stores/nav.svelte'
  import { sound } from '../stores/sound.svelte'
  import { events } from '../stores/events.svelte'

  // --- Types ---
  interface ChatTab {
    id: string
    label: string
    convId: string
    unread: number
  }

  interface ChatMsg {
    id: string
    role: string
    text: string
    ts: string
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
  }

  // --- Tab state ---
  let tabs = $state<ChatTab[]>(loadTabs())
  let activeTabId = $state(loadActiveTabId(tabs))

  let activeTab = $derived(tabs.find(t => t.id === activeTabId))

  function loadTabs(): ChatTab[] {
    try {
      const stored = localStorage.getItem('alf-chat-tabs')
      if (stored) {
        const parsed = JSON.parse(stored)
        if (Array.isArray(parsed)) return parsed
      }
    } catch { /* ignore */ }
    return []
  }

  function loadActiveTabId(tabs: ChatTab[]): string {
    try {
      const stored = localStorage.getItem('alf-chat-active-tab')
      if (stored && tabs.some(t => t.id === stored)) return stored
    } catch { /* ignore */ }
    return tabs[0]?.id || ''
  }

  function saveTabs() {
    localStorage.setItem('alf-chat-tabs', JSON.stringify(tabs))
    localStorage.setItem('alf-chat-active-tab', activeTabId)
  }

  function genId(): string {
    return Math.random().toString(36).slice(2, 10)
  }

  async function addTab() {
    const tab: ChatTab = { id: genId(), label: `Chat ${tabs.length + 1}`, convId: '', unread: 0 }
    tabs = [...tabs, tab]
    activeTabId = tab.id
    setMessages(tab.id, [])
    // Create a new backend session for this tab
    try {
      const data = await api<any>('/api/chat', { method: 'DELETE' })
      if (data.conv_id) {
        tab.convId = data.conv_id
        tabs = [...tabs]
      }
    } catch { /* will get conv_id on first message */ }
    saveTabs()
  }

  function closeTab(tabId: string) {
    // Clean up tab messages and drafts
    delete tabMessages[tabId]
    tabMessages = { ...tabMessages }
    delete tabDrafts[tabId]
    tabDrafts = { ...tabDrafts }
    tabs = tabs.filter(t => t.id !== tabId)
    if (activeTabId === tabId) {
      activeTabId = tabs[0]?.id || ''
    }
    saveTabs()
  }

  function switchTab(tabId: string) {
    if (activeTabId === tabId) return
    activeTabId = tabId
    // Clear unread
    const tab = tabs.find(t => t.id === tabId)
    if (tab) {
      tab.unread = 0
      tabs = [...tabs]
      saveTabs()
    }
    // Only load from API if we don't already have messages cached for this tab
    if (!tabMessages[tabId] || tabMessages[tabId].length === 0) {
      loadHistory()
    } else {
      scrollToBottom()
    }
  }

  function renameTab(tabId: string) {
    const tab = tabs.find(t => t.id === tabId)
    if (!tab) return
    const name = prompt('Rename tab:', tab.label)
    if (name && name.trim()) {
      tab.label = name.trim()
      tabs = [...tabs]
      saveTabs()
    }
  }

  // --- Messages (per-tab) ---
  let tabMessages = $state<Record<string, ChatMsg[]>>({})
  let messages = $derived(tabMessages[activeTabId] || [])
  let sending = $state(false)
  let sendingTabId = $state('') // track which tab initiated the send
  let tiers = $state<Tier[]>([])
  let messagesContainer: HTMLDivElement
  let streamingBlocks = $state<any[]>([])
  let streamingText = $state('')
  let pollTimer: ReturnType<typeof setTimeout> | null = null
  let activeJobId = $state<string | null>(null)
  let messageQueue = $state<{ message: string; mediaFiles: MediaFile[]; model: string; tabId: string }[]>([])
  let tabDrafts = $state<Record<string, string>>({})
  let currentDraft = $derived(tabDrafts[activeTabId] || '')

  function updateDraft(text: string) {
    tabDrafts[activeTabId] = text
    tabDrafts = { ...tabDrafts }
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

  function setMessages(tabId: string, msgs: ChatMsg[]) {
    tabMessages[tabId] = msgs
    tabMessages = { ...tabMessages }
  }

  function appendMessage(tabId: string, msg: ChatMsg) {
    const current = tabMessages[tabId] || []
    tabMessages[tabId] = [...current, msg]
    tabMessages = { ...tabMessages }
  }

  async function scrollToBottom() {
    await tick()
    if (messagesContainer) {
      requestAnimationFrame(() => {
        messagesContainer.scrollTop = messagesContainer.scrollHeight
      })
    }
  }

  async function loadHistory(tabId?: string) {
    const tid = tabId || activeTabId
    const tab = tabs.find(t => t.id === tid)
    const convId = tab?.convId || ''
    // Don't load history for tabs without a conversation ID (new empty tabs)
    if (!convId) {
      setMessages(tid, [])
      return
    }
    try {
      const data = await api<ChatMsg[]>(`/api/chat?limit=100&conv_id=${convId}`)
      setMessages(tid, data || [])
      if (tid === activeTabId) scrollToBottom()
    } catch {
      setMessages(tid, [])
    }
  }

  async function loadTiers() {
    try {
      const data = await api<any>('/api/tiers')
      tiers = (data.tiers || []).map((t: any) => ({ name: t.name, model: t.model }))
    } catch {
      tiers = []
    }
  }

  // Check for active job on load (reconnect)
  async function checkActiveJob() {
    const convId = activeTab?.convId || ''
    try {
      const data = await api<any>(`/api/chat/job?conv_id=${convId}`)
      if (data.active && data.job_id) {
        activeJobId = data.job_id
        sending = true
        sendingTabId = activeTabId
        reconnectToStream(data.job_id, data.events || 0)
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
    // Auto-create a tab if none exist
    if (tabs.length === 0) {
      await addTab()
    }

    // Clear draft for current tab
    tabDrafts[activeTabId] = ''
    tabDrafts = { ...tabDrafts }

    // Client-side command handling
    const trimmed = message.trim()
    if (trimmed === '/new' || trimmed === '/clear') {
      await newConversation()
      return
    }
    if (trimmed === '/skills') {
      nav.navigateTo('skills')
      return
    }

    // If this tab is already sending, queue it
    if (sending && sendingTabId === activeTabId) {
      messageQueue = [...messageQueue, { message, mediaFiles, model, tabId: activeTabId }]
      return
    }

    await doSend(message, mediaFiles, model, activeTabId)
  }

  async function doSend(message: string, mediaFiles: MediaFile[], model: string, tabId: string) {
    sending = true
    sendingTabId = tabId
    streamingBlocks = []
    streamingText = ''

    const tab = tabs.find(t => t.id === tabId)
    const convId = tab?.convId || ''
    const mediaIds = mediaFiles.map(f => f.upload_id)

    // Add user message optimistically to the originating tab
    if (message || mediaFiles.length > 0) {
      const userMsg: ChatMsg = {
        id: 'temp-' + Date.now(),
        role: 'user',
        text: message,
        ts: new Date().toISOString(),
        conv_id: convId,
        media: mediaFiles.map(f => ({ upload_id: f.upload_id, type: f.mime_type?.startsWith('image/') ? 'photo' : 'document', file_name: f.file_name, mime_type: f.mime_type })),
      }
      appendMessage(tabId, userMsg)
      if (tabId === activeTabId) scrollToBottom()
    }

    try {
      const body: any = { message, conv_id: convId }
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
      })

      if (res.status === 401) {
        toasts.show('Session expired', 'error')
        sending = false
        return
      }

      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: 'Send failed' }))
        toasts.show(err.error || 'Send failed', 'error')
        sending = false
        return
      }

      // Read SSE stream
      await readStream(res)
    } catch (e: any) {
      toasts.show(e.message || 'Send failed', 'error')
      sending = false
    }
  }

  async function readStream(res: Response) {
    const reader = res.body?.getReader()
    if (!reader) { sending = false; return }

    const decoder = new TextDecoder()
    let buffer = ''

    // Track content blocks as they stream
    let currentBlocks: any[] = []
    let currentText = ''
    let pendingEvent = '' // SSE event type from "event:" line

    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (line === '') {
            // Empty line = end of SSE message, reset pending event
            pendingEvent = ''
            continue
          }

          if (line.startsWith('event: ')) {
            pendingEvent = line.slice(7).trim()
            continue
          }

          if (!line.startsWith('data: ')) continue
          const dataStr = line.slice(6)
          let data: any
          try { data = JSON.parse(dataStr) } catch { continue }

          // Use the paired event type, or fall back to data.type
          const eventType = pendingEvent || data.type || ''

          if (eventType === 'job') {
            activeJobId = data.job_id
            // Capture conv_id for tab association
            if (data.conv_id && sendingTabId) {
              const tab = tabs.find(t => t.id === sendingTabId)
              if (tab && !tab.convId) {
                tab.convId = data.conv_id
                tabs = [...tabs]
                saveTabs()
              }
            }
            continue
          }

          if (eventType === 'error') {
            toasts.show(data.error || 'Stream error', 'error')
            continue
          }

          if (eventType === 'system') {
            // System message from command handling (e.g. /new, /skills)
            if (data.text && sendingTabId) {
              appendMessage(sendingTabId, {
                id: 'sys-' + Date.now(),
                role: 'system',
                text: data.text,
                ts: new Date().toISOString(),
              })
            }
            continue
          }

          if (eventType === 'done') {
            // Apply metadata from done event to the last assistant message
            if (sendingTabId && data) {
              const msgs = tabMessages[sendingTabId] || []
              const lastAssistant = [...msgs].reverse().find(m => m.role === 'assistant')
              if (lastAssistant) {
                if (data.model) lastAssistant.model = data.model
                if (data.tier) lastAssistant.tier = data.tier
                if (data.cost_usd) lastAssistant.cost_usd = data.cost_usd
                if (data.duration_ms) lastAssistant.duration_ms = data.duration_ms
                if (data.skills) lastAssistant.skills = data.skills
                tabMessages = { ...tabMessages }
              }
            }
            continue
          }

          // Inject the event type into data for the handler
          data.type = data.type || eventType
          handleStreamEvent(data, currentBlocks, (updated) => {
            currentBlocks = updated
          }, (text) => {
            currentText = text
          })
        }

        // Update streaming display
        streamingBlocks = [...currentBlocks]
        streamingText = currentText
        scrollToBottom()
      }
    } catch {
      // Stream ended or errored
    } finally {
      // Finalize: reload history for the originating tab
      const originTab = sendingTabId
      const finalText = streamingText
      sending = false
      sendingTabId = ''
      activeJobId = null
      streamingBlocks = []
      streamingText = ''
      await loadHistory(originTab)
      if (originTab === activeTabId) scrollToBottom()

      // Desktop notification if tab is not visible
      if (document.hidden && 'Notification' in window && Notification.permission === 'granted' && finalText) {
        new Notification('ALF', { body: finalText.slice(0, 100) })
      }

      // Sound notification
      if (originTab) {
        sound.play()
      }

      // Notify sidebar badge if user isn't viewing chat
      if (nav.currentView !== 'chat') {
        nav.incrementBadge('chat')
      }

      // Bump unread on originating tab if user switched away
      if (originTab !== activeTabId) {
        const t = tabs.find(t => t.id === originTab)
        if (t) {
          t.unread = (t.unread || 0) + 1
          tabs = [...tabs]
          saveTabs()
        }
      }

      // Process queue
      if (messageQueue.length > 0) {
        const next = messageQueue[0]
        messageQueue = messageQueue.slice(1)
        doSend(next.message, next.mediaFiles, next.model, next.tabId)
      }
    }
  }

  // SSE event parsing — the server uses paired event:/data: lines
  // We need to handle a stateful parse

  async function reconnectToStream(jobId: string, offset: number) {
    try {
      const res = await fetch(`/api/chat/job?stream=${jobId}&offset=${offset}`, {
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
      })
      if (!res.ok) {
        sending = false
        return
      }
      await readStream(res)
    } catch {
      sending = false
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
    } else if (type === 'message_delta' || type === 'message_stop') {
      // Done event — may contain cost, model, etc.
      // No action needed; stream will end and we reload history
    } else if (type === 'error' || data.error) {
      toasts.show(data.error || 'Stream error', 'error')
    }

    // Build aggregate text for simple display
    const fullText = blocks
      .filter(b => b.type === 'text')
      .map(b => b.text)
      .join('')
    setText(fullText)
  }

  // --- Stop active call ---
  async function stopCall() {
    if (!activeJobId) return
    try {
      await api('DELETE', '/api/chat/job')
    } catch { /* ignore */ }
    // Immediately reset UI state — don't wait for stream to end
    const originTab = sendingTabId
    sending = false
    sendingTabId = ''
    activeJobId = null
    streamingBlocks = []
    streamingText = ''
    await loadHistory(originTab)
    if (originTab === activeTabId) scrollToBottom()
  }

  // --- New conversation ---
  async function newConversation() {
    try {
      const data = await api<any>('/api/chat', { method: 'DELETE' })
      if (data.conv_id && activeTab) {
        activeTab.convId = data.conv_id
        tabs = [...tabs]
        saveTabs()
      }
      setMessages(activeTabId, [])
      toasts.show('New conversation started', 'success')
    } catch (e: any) {
      toasts.show(e.error || 'Failed to start new conversation', 'error')
    }
  }

  // --- Lifecycle ---
  let unsubTiers: (() => void) | null = null

  onMount(async () => {
    // Request notification permission
    if ('Notification' in window && Notification.permission === 'default') {
      Notification.requestPermission()
    }
    await loadTiers()
    // Reload tiers on profile switch (SSE event)
    unsubTiers = events.subscribe('tiers', () => loadTiers())
    // Only load history if the tab has a saved convId (restored session)
    if (activeTab?.convId) {
      await loadHistory()
    }
    await checkActiveJob()
  })

  onDestroy(() => {
    if (pollTimer) clearTimeout(pollTimer)
    unsubTiers?.()
  })
</script>

<div class="chat-view">
  <!-- Tab bar -->
  <div class="chat-tabs">
    <div class="tab-list">
      {#each tabs as tab}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div
          class="tab-item"
          class:active={tab.id === activeTabId}
          onclick={() => switchTab(tab.id)}
          ondblclick={() => renameTab(tab.id)}
        >
          <MessageCircle size={13} />
          <span class="tab-label">{tab.label}</span>
          {#if tab.unread > 0}
            <span class="tab-unread">{tab.unread}</span>
          {/if}
          <button
            class="tab-close"
            onclick={(e: MouseEvent) => { e.stopPropagation(); closeTab(tab.id) }}
          >
            <X size={11} />
          </button>
        </div>
      {/each}
      <button class="tab-add" onclick={addTab}><Plus size={14} /></button>
    </div>

    <button class="new-conv-btn" onclick={newConversation} title="New conversation">
      <RotateCw size={14} />
    </button>
  </div>

  <!-- Messages -->
  <div class="chat-messages" bind:this={messagesContainer}>
    {#if messages.length === 0 && !(sending && sendingTabId === activeTabId)}
      <div class="chat-empty">
        <MessageCircle size={32} />
        <p>No messages yet. Start a conversation.</p>
      </div>
    {/if}

    {#each messages as msg (msg.id)}
      <ChatMessageComponent {msg} convId={activeTab?.convId || ''} onSendToTask={openAgentModal} />
    {/each}

    <!-- Streaming response (only on the tab that initiated the send) -->
    {#if sending && sendingTabId === activeTabId && streamingBlocks.length > 0}
      <ChatMessageComponent
        msg={{
          id: 'streaming',
          role: 'assistant',
          text: streamingText,
          ts: new Date().toISOString(),
          content_blocks: streamingBlocks,
        }}
        convId={activeTab?.convId || ''}
      />
    {:else if sending && sendingTabId === activeTabId}
      <div class="chat-msg chat-msg-assistant typing-indicator">
        <span class="dot"></span>
        <span class="dot"></span>
        <span class="dot"></span>
      </div>
    {/if}

    <!-- Queued messages (shown as pending user bubbles) -->
    {#each messageQueue as queued, i}
      {#if queued.tabId === activeTabId}
        <div class="chat-msg chat-msg-user queued-msg">
          <div class="msg-text">{queued.message}</div>
          <div class="msg-footer">
            <span class="msg-time queued-label">queued</span>
            <button class="queued-cancel" onclick={() => { messageQueue = messageQueue.filter((_, idx) => idx !== i) }} title="Cancel">
              <X size={12} />
            </button>
          </div>
        </div>
      {/if}
    {/each}
  </div>

  <!-- Input -->
  <ChatInput onSend={handleSend} onStop={stopCall} sending={sending && sendingTabId === activeTabId} {tiers} draft={currentDraft} onDraftChange={updateDraft} />
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
      <label class="checkbox-label">
        <input type="checkbox" bind:checked={agentModalValidation} />
        Review plan before execution
      </label>
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
  .chat-view {
    display: flex;
    flex-direction: column;
    height: calc(100vh - 48px);
    max-height: calc(100vh - 48px);
    margin-bottom: -24px;
  }

  /* Tabs */
  .chat-tabs {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
    overflow-x: auto;
    flex-shrink: 0;
  }

  .tab-list {
    display: flex;
    align-items: center;
    gap: 2px;
    flex: 1;
    overflow-x: auto;
  }

  .tab-list::-webkit-scrollbar { height: 0; }

  .tab-item {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    background: none;
    border: none;
    border-radius: 6px;
    color: var(--text-dim);
    font-family: inherit;
    font-size: 0.78rem;
    cursor: pointer;
    white-space: nowrap;
    transition: background 0.15s, color 0.15s;
  }

  .tab-item:hover {
    background: var(--bg-input);
    color: var(--text);
  }

  .tab-item.active {
    background: var(--bg-card);
    color: var(--text);
    font-weight: 500;
    border: 1px solid var(--border);
  }

  .tab-label {
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .tab-unread {
    background: var(--red, #e53935);
    color: #fff;
    font-size: 0.6rem;
    font-weight: 600;
    padding: 1px 5px;
    border-radius: 8px;
    min-width: 16px;
    text-align: center;
  }

  .tab-close {
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    padding: 2px;
    display: flex;
    align-items: center;
    border-radius: 3px;
    opacity: 0;
    transition: opacity 0.15s;
  }

  .tab-item:hover .tab-close {
    opacity: 1;
  }

  .tab-close:hover {
    background: var(--border);
  }

  .tab-add {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    background: none;
    border: 1px dashed var(--border);
    border-radius: 6px;
    color: var(--text-dim);
    cursor: pointer;
    flex-shrink: 0;
    transition: background 0.15s;
  }

  .tab-add:hover {
    background: var(--bg-input);
    color: var(--text);
  }

  .new-conv-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    background: none;
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-dim);
    cursor: pointer;
    flex-shrink: 0;
    transition: background 0.15s;
  }

  .new-conv-btn:hover {
    background: var(--bg-input);
    color: var(--text);
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
    font-size: 0.85rem;
  }

  /* Typing indicator */
  .typing-indicator {
    display: flex;
    gap: 4px;
    padding: 12px 16px;
    align-self: flex-start;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 12px;
    border-bottom-left-radius: 4px;
  }

  .dot {
    width: 6px;
    height: 6px;
    background: var(--text-dim);
    border-radius: 50%;
    animation: bounce 1.4s infinite ease-in-out both;
  }

  .dot:nth-child(1) { animation-delay: -0.32s; }
  .dot:nth-child(2) { animation-delay: -0.16s; }

  @keyframes bounce {
    0%, 80%, 100% { transform: scale(0.6); opacity: 0.4; }
    40% { transform: scale(1); opacity: 1; }
  }

  /* Queued messages */
  .queued-msg {
    opacity: 0.55;
    border: 1px dashed var(--border);
  }

  .queued-label {
    font-style: italic;
  }

  .queued-cancel {
    display: flex;
    align-items: center;
    background: none;
    border: none;
    color: inherit;
    opacity: 0.4;
    cursor: pointer;
    padding: 2px;
    border-radius: 4px;
    transition: opacity 0.15s;
  }

  .queued-cancel:hover {
    opacity: 1;
    color: var(--red, #e53935);
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
    font-size: 0.8rem;
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
    font-size: 0.85rem;
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
      height: calc(100vh - 120px);
    }

    .chat-messages {
      padding: 8px;
    }
  }
</style>
