<script lang="ts">
  import { onMount } from 'svelte'
  import { ArrowLeft, ArrowUp, Search } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { nav } from '../stores/nav.svelte'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'

  interface DocMeta {
    id: string
    title: string
    summary: string
    category: string
    tags: string[]
    order: number
  }

  interface DocFull {
    id: string
    title: string
    category: string
    tags: string[]
    content: string
  }

  interface TocEntry {
    level: number
    text: string
    slug: string
  }

  let { articleId = '' }: { articleId?: string } = $props()

  let docs = $state<DocMeta[]>([])
  let article = $state<DocFull | null>(null)
  let toc = $state<TocEntry[]>([])
  let searchQuery = $state('')
  let activeCategory = $state('')
  let activeTag = $state('')
  let loading = $state(false)
  let searchTimer: ReturnType<typeof setTimeout> | undefined
  let showScrollTop = $state(false)

  let categories = $derived.by(() => {
    const cats = new Set<string>()
    for (const d of docs) {
      if (d.category) cats.add(d.category)
    }
    return [...cats].sort()
  })

  let allTags = $derived.by(() => {
    const tagMap = new Map<string, number>()
    for (const d of docs) {
      for (const t of d.tags || []) {
        tagMap.set(t, (tagMap.get(t) || 0) + 1)
      }
    }
    return [...tagMap.entries()].sort((a, b) => b[1] - a[1])
  })

  let filteredDocs = $derived.by(() => {
    let result = docs
    if (activeCategory) {
      result = result.filter(d => d.category === activeCategory)
    }
    if (activeTag) {
      result = result.filter(d => d.tags?.includes(activeTag))
    }
    return result
  })

  async function loadDocs(query = '') {
    loading = true
    try {
      const q = query ? `?q=${encodeURIComponent(query)}` : ''
      docs = await api<DocMeta[]>(`/api/docs/${q}`)
    } catch (e: any) {
      toasts.show(e.error || 'Failed to load docs', 'error')
    } finally {
      loading = false
    }
  }

  async function loadArticle(id: string) {
    loading = true
    try {
      article = await api<DocFull>(`/api/docs/${encodeURIComponent(id)}`)
      buildToc(article.content)
    } catch (e: any) {
      toasts.show(e.error || 'Article not found', 'error')
      article = null
    } finally {
      loading = false
    }
  }

  function buildToc(markdown: string) {
    const entries: TocEntry[] = []
    const headingRe = /^(#{2,3})\s+(.+)$/gm
    let m: RegExpExecArray | null
    while ((m = headingRe.exec(markdown)) !== null) {
      const text = m[2]
      const slug = text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '')
      entries.push({ level: m[1].length, text, slug })
    }
    toc = entries
  }

  function renderMarkdown(content: string): string {
    // Add IDs to headings for TOC linking
    const renderer = new marked.Renderer()
    renderer.heading = ({ text, depth }: { text: string; depth: number }) => {
      const slug = text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '')
      return `<h${depth} id="${slug}">${text}</h${depth}>`
    }

    // Handle internal doc links (docs:id)
    renderer.link = ({ href, text }: { href: string; text: string }) => {
      if (href.startsWith('docs:')) {
        const docId = href.slice(5)
        return `<a href="#" class="doc-link" data-doc-id="${docId}">${text}</a>`
      }
      return `<a href="${href}" target="_blank" rel="noopener">${text}</a>`
    }

    const html = marked.parse(content, { renderer, async: false }) as string
    return DOMPurify.sanitize(html)
  }

  function handleArticleClick(e: MouseEvent) {
    const target = e.target as HTMLElement
    const link = target.closest('.doc-link') as HTMLAnchorElement | null
    if (link) {
      e.preventDefault()
      const docId = link.dataset.docId
      if (docId) loadArticle(docId)
    }
  }

  function onSearch() {
    if (searchTimer) clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      loadDocs(searchQuery)
    }, 300)
  }

  function goBack() {
    article = null
    toc = []
  }

  function scrollToTop() {
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  function scrollToHeading(slug: string) {
    const el = document.getElementById(slug)
    if (el) el.scrollIntoView({ behavior: 'smooth' })
  }

  onMount(() => {
    if (articleId) {
      loadArticle(articleId)
    } else {
      loadDocs()
    }

    const onScroll = () => {
      showScrollTop = window.scrollY > 300
    }
    window.addEventListener('scroll', onScroll)
    return () => window.removeEventListener('scroll', onScroll)
  })
</script>

<div class="view">
  {#if article}
    <!-- Article mode -->
    <div style="margin-bottom:1rem">
      <button class="btn btn-ghost btn-sm" onclick={goBack} style="margin-bottom:0.75rem">
        <ArrowLeft size={16} /> Back
      </button>
      <h2>{article.title}</h2>
      {#if article.tags?.length}
        <div class="flex gap-xs" style="margin-top:0.5rem">
          {#each article.tags as tag}
            <span class="tag">{tag}</span>
          {/each}
        </div>
      {/if}
    </div>

    <div class="layout-sidebar">
      {#if toc.length > 0}
        <nav class="toc">
          <h4>Contents</h4>
          {#each toc as entry}
            <button
              class="toc-item"
              class:toc-h3={entry.level === 3}
              onclick={() => scrollToHeading(entry.slug)}
            >{entry.text}</button>
          {/each}
        </nav>
      {/if}

      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <div class="prose flex-1" onclick={handleArticleClick}>
        {@html renderMarkdown(article.content)}
      </div>
    </div>

    {#if showScrollTop}
      <button class="scroll-top" onclick={scrollToTop}>
        <ArrowUp size={18} />
      </button>
    {/if}
  {:else}
    <!-- List mode -->
    <h2>Documentation</h2>

    <div style="margin-bottom:1rem">
      <div class="search-box">
        <Search size={14} />
        <input
          type="text"
          placeholder="Search docs..."
          bind:value={searchQuery}
          oninput={onSearch}
        />
      </div>
    </div>

    <div class="layout-sidebar">
      <aside class="sidebar-nav">
        <h4>Categories</h4>
        <button
          class="sidebar-nav-item"
          class:active={!activeCategory}
          onclick={() => { activeCategory = ''; activeTag = '' }}
        >All</button>
        {#each categories as cat}
          <button
            class="sidebar-nav-item"
            class:active={activeCategory === cat}
            onclick={() => { activeCategory = cat; activeTag = '' }}
          >{cat}</button>
        {/each}

        {#if allTags.length > 0}
          <h4 style="margin-top:1.5rem">Tags</h4>
          <div class="tag-cloud">
            {#each allTags as [tag, count]}
              <button
                class="tag-btn"
                class:active={activeTag === tag}
                onclick={() => { activeTag = activeTag === tag ? '' : tag; activeCategory = '' }}
              >{tag} <span class="tag-count">{count}</span></button>
            {/each}
          </div>
        {/if}
      </aside>

      <div class="flex-1">
        {#each filteredDocs as doc}
          <Card>
            <button class="doc-card" onclick={() => loadArticle(doc.id)}>
              <h3>{doc.title}</h3>
              {#if doc.summary}
                <p class="summary">{doc.summary}</p>
              {/if}
              <div class="flex gap-xs flex-wrap">
                {#if doc.category}
                  <span class="tag tag-accent">{doc.category}</span>
                {/if}
                {#each doc.tags || [] as tag}
                  <span class="tag">{tag}</span>
                {/each}
              </div>
            </button>
          </Card>
        {:else}
          <p class="empty-sm">{loading ? 'Loading...' : 'No documents found'}</p>
        {/each}
      </div>
    </div>
  {/if}
</div>

<!-- No scoped CSS — all styles from alf-ui.css -->
