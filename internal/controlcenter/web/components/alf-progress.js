class AlfProgress extends HTMLElement {
  static get observedAttributes() { return ['value', 'max', 'label']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    const value = parseInt(this.getAttribute('value') || '0');
    const max = parseInt(this.getAttribute('max') || '100');
    const label = this.getAttribute('label') || '';
    const pct = max > 0 ? Math.round((value / max) * 100) : 0;
    this.innerHTML = `<div class="progress-wrap">${label ? `<div class="progress-label">${label}</div>` : ''}<div class="progress-bar"><div class="progress-fill" style="width:${pct}%"></div></div></div>`;
  }
}
customElements.define('alf-progress', AlfProgress);
