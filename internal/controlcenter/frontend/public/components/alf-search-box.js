import { fire, ICONS } from './_helpers.js';
class AlfSearchBox extends HTMLElement {
  connectedCallback() {
    const ph = this.getAttribute('placeholder') || 'Search...';
    this.innerHTML = `<div class="search-box">${ICONS.search}<input type="text" placeholder="${ph}"></div>`;
    this.querySelector('input').addEventListener('input', e => {
      fire(this, 'alf-search', { value: e.target.value });
    });
  }
  get value() { const el = this.querySelector('input'); return el ? el.value : ''; }
  set value(v) { const el = this.querySelector('input'); if (el) el.value = v; }
}
customElements.define('alf-search-box', AlfSearchBox);
