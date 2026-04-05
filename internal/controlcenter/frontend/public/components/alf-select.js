import { esc } from './_helpers.js';
class AlfSelect extends HTMLElement {
  connectedCallback() {
    // Don't render if inside alf-dialog (dialog reads attrs directly)
    if (this.closest('alf-dialog')) return;
    this._render();
  }
  _render() {
    const label = this.getAttribute('label') || '';
    const name = this.getAttribute('name') || '';
    const ph = this.getAttribute('placeholder') || '';
    const dis = this.hasAttribute('disabled') ? 'disabled' : '';
    const options = Array.from(this.querySelectorAll('option'));
    const selectedVal = this._value;
    const optionsHtml = (ph ? '<option value="" disabled selected>' + esc(ph) + '</option>' : '') +
      options.map(function(o) {
        var sel = selectedVal !== undefined ? (o.value === selectedVal ? ' selected' : '') : (o.selected ? ' selected' : '');
        return '<option value="' + esc(o.value) + '"' + sel + '>' + o.textContent + '</option>';
      }).join('');
    this.innerHTML = '<div class="form-group">' +
      (label ? '<label class="form-label">' + esc(label) + '</label>' : '') +
      '<select class="input" name="' + esc(name) + '" ' + dis + '>' + optionsHtml + '</select></div>';
  }
  get value() {
    const el = this.querySelector('select');
    if (el) return el.value;
    return this._value !== undefined ? this._value : '';
  }
  set value(v) {
    this._value = v;
    const el = this.querySelector('select');
    if (el) el.value = v;
  }
}
customElements.define('alf-select', AlfSelect);
