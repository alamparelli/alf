<script lang="ts">
  import { onMount } from 'svelte'
  import { theme } from '../stores/theme.svelte'

  let { slug }: { slug: string } = $props()
  let iframe: HTMLIFrameElement

  onMount(() => {
    // Sync theme when iframe loads
    if (iframe) {
      iframe.addEventListener('load', () => {
        theme.syncIframe(iframe)
      })
    }
  })

  // Re-sync theme when palette changes
  $effect(() => {
    if (iframe) {
      theme.palette // track dependency
      theme.syncIframe(iframe)
    }
  })
</script>

<iframe
  bind:this={iframe}
  class="page-frame"
  src="/apps/{slug}/"
  title={slug}
></iframe>

<style>
  .page-frame {
    width: 100%;
    height: calc(100vh - 60px);
    border: none;
    border-radius: var(--radius, 8px);
  }
</style>
