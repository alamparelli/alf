class AlfStatGrid extends HTMLElement {
  connectedCallback() { this.classList.add('stat-grid'); }
}
class AlfStat extends HTMLElement {
  static get observedAttributes() { return ['value', 'label', 'color']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    const value = this.getAttribute('value') || '0';
    const label = this.getAttribute('label') || '';
    const color = this.getAttribute('color') || 'accent';
    this.className = 'stat-item';
    this.innerHTML = `<div class="stat-bar" style="background:var(--${color})"></div><div class="stat-value">${value}</div><div class="stat-label">${label}</div>`;
  }
}
customElements.define('alf-stat-grid', AlfStatGrid);
customElements.define('alf-stat', AlfStat);
