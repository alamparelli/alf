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

<div class="docs-view">
  {#if article}
    <!-- Article mode -->
    <div class="article-header">
      <button class="btn-back" onclick={goBack}>
        <ArrowLeft size={16} /> Back
      </button>
      <h2>{article.title}</h2>
      {#if article.tags?.length}
        <div class="tags">
          {#each article.tags as tag}
            <span class="tag">{tag}</span>
          {/each}
        </div>
      {/if}
    </div>

    <div class="article-layout">
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
      <div class="article-content" onclick={handleArticleClick}>
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

    <div class="search-row">
      <div class="search-input">
        <Search size={14} />
        <input
          type="text"
          placeholder="Search docs..."
          bind:value={searchQuery}
          oninput={onSearch}
        />
      </div>
    </div>

    <div class="docs-layout">
      <aside class="sidebar">
        <h4>Categories</h4>
        <button
          class="cat-item"
          class:active={!activeCategory}
          onclick={() => { activeCategory = ''; activeTag = '' }}
        >All</button>
        {#each categories as cat}
          <button
            class="cat-item"
            class:active={activeCategory === cat}
            onclick={() => { activeCategory = cat; activeTag = '' }}
          >{cat}</button>
        {/each}

        {#if allTags.length > 0}
          <h4 class="tags-heading">Tags</h4>
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

      <div class="doc-list">
        {#each filteredDocs as doc}
          <Card>
            <button class="doc-card" onclick={() => loadArticle(doc.id)}>
              <h3>{doc.title}</h3>
              {#if doc.summary}
                <p class="summary">{doc.summary}</p>
              {/if}
              <div class="doc-meta">
                {#if doc.category}
                  <span class="cat-badge">{doc.category}</span>
                {/if}
                {#each doc.tags || [] as tag}
                  <span class="tag-sm">{tag}</span>
                {/each}
              </div>
            </button>
          </Card>
        {:else}
          <p class="empty">{loading ? 'Loading...' : 'No documents found'}</p>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .docs-view {
    padding: 8px 0;
    width: 100%;
  }

  h2 {
    margin-bottom: 16px;
  }

  .search-row {
    margin-bottom: 1rem;
  }

  .search-input {
    display: flex;
    align-items: center;
    gap: 6px;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 12px;
  }

  .search-input input {
    flex: 1;
    background: none;
    border: none;
    color: var(--text);
    outline: none;
    font-size: var(--font-sm, 13px);
  }

  .docs-layout {
    display: flex;
    gap: 1.5rem;
  }

  .sidebar {
    width: 200px;
    flex-shrink: 0;
  }

  .sidebar h4 {
    font-size: var(--font-xs, 11px);
    text-transform: uppercase;
    color: var(--text-dim);
    margin-bottom: 0.5rem;
  }

  .tags-heading {
    margin-top: 1.5rem;
  }

  .cat-item {
    display: block;
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    color: var(--text-dim);
    padding: 4px 8px;
    border-radius: 4px;
    cursor: pointer;
    font-size: var(--font-sm, 13px);
  }

  .cat-item:hover, .cat-item.active {
    color: var(--accent);
    background: var(--bg-card);
  }

  .tag-cloud {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .tag-btn {
    background: var(--bg-card);
    border-radius: 10px;
    padding: 2px 8px;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    cursor: pointer;
  }

  .tag-btn:hover, .tag-btn.active {
    color: var(--accent);
  }

  .tag-count {
    opacity: 0.5;
    margin-left: 2px;
  }

  .doc-list {
    flex: 1;
    min-width: 0;
  }

  .doc-card {
    display: block;
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    color: var(--text);
    cursor: pointer;
    padding: 0;
  }

  .doc-card h3 {
    font-size: var(--font-md, 15px);
    margin-bottom: 0.25rem;
  }

  .doc-card:hover h3 {
    color: var(--accent);
  }

  .summary {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin-bottom: 0.5rem;
  }

  .doc-meta {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  .cat-badge {
    background: var(--accent);
    color: var(--bg);
    border-radius: 4px;
    padding: 1px 6px;
    font-size: var(--font-xs, 11px);
    font-weight: 600;
  }

  .tag-sm {
    background: var(--bg);
    border-radius: 4px;
    padding: 1px 6px;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
  }

  .empty {
    text-align: center;
    color: var(--text-dim);
    padding: 2rem;
  }

  /* Article mode */
  .article-header {
    margin-bottom: 1rem;
  }

  .btn-back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: none;
    border: none;
    border-radius: 6px;
    color: var(--text-dim);
    padding: 6px 12px;
    cursor: pointer;
    font-size: var(--font-sm, 13px);
    margin-bottom: 0.75rem;
  }

  .btn-back:hover {
    color: var(--accent);
  }

  .tags {
    display: flex;
    gap: 6px;
    margin-top: 0.5rem;
  }

  .tag {
    background: var(--bg-card);
    border-radius: 10px;
    padding: 2px 8px;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
  }

  .article-layout {
    display: flex;
    gap: 1.5rem;
  }

  .toc {
    width: 200px;
    flex-shrink: 0;
    position: sticky;
    top: 60px;
    align-self: flex-start;
    max-height: calc(100vh - 100px);
    overflow-y: auto;
  }

  .toc h4 {
    font-size: var(--font-xs, 11px);
    text-transform: uppercase;
    color: var(--text-dim);
    margin-bottom: 0.5rem;
  }

  .toc-item {
    display: block;
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    color: var(--text-dim);
    padding: 3px 8px;
    cursor: pointer;
    font-size: var(--font-sm, 13px);
    border-left: 2px solid var(--border);
  }

  .toc-item:hover {
    color: var(--accent);
    border-left-color: var(--accent);
  }

  .toc-h3 {
    padding-left: 20px;
  }

  .article-content {
    flex: 1;
    min-width: 0;
    line-height: 1.7;
  }

  .article-content :global(h1) { font-size: 1.75rem; font-weight: 700; margin: 2rem 0 1rem; line-height: 1.3; }
  .article-content :global(h2) { font-size: 1.35rem; font-weight: 600; margin: 1.75rem 0 0.75rem; border-bottom: 1px solid var(--border); padding-bottom: 0.35rem; line-height: 1.4; }
  .article-content :global(h3) { font-size: 1.1rem; font-weight: 600; margin: 1.25rem 0 0.5rem; color: var(--text); }
  .article-content :global(h4) { font-size: var(--font-sm, 13px); font-weight: 600; margin: 1rem 0 0.4rem; text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-dim); }
  .article-content :global(h5), .article-content :global(h6) { font-size: var(--font-sm, 13px); font-weight: 500; margin: 0.75rem 0 0.35rem; color: var(--text-dim); }
  .article-content :global(p) { margin-bottom: 1rem; color: var(--text); }
  .article-content :global(code) { background: var(--bg); padding: 2px 5px; border-radius: 3px; font-size: 0.85em; border: 1px solid var(--border); }
  .article-content :global(pre) { background: var(--bg); border: 1px solid var(--border); border-radius: 6px; padding: 1rem; overflow-x: auto; margin-bottom: 1rem; }
  .article-content :global(pre code) { background: none; padding: 0; border: none; }
  .article-content :global(ul) { list-style: disc; padding-left: 1.5rem; margin-bottom: 1rem; }
  .article-content :global(ol) { list-style: decimal; padding-left: 1.5rem; margin-bottom: 1rem; }
  .article-content :global(li) { margin-bottom: 0.3rem; }
  .article-content :global(a) { color: var(--accent); text-decoration: underline; text-underline-offset: 2px; }
  .article-content :global(blockquote) { border-left: 3px solid var(--accent); padding: 0.5rem 1rem; color: var(--text-dim); margin-bottom: 1rem; background: color-mix(in srgb, var(--accent) 5%, transparent); border-radius: 0 4px 4px 0; }
  .article-content :global(hr) { border: none; border-top: 1px solid var(--border); margin: 1.5rem 0; }
  .article-content :global(table) { width: 100%; border-collapse: collapse; margin-bottom: 1rem; }
  .article-content :global(th), .article-content :global(td) { border: 1px solid var(--border); padding: 6px 10px; text-align: left; font-size: var(--font-sm, 13px); }
  .article-content :global(th) { background: var(--bg); font-weight: 600; }

  .scroll-top {
    position: fixed;
    bottom: 100px;
    right: 24px;
    background: var(--accent);
    color: var(--bg);
    border: none;
    border-radius: 50%;
    width: 40px;
    height: 40px;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
    z-index: 10;
  }

  @media (max-width: 768px) {
    .docs-layout {
      flex-direction: column;
    }
    .sidebar {
      width: 100%;
    }
    .article-layout {
      flex-direction: column;
    }
    .toc {
      width: 100%;
      position: static;
    }
  }
</style>
