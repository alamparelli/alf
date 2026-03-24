<script lang="ts">
  import { onMount } from 'svelte'
  import { theme } from '../stores/theme.svelte'

  let { slug }: { slug: string } = $props()
  let iframe: HTMLIFrameElement

  onMount(() => {
    if (iframe) {
      iframe.addEventListener('load', () => {
        theme.syncIframe(iframe)
      })
    }
  })

  $effect(() => {
    if (iframe) {
      theme.palette
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
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    border: none;
  }
</style>
