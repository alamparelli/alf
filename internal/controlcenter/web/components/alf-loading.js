class AlfLoading extends HTMLElement {
  static get observedAttributes() { return ['active', 'variant', 'message']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    const active = this.hasAttribute('active');
    const variant = this.getAttribute('variant') || 'spinner';
    const msg = this.getAttribute('message') || '';
    this.style.display = active ? '' : 'none';
    if (variant === 'skeleton') {
      this.innerHTML = '<div class="skeleton-lines"><div class="skeleton-line"></div><div class="skeleton-line skeleton-line-short"></div><div class="skeleton-line"></div></div>';
    } else {
      this.innerHTML = `<div class="loading-spinner">${msg ? `<span class="loading-message">${msg}</span>` : ''}</div>`;
    }
  }
  show() { this.setAttribute('active', ''); }
  hide() { this.removeAttribute('active'); }
}
customElements.define('alf-loading', AlfLoading);
