<script lang="ts">
  interface Props {
    checked: boolean
    label?: string
    disabled?: boolean
    onchange?: (checked: boolean) => void
  }

  let { checked = $bindable(), label, disabled = false, onchange }: Props = $props()

  function handleChange(e: Event) {
    checked = (e.target as HTMLInputElement).checked
    onchange?.(checked)
  }
</script>

<label class="toggle-switch" class:disabled>
  <input type="checkbox" bind:checked {disabled} onchange={handleChange} />
  <span class="slider"></span>
  {#if label}
    <span class="toggle-text">{label}</span>
  {/if}
</label>

<style>
  .toggle-switch {
    display: inline-flex;
    align-items: center;
    gap: 10px;
    cursor: pointer;
    font-size: var(--font-sm, 13px);
    position: relative;
  }

  .toggle-switch.disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .toggle-switch input {
    position: absolute;
    opacity: 0;
    width: 0;
    height: 0;
  }

  .slider {
    width: 36px;
    height: 20px;
    background: var(--border);
    border-radius: 10px;
    position: relative;
    transition: background 0.2s;
    flex-shrink: 0;
  }

  .slider::after {
    content: '';
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    background: var(--bg-card, #fff);
    border-radius: 50%;
    transition: transform 0.2s;
  }

  .toggle-switch input:checked + .slider {
    background: var(--accent);
  }

  .toggle-switch input:checked + .slider::after {
    transform: translateX(16px);
  }

  .toggle-text {
    user-select: none;
  }
</style>
