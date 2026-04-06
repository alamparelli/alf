class AlfRingChart extends HTMLElement {
  static get observedAttributes() { return ['value', 'color']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    const val = parseInt(this.getAttribute('value') || '0');
    const color = this.getAttribute('color') || 'accent';
    const r = 36, c = 2 * Math.PI * r;
    const offset = c - (val / 100) * c;
    this.innerHTML = `<svg width="80" height="80" viewBox="0 0 80 80"><circle cx="40" cy="40" r="${r}" fill="none" stroke="var(--border)" stroke-width="6"/><circle cx="40" cy="40" r="${r}" fill="none" stroke="var(--${color})" stroke-width="6" stroke-dasharray="${c}" stroke-dashoffset="${offset}" stroke-linecap="round" transform="rotate(-90 40 40)"/><text x="40" y="44" text-anchor="middle" fill="var(--text)" font-size="14" font-weight="600">${val}%</text></svg>`;
  }
}
customElements.define('alf-ring-chart', AlfRingChart);
