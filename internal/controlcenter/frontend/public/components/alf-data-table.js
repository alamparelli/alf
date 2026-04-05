import { esc } from './_helpers.js';
class AlfDataTable extends HTMLElement {
  connectedCallback() { this._columns = Array.from(this.querySelectorAll('alf-column')); this._render(); }
  set data(v) { this._data = v; this._render(); }
  get data() { return this._data || []; }
  _render() {
    if (!this._columns) return;
    const cols = this._columns;
    const rows = this._data || [];
    let html = '<table class="table"><thead><tr>';
    cols.forEach(c => { html += `<th>${esc(c.getAttribute('label') || '')}</th>`; });
    html += '</tr></thead><tbody>';
    rows.forEach(row => {
      html += '<tr>';
      cols.forEach(c => {
        const key = c.getAttribute('key') || '';
        const type = c.getAttribute('type') || 'text';
        const val = row[key];
        if (type === 'tag' && val && typeof val === 'object') {
          html += `<td><span class="tag tag-${val.variant || 'accent'}">${esc(val.text || '')}</span></td>`;
        } else {
          html += `<td>${esc(String(val || ''))}</td>`;
        }
      });
      html += '</tr>';
    });
    html += '</tbody></table>';
    // Keep alf-column elements hidden
    cols.forEach(c => c.style.display = 'none');
    let wrap = this.querySelector('._alf-table-wrap');
    if (!wrap) { wrap = document.createElement('div'); wrap.className = '_alf-table-wrap'; this.appendChild(wrap); }
    wrap.innerHTML = html;
  }
}
class AlfColumn extends HTMLElement {}
customElements.define('alf-data-table', AlfDataTable);
customElements.define('alf-column', AlfColumn);
