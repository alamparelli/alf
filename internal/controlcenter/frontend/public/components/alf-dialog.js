import { fire, esc } from './_helpers.js';
class AlfDialog extends HTMLElement {
  open() {
    const label = this.getAttribute('label') || 'Dialog';
    const fields = Array.from(this.querySelectorAll('alf-input, alf-select'));
    let html = `<h3>${esc(label)}</h3><hr class="divider">`;
    fields.forEach(f => {
      const tag = f.tagName.toLowerCase();
      if (tag === 'alf-input') {
        const type = f.getAttribute('type') || 'text';
        const isTextarea = type === 'textarea';
        const el = isTextarea ? 'textarea' : 'input';
        const typeAttr = isTextarea ? '' : ` type="${type}"`;
        html += `<div class="form-group">${f.getAttribute('label') ? `<label class="form-label">${esc(f.getAttribute('label'))}</label>` : ''}<${el} class="input"${typeAttr} name="${esc(f.getAttribute('name') || '')}" placeholder="${esc(f.getAttribute('placeholder') || '')}" value="${esc(f.getAttribute('value') || '')}"${f.hasAttribute('required') ? ' required' : ''}>${isTextarea ? esc(f.getAttribute('value') || '') + '</textarea>' : ''}</div>`;
      } else if (tag === 'alf-select') {
        const opts = Array.from(f.querySelectorAll('option')).map(o => `<option value="${esc(o.value)}"${o.selected ? ' selected' : ''}>${o.textContent}</option>`).join('');
        html += `<div class="form-group">${f.getAttribute('label') ? `<label class="form-label">${esc(f.getAttribute('label'))}</label>` : ''}<select class="input" name="${esc(f.getAttribute('name') || '')}">${opts}</select></div>`;
      }
    });
    html += `<div class="modal-actions"><button data-action="cancel" class="btn">Cancel</button><button data-action="save" class="btn btn-primary">Save</button></div>`;
    const self = this;
    if (typeof AlfSDK !== 'undefined') {
      AlfSDK.sheet(html, {
        cancel: function() { AlfSDK.closeSheet(); fire(self, 'alf-cancel', {}); },
        save: function(p) { AlfSDK.closeSheet(); fire(self, 'alf-submit', p); }
      });
    }
  }
  close() { if (typeof AlfSDK !== 'undefined') AlfSDK.closeSheet(); }
}
customElements.define('alf-dialog', AlfDialog);
