import { fire } from './_helpers.js';
class AlfTabs extends HTMLElement {
  static get observedAttributes() { return ['value']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    const variant = this.getAttribute('variant') || 'filter';
    const val = this.getAttribute('value') || '';
    const tabs = Array.from(this.querySelectorAll('alf-tab'));
    const wrapClass = variant === 'underline' ? 'tab-bar tab-underline' : 'filter-tabs';
    // Save alf-tab elements, build buttons
    let existing = this.querySelector('._alf-tabs-wrap');
    if (!existing) {
      existing = document.createElement('div');
      existing.className = '_alf-tabs-wrap';
      // Hide original alf-tab elements
      tabs.forEach(t => t.style.display = 'none');
      this.appendChild(existing);
    }
    existing.className = '_alf-tabs-wrap ' + wrapClass;
    existing.innerHTML = tabs.map(t => {
      const v = t.getAttribute('value') || '';
      const active = v === val ? ' active' : '';
      return `<button class="tab${active}" data-value="${v}">${t.textContent}</button>`;
    }).join('');
    existing.querySelectorAll('button').forEach(btn => {
      btn.addEventListener('click', () => {
        this.setAttribute('value', btn.dataset.value);
        fire(this, 'alf-tab-change', { value: btn.dataset.value });
      });
    });
  }
}
class AlfTab extends HTMLElement {}
customElements.define('alf-tabs', AlfTabs);
customElements.define('alf-tab', AlfTab);
