import { esc } from './_helpers.js';
class AlfInput extends HTMLElement {
  connectedCallback() {
    // Don't render if inside alf-dialog (dialog reads attrs directly)
    if (this.closest('alf-dialog')) return;
    this._render();
  }
  _render() {
    const label = this.getAttribute('label') || '';
    const name = this.getAttribute('name') || '';
    const type = this.getAttribute('type') || 'text';
    const ph = this.getAttribute('placeholder') || '';
    const hint = this.getAttribute('hint') || '';
    const val = this._value !== undefined ? this._value : (this.getAttribute('value') || '');
    const req = this.hasAttribute('required') ? 'required' : '';
    const dis = this.hasAttribute('disabled') ? 'disabled' : '';
    const isTextarea = type === 'textarea';
    const inputEl = isTextarea
      ? '<textarea class="input" name="' + esc(name) + '" placeholder="' + esc(ph) + '" ' + req + ' ' + dis + '>' + esc(val) + '</textarea>'
      : '<input class="input" type="' + type + '" name="' + esc(name) + '" placeholder="' + esc(ph) + '" value="' + esc(val) + '" ' + req + ' ' + dis + '>';
    this.innerHTML = '<div class="form-group">' +
      (label ? '<label class="form-label">' + esc(label) + '</label>' : '') +
      inputEl +
      (hint ? '<small class="form-hint">' + esc(hint) + '</small>' : '') +
      '</div>';
  }
  get value() {
    const el = this.querySelector('input,textarea');
    if (el) return el.value;
    return this._value !== undefined ? this._value : (this.getAttribute('value') || '');
  }
  set value(v) {
    this._value = v;
    const el = this.querySelector('input,textarea');
    if (el) el.value = v;
  }
}
customElements.define('alf-input', AlfInput);
