import { fire } from './_helpers.js';
class AlfDrawer extends HTMLElement {
  connectedCallback() {
    const label = this.getAttribute('label') || '';
    const content = this.innerHTML;
    this.innerHTML = '';
    this._overlay = document.createElement('div');
    this._overlay.className = 'drawer-overlay';
    this._overlay.addEventListener('click', () => this.close());
    this._panel = document.createElement('div');
    this._panel.className = 'drawer-panel';
    this._panel.innerHTML = `<div class="drawer-header"><h3>${label}</h3><button class="btn-icon drawer-close"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6L6 18M6 6l12 12"/></svg></button></div><div class="drawer-body">${content}</div>`;
    this._panel.querySelector('.drawer-close').addEventListener('click', () => this.close());
    this.appendChild(this._overlay);
    this.appendChild(this._panel);
    this.classList.add('drawer');
  }
  get body() { return this._panel ? this._panel.querySelector('.drawer-body') : null; }
  open() { this.classList.add('open'); fire(this, 'alf-open', {}); }
  close() { this.classList.remove('open'); fire(this, 'alf-close', {}); }
}
customElements.define('alf-drawer', AlfDrawer);
