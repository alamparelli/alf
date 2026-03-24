<script lang="ts">
  import type { Snippet } from 'svelte'

  let { children }: { children: Snippet } = $props()
</script>

<div class="card">
  {@render children()}
</div>

<style>
  .card {
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    padding: 1.25rem;
    margin-bottom: 1rem;
  }
</style>
