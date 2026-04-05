class AlfBarChart extends HTMLElement {
  connectedCallback() {
    this.classList.add('bar-chart');
    if (this.getAttribute('height')) this.style.height = this.getAttribute('height');
  }
  set data(v) {
    this._data = v || [];
    const max = Math.max(...this._data.map(d => d.value), 1);
    this.innerHTML = '<div class="bar-chart-bars">' + this._data.map(d => {
      const pct = (d.value / max) * 100;
      const color = d.color ? `var(--${d.color})` : 'var(--accent)';
      return `<div class="bar-chart-col"><div class="bar-chart-bar" style="height:${pct}%;background:${color}"></div><div class="bar-chart-label">${d.label || ''}</div></div>`;
    }).join('') + '</div>';
  }
  get data() { return this._data || []; }
}
customElements.define('alf-bar-chart', AlfBarChart);
