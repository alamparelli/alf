import { esc } from './_helpers.js';
class AlfKvRow extends HTMLElement {
  connectedCallback() {
    const label = this.getAttribute('label') || '';
    const content = this.innerHTML;
    this.classList.add('kv-row');
    this.innerHTML = `<span class="kv-label">${esc(label)}</span><span class="kv-value">${content}</span>`;
  }
}
customElements.define('alf-kv-row', AlfKvRow);
