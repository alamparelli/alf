import { fire, ICONS } from './_helpers.js';
class AlfAlert extends HTMLElement {
  connectedCallback() {
    const variant = this.getAttribute('variant') || 'info';
    const dismissible = this.hasAttribute('dismissible');
    this.classList.add('alert', `alert-${variant}`);
    if (dismissible) {
      const btn = document.createElement('button');
      btn.className = 'btn-icon';
      btn.style.marginLeft = 'auto';
      btn.innerHTML = ICONS.close;
      btn.addEventListener('click', () => { this.remove(); fire(this, 'alf-dismiss', {}); });
      this.appendChild(btn);
      this.style.display = 'flex';
      this.style.alignItems = 'center';
    }
  }
}
customElements.define('alf-alert', AlfAlert);
