import { fire, esc } from './_helpers.js';
class AlfDialog extends HTMLElement {
  open() {
    const label = this.getAttribute('label') || 'Dialog';
    const fields = Array.from(this.querySelectorAll('alf-input, alf-select'));
    let html = '<h3>' + esc(label) + '</h3><hr class="divider">';
    fields.forEach(function(f) {
      var tag = f.tagName.toLowerCase();
      // Read value from JS property first (set via .value =), then attribute
      var val = (typeof f.value === 'string' && f.value) ? f.value : (f.getAttribute('value') || '');
      if (tag === 'alf-input') {
        var type = f.getAttribute('type') || 'text';
        var fieldLabel = f.getAttribute('label') || '';
        var name = f.getAttribute('name') || '';
        var ph = f.getAttribute('placeholder') || '';
        var req = f.hasAttribute('required') ? ' required' : '';
        if (type === 'textarea') {
          html += '<div class="form-group">' +
            (fieldLabel ? '<label class="form-label">' + esc(fieldLabel) + '</label>' : '') +
            '<textarea class="input" name="' + esc(name) + '" placeholder="' + esc(ph) + '"' + req + '>' + esc(val) + '</textarea></div>';
        } else {
          html += '<div class="form-group">' +
            (fieldLabel ? '<label class="form-label">' + esc(fieldLabel) + '</label>' : '') +
            '<input class="input" type="' + type + '" name="' + esc(name) + '" placeholder="' + esc(ph) + '" value="' + esc(val) + '"' + req + '></div>';
        }
      } else if (tag === 'alf-select') {
        var selectLabel = f.getAttribute('label') || '';
        var selectName = f.getAttribute('name') || '';
        var selectedVal = (typeof f.value === 'string' && f.value) ? f.value : '';
        var opts = Array.from(f.querySelectorAll('option')).map(function(o) {
          var sel = (selectedVal && o.value === selectedVal) ? ' selected' : (o.selected ? ' selected' : '');
          return '<option value="' + esc(o.value) + '"' + sel + '>' + o.textContent + '</option>';
        }).join('');
        html += '<div class="form-group">' +
          (selectLabel ? '<label class="form-label">' + esc(selectLabel) + '</label>' : '') +
          '<select class="input" name="' + esc(selectName) + '">' + opts + '</select></div>';
      }
    });
    html += '<div class="modal-actions"><button data-action="cancel" class="btn">Cancel</button><button data-action="save" class="btn btn-primary">Save</button></div>';
    var self = this;
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
