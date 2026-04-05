import { esc } from './_helpers.js';
class AlfSelect extends HTMLElement {
  connectedCallback() {
    const label = this.getAttribute('label') || '';
    const name = this.getAttribute('name') || '';
    const ph = this.getAttribute('placeholder') || '';
    const dis = this.hasAttribute('disabled') ? 'disabled' : '';
    const options = Array.from(this.querySelectorAll('option'));
    const optionsHtml = (ph ? `<option value="" disabled selected>${esc(ph)}</option>` : '') +
      options.map(o => `<option value="${esc(o.value)}"${o.selected ? ' selected' : ''}>${o.textContent}</option>`).join('');
    this.innerHTML = `<div class="form-group">${label ? `<label class="form-label">${esc(label)}</label>` : ''}<select class="input" name="${esc(name)}" ${dis}>${optionsHtml}</select></div>`;
  }
  get value() { const el = this.querySelector('select'); return el ? el.value : ''; }
  set value(v) { const el = this.querySelector('select'); if (el) el.value = v; }
}
customElements.define('alf-select', AlfSelect);
