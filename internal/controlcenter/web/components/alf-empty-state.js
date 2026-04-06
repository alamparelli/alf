import { fire, ICONS } from './_helpers.js';
class AlfEmptyState extends HTMLElement {
  connectedCallback() {
    const msg = this.getAttribute('message') || 'No items yet';
    const action = this.getAttribute('action') || '';
    const hideIcon = this.getAttribute('icon') === 'none';
    this.classList.add('empty-state');
    this.innerHTML = `${hideIcon ? '' : ICONS.empty}<p>${msg}</p>${action ? `<button class="btn btn-primary btn-sm">${action}</button>` : ''}`;
    const btn = this.querySelector('button');
    if (btn) btn.addEventListener('click', () => fire(this, 'alf-action', {}));
  }
}
customElements.define('alf-empty-state', AlfEmptyState);
