import { fire, ICONS } from './_helpers.js';
class AlfNavRow extends HTMLElement {
  static get observedAttributes() { return ['label']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    const label = this.getAttribute('label') || '';
    this.classList.add('nav-row');
    this.innerHTML = `<button class="btn-icon nav-prev">${ICONS.chevronLeft}</button><span class="nav-label">${label}</span><button class="btn-icon nav-next">${ICONS.chevronRight}</button>`;
    this.querySelector('.nav-prev').addEventListener('click', () => fire(this, 'alf-nav', { direction: 'prev' }));
    this.querySelector('.nav-next').addEventListener('click', () => fire(this, 'alf-nav', { direction: 'next' }));
  }
}
customElements.define('alf-nav-row', AlfNavRow);
