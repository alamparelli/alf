(function() {
'use strict';
if (window.__alfComponentsLoaded) return;
window.__alfComponentsLoaded = true;

// --- helpers ---
function esc(s) { if (!s) return ''; var d = document.createElement('div'); d.textContent = s; return d.innerHTML; }
function fire(el, name, detail) { el.dispatchEvent(new CustomEvent(name, { bubbles: true, detail: detail })); }
var ICONS = {
  search: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/></svg>',
  plus: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 5v14M5 12h14"/></svg>',
  close: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6L6 18M6 6l12 12"/></svg>',
  chevronLeft: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M15 18l-6-6 6-6"/></svg>',
  chevronRight: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M9 18l6-6-6-6"/></svg>',
  chevronDown: '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>',
  empty: '<svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" style="color:var(--text-dim)"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 9h6M9 13h4"/></svg>'
};

// --- alf-accordion ---
var AlfAccordion = class extends HTMLElement {
  connectedCallback() { this.classList.add('accordion'); }
};
var AlfAccordionItem = class extends HTMLElement {
  connectedCallback() {
    var label = this.getAttribute('label') || '';
    var isOpen = this.hasAttribute('open');
    var content = this.innerHTML;
    this.classList.add('accordion-item');
    if (isOpen) this.classList.add('open');
    this.innerHTML = '<button class="accordion-header">' + label + '<svg class="accordion-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg></button><div class="accordion-body">' + content + '</div>';
    this.querySelector('.accordion-header').addEventListener('click', function() {
      this.closest('alf-accordion-item').classList.toggle('open');
    });
  }
};
customElements.define('alf-accordion', AlfAccordion);
customElements.define('alf-accordion-item', AlfAccordionItem);

// --- alf-alert ---
var AlfAlert = class extends HTMLElement {
  connectedCallback() {
    var variant = this.getAttribute('variant') || 'info';
    var dismissible = this.hasAttribute('dismissible');
    this.classList.add('alert', 'alert-' + variant);
    if (dismissible) {
      var btn = document.createElement('button');
      btn.className = 'btn-icon';
      btn.style.marginLeft = 'auto';
      btn.innerHTML = ICONS.close;
      var self = this;
      btn.addEventListener('click', function() { self.remove(); fire(self, 'alf-dismiss', {}); });
      this.appendChild(btn);
      this.style.display = 'flex';
      this.style.alignItems = 'center';
    }
  }
};
customElements.define('alf-alert', AlfAlert);

// --- alf-app-shell ---
var AlfAppShell = class extends HTMLElement {
  connectedCallback() {
    var titleEl = this.querySelector('[slot="title"]');
    var actionEls = Array.from(this.querySelectorAll('[slot="actions"]'));
    var body = Array.from(this.childNodes).filter(function(n) {
      if (n.nodeType !== 1) return n.nodeType === 3 && n.textContent.trim();
      return !n.hasAttribute('slot');
    });
    var shell = document.createElement('div');
    shell.className = 'app-shell';
    var header = document.createElement('div');
    header.className = 'app-header';
    if (titleEl) {
      var h1 = document.createElement('h1');
      h1.className = 'app-header-title';
      h1.textContent = titleEl.textContent;
      header.appendChild(h1);
    }
    var spacer = document.createElement('span');
    spacer.className = 'spacer';
    header.appendChild(spacer);
    if (actionEls.length) {
      var acts = document.createElement('div');
      acts.className = 'app-header-actions';
      actionEls.forEach(function(a) { a.removeAttribute('slot'); acts.appendChild(a); });
      header.appendChild(acts);
    }
    shell.appendChild(header);
    var appBody = document.createElement('div');
    appBody.className = 'app-body';
    body.forEach(function(n) { appBody.appendChild(n); });
    shell.appendChild(appBody);
    this.innerHTML = '';
    if (titleEl) titleEl.remove();
    this.appendChild(shell);
  }
};
customElements.define('alf-app-shell', AlfAppShell);

// --- alf-avatar ---
var AlfAvatar = class extends HTMLElement {
  connectedCallback() {
    var initials = this.getAttribute('initials') || '?';
    var size = this.getAttribute('size') || '';
    var color = this.getAttribute('color') || 'accent';
    this.className = 'avatar' + (size ? ' avatar-' + size : '');
    this.style.background = 'var(--' + color + ')';
    this.textContent = initials;
  }
};
customElements.define('alf-avatar', AlfAvatar);

// --- alf-bar-chart ---
var AlfBarChart = class extends HTMLElement {
  connectedCallback() {
    this.classList.add('bar-chart');
    if (this.getAttribute('height')) this.style.height = this.getAttribute('height');
  }
  set data(v) {
    this._data = v || [];
    var max = Math.max.apply(null, this._data.map(function(d) { return d.value; }).concat([1]));
    this.innerHTML = '<div class="bar-chart-bars">' + this._data.map(function(d) {
      var pct = (d.value / max) * 100;
      var color = d.color ? 'var(--' + d.color + ')' : 'var(--accent)';
      return '<div class="bar-chart-col"><div class="bar-chart-bar" style="height:' + pct + '%;background:' + color + '"></div><div class="bar-chart-label">' + (d.label || '') + '</div></div>';
    }).join('') + '</div>';
  }
  get data() { return this._data || []; }
};
customElements.define('alf-bar-chart', AlfBarChart);

// --- alf-breadcrumb ---
var AlfBreadcrumb = class extends HTMLElement {
  connectedCallback() {
    var crumbs = Array.from(this.querySelectorAll('alf-crumb'));
    this.classList.add('breadcrumb');
    crumbs.forEach(function(c, i) {
      var href = c.getAttribute('href');
      var isLast = i === crumbs.length - 1;
      var el = href && !isLast ? document.createElement('a') : document.createElement('span');
      if (href && !isLast) el.href = href;
      el.textContent = c.textContent;
      if (isLast) el.classList.add('breadcrumb-current');
      c.replaceWith(el);
      if (!isLast) {
        var sep = document.createElement('span');
        sep.className = 'breadcrumb-sep';
        sep.textContent = '/';
        el.after(sep);
      }
    });
  }
};
var AlfCrumb = class extends HTMLElement {};
customElements.define('alf-breadcrumb', AlfBreadcrumb);
customElements.define('alf-crumb', AlfCrumb);

// --- alf-btn-group ---
var AlfBtnGroup = class extends HTMLElement {
  connectedCallback() {
    var val = this.getAttribute('value') || '';
    this.classList.add('btn-group');
    this._update(val);
    var self = this;
    this.addEventListener('click', function(e) {
      var btn = e.target.closest('[data-value]');
      if (!btn) return;
      self.setAttribute('value', btn.dataset.value);
      self._update(btn.dataset.value);
      fire(self, 'alf-change', { value: btn.dataset.value });
    });
  }
  _update(val) {
    this.querySelectorAll('[data-value]').forEach(function(b) {
      b.classList.toggle('active', b.dataset.value === val);
    });
  }
};
customElements.define('alf-btn-group', AlfBtnGroup);

// --- alf-carousel ---
var AlfCarousel = class extends HTMLElement {
  connectedCallback() {
    var variant = this.getAttribute('variant') || 'cards';
    this.classList.add('carousel', 'carousel-' + variant);
    var items = Array.from(this.children);
    var track = document.createElement('div');
    track.className = 'carousel-track';
    items.forEach(function(item) {
      var wrap = document.createElement('div');
      wrap.className = 'carousel-item';
      wrap.appendChild(item);
      track.appendChild(wrap);
    });
    this.innerHTML = '';
    this.appendChild(track);
  }
};
customElements.define('alf-carousel', AlfCarousel);

// --- alf-chip ---
var AlfChip = class extends HTMLElement {
  connectedCallback() {
    var text = this.textContent;
    this.classList.add('chip');
    this.innerHTML = '<span>' + text + '</span><button class="chip-remove">' + ICONS.close + '</button>';
    var self = this;
    this.querySelector('.chip-remove').addEventListener('click', function() {
      fire(self, 'alf-remove', { value: text });
      self.remove();
    });
  }
};
customElements.define('alf-chip', AlfChip);

// --- alf-danger-zone ---
var AlfDangerZone = class extends HTMLElement {
  connectedCallback() { this.classList.add('danger-zone'); }
};
customElements.define('alf-danger-zone', AlfDangerZone);

// --- alf-data-table ---
var AlfDataTable = class extends HTMLElement {
  connectedCallback() { this._columns = Array.from(this.querySelectorAll('alf-column')); this._render(); }
  set data(v) { this._data = v; this._render(); }
  get data() { return this._data || []; }
  _render() {
    if (!this._columns) return;
    var cols = this._columns;
    var rows = this._data || [];
    var html = '<table class="table"><thead><tr>';
    cols.forEach(function(c) { html += '<th>' + esc(c.getAttribute('label') || '') + '</th>'; });
    html += '</tr></thead><tbody>';
    rows.forEach(function(row) {
      html += '<tr>';
      cols.forEach(function(c) {
        var key = c.getAttribute('key') || '';
        var type = c.getAttribute('type') || 'text';
        var val = row[key];
        if (type === 'tag' && val && typeof val === 'object') {
          html += '<td><span class="tag tag-' + (val.variant || 'accent') + '">' + esc(val.text || '') + '</span></td>';
        } else {
          html += '<td>' + esc(String(val || '')) + '</td>';
        }
      });
      html += '</tr>';
    });
    html += '</tbody></table>';
    cols.forEach(function(c) { c.style.display = 'none'; });
    var wrap = this.querySelector('._alf-table-wrap');
    if (!wrap) { wrap = document.createElement('div'); wrap.className = '_alf-table-wrap'; this.appendChild(wrap); }
    wrap.innerHTML = html;
  }
};
var AlfColumn = class extends HTMLElement {};
customElements.define('alf-data-table', AlfDataTable);
customElements.define('alf-column', AlfColumn);

// --- alf-dialog ---
var AlfDialog = class extends HTMLElement {
  open() {
    var label = this.getAttribute('label') || 'Dialog';
    var fields = Array.from(this.querySelectorAll('alf-input, alf-select'));
    var html = '<h3>' + esc(label) + '</h3><hr class="divider">';
    fields.forEach(function(f) {
      var tag = f.tagName.toLowerCase();
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
};
customElements.define('alf-dialog', AlfDialog);

// --- alf-drawer ---
var AlfDrawer = class extends HTMLElement {
  connectedCallback() {
    var label = this.getAttribute('label') || '';
    var content = this.innerHTML;
    this.innerHTML = '';
    this._overlay = document.createElement('div');
    this._overlay.className = 'drawer-overlay';
    var self = this;
    this._overlay.addEventListener('click', function() { self.close(); });
    this._panel = document.createElement('div');
    this._panel.className = 'drawer-panel';
    this._panel.innerHTML = '<div class="drawer-header"><h3>' + label + '</h3><button class="btn-icon drawer-close"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M18 6L6 18M6 6l12 12"/></svg></button></div><div class="drawer-body">' + content + '</div>';
    this._panel.querySelector('.drawer-close').addEventListener('click', function() { self.close(); });
    this.appendChild(this._overlay);
    this.appendChild(this._panel);
    this.classList.add('drawer');
  }
  get body() { return this._panel ? this._panel.querySelector('.drawer-body') : null; }
  open() { this.classList.add('open'); fire(this, 'alf-open', {}); }
  close() { this.classList.remove('open'); fire(this, 'alf-close', {}); }
};
customElements.define('alf-drawer', AlfDrawer);

// --- alf-dropdown ---
var AlfDropdown = class extends HTMLElement {
  connectedCallback() {
    var self = this;
    var trigger = this.querySelector('[slot="trigger"]');
    var items = Array.from(this.querySelectorAll('alf-menu-item, alf-menu-divider'));
    var wrap = document.createElement('div');
    wrap.className = 'dropdown';
    var menu = document.createElement('div');
    menu.className = 'dropdown-menu';
    items.forEach(function(item) {
      if (item.tagName === 'ALF-MENU-DIVIDER') {
        menu.innerHTML += '<hr class="divider">';
      } else {
        var btn = document.createElement('button');
        btn.className = 'dropdown-item' + (item.hasAttribute('danger') ? ' danger' : '');
        btn.textContent = item.textContent;
        btn.addEventListener('click', function() {
          wrap.classList.remove('open');
          fire(self, 'alf-select', { value: item.getAttribute('value') || '' });
        });
        menu.appendChild(btn);
      }
      item.style.display = 'none';
    });
    if (trigger) {
      trigger.removeAttribute('slot');
      trigger.addEventListener('click', function(e) {
        e.stopPropagation();
        wrap.classList.toggle('open');
      });
      wrap.appendChild(trigger);
    }
    wrap.appendChild(menu);
    this.appendChild(wrap);
    document.addEventListener('click', function() { wrap.classList.remove('open'); });
  }
};
var AlfMenuItem = class extends HTMLElement {};
var AlfMenuDivider = class extends HTMLElement {};
customElements.define('alf-dropdown', AlfDropdown);
customElements.define('alf-menu-item', AlfMenuItem);
customElements.define('alf-menu-divider', AlfMenuDivider);

// --- alf-dropzone ---
var AlfDropzone = class extends HTMLElement {
  connectedCallback() {
    var self = this;
    var hint = this.getAttribute('hint') || '';
    var label = this.getAttribute('label') || 'Drop files here or click to browse';
    var compact = this.hasAttribute('compact');
    var multiple = this.hasAttribute('multiple');
    var accept = this.getAttribute('accept') || '';
    this.classList.add('dropzone');
    if (compact) this.classList.add('dropzone-compact');
    this.innerHTML = '<input type="file" style="display:none" ' + (multiple ? 'multiple' : '') + ' ' + (accept ? 'accept="' + accept + '"' : '') + '><div class="dropzone-content"><p>' + label + '</p>' + (hint ? '<small class="text-dim">' + hint + '</small>' : '') + '</div>';
    var input = this.querySelector('input');
    this.querySelector('.dropzone-content').addEventListener('click', function() { input.click(); });
    input.addEventListener('change', function() { fire(self, 'alf-files', { files: Array.from(input.files) }); });
    this.addEventListener('dragover', function(e) { e.preventDefault(); self.classList.add('dragover'); });
    this.addEventListener('dragleave', function() { self.classList.remove('dragover'); });
    this.addEventListener('drop', function(e) { e.preventDefault(); self.classList.remove('dragover'); fire(self, 'alf-files', { files: Array.from(e.dataTransfer.files) }); });
  }
};
customElements.define('alf-dropzone', AlfDropzone);

// --- alf-empty-state ---
var AlfEmptyState = class extends HTMLElement {
  connectedCallback() {
    var self = this;
    var msg = this.getAttribute('message') || 'No items yet';
    var action = this.getAttribute('action') || '';
    var hideIcon = this.getAttribute('icon') === 'none';
    this.classList.add('empty-state');
    this.innerHTML = (hideIcon ? '' : ICONS.empty) + '<p>' + msg + '</p>' + (action ? '<button class="btn btn-primary btn-sm">' + action + '</button>' : '');
    var btn = this.querySelector('button');
    if (btn) btn.addEventListener('click', function() { fire(self, 'alf-action', {}); });
  }
};
customElements.define('alf-empty-state', AlfEmptyState);

// --- alf-hbar ---
var AlfHbar = class extends HTMLElement {
  connectedCallback() {
    var label = this.getAttribute('label') || '';
    var value = this.getAttribute('value') || '';
    var pct = this.getAttribute('percent') || '0';
    var color = this.getAttribute('color') || 'accent';
    this.classList.add('hbar');
    this.innerHTML = '<div class="hbar-header"><span>' + label + '</span><span>' + value + '</span></div><div class="hbar-track"><div class="hbar-fill" style="width:' + pct + '%;background:var(--' + color + ')"></div></div>';
  }
};
customElements.define('alf-hbar', AlfHbar);

// --- alf-input ---
var AlfInput = class extends HTMLElement {
  connectedCallback() {
    if (this.closest('alf-dialog')) return;
    this._render();
  }
  _render() {
    var label = this.getAttribute('label') || '';
    var name = this.getAttribute('name') || '';
    var type = this.getAttribute('type') || 'text';
    var ph = this.getAttribute('placeholder') || '';
    var hint = this.getAttribute('hint') || '';
    var val = this._value !== undefined ? this._value : (this.getAttribute('value') || '');
    var req = this.hasAttribute('required') ? 'required' : '';
    var dis = this.hasAttribute('disabled') ? 'disabled' : '';
    var isTextarea = type === 'textarea';
    var inputEl = isTextarea
      ? '<textarea class="input" name="' + esc(name) + '" placeholder="' + esc(ph) + '" ' + req + ' ' + dis + '>' + esc(val) + '</textarea>'
      : '<input class="input" type="' + type + '" name="' + esc(name) + '" placeholder="' + esc(ph) + '" value="' + esc(val) + '" ' + req + ' ' + dis + '>';
    this.innerHTML = '<div class="form-group">' +
      (label ? '<label class="form-label">' + esc(label) + '</label>' : '') +
      inputEl +
      (hint ? '<small class="form-hint">' + esc(hint) + '</small>' : '') +
      '</div>';
  }
  get value() {
    var el = this.querySelector('input,textarea');
    if (el) return el.value;
    return this._value !== undefined ? this._value : (this.getAttribute('value') || '');
  }
  set value(v) {
    this._value = v;
    var el = this.querySelector('input,textarea');
    if (el) el.value = v;
  }
};
customElements.define('alf-input', AlfInput);

// --- alf-input-row ---
var AlfInputRow = class extends HTMLElement {
  connectedCallback() {
    var self = this;
    var ph = this.getAttribute('placeholder') || '';
    var btn = this.getAttribute('button') || 'Add';
    var name = this.getAttribute('name') || '';
    this.classList.add('input-row');
    this.innerHTML = '<input class="input flex-1" type="text" name="' + esc(name) + '" placeholder="' + esc(ph) + '"><button class="btn btn-primary btn-sm">' + esc(btn) + '</button>';
    var input = this.querySelector('input');
    var submit = function() {
      if (!input.value.trim()) return;
      fire(self, 'alf-submit', { value: input.value.trim() });
      input.value = '';
    };
    this.querySelector('button').addEventListener('click', submit);
    input.addEventListener('keydown', function(e) { if (e.key === 'Enter') submit(); });
  }
};
customElements.define('alf-input-row', AlfInputRow);

// --- alf-kv-row ---
var AlfKvRow = class extends HTMLElement {
  connectedCallback() {
    var label = this.getAttribute('label') || '';
    var content = this.innerHTML;
    this.classList.add('kv-row');
    this.innerHTML = '<span class="kv-label">' + esc(label) + '</span><span class="kv-value">' + content + '</span>';
  }
};
customElements.define('alf-kv-row', AlfKvRow);

// --- alf-list ---
var AlfList = class extends HTMLElement {
  connectedCallback() {
    if (!this.querySelector('.card-group')) {
      var wrap = document.createElement('div');
      wrap.className = 'card-group';
      while (this.firstChild) wrap.appendChild(this.firstChild);
      this.appendChild(wrap);
    }
  }
};
var AlfListItem = class extends HTMLElement {
  connectedCallback() {
    var hasCheckbox = this.hasAttribute('checkbox');
    var isChecked = this.hasAttribute('checked');
    if (hasCheckbox) {
      var check = document.createElement('div');
      check.className = 'check' + (isChecked ? ' checked' : '');
      var self = this;
      check.addEventListener('click', function() {
        self.checked = !self.checked;
      });
      this.insertBefore(check, this.firstChild);
    }
    this.classList.add('list-item-interactive');
  }
  get checked() { var c = this.querySelector('.check'); return c ? c.classList.contains('checked') : false; }
  set checked(v) {
    var c = this.querySelector('.check');
    if (c) { c.classList.toggle('checked', v); this.toggleAttribute('checked', v); fire(this, 'alf-check-change', { checked: v }); }
  }
};
customElements.define('alf-list', AlfList);
customElements.define('alf-list-item', AlfListItem);

// --- alf-loading ---
var AlfLoading = class extends HTMLElement {
  static get observedAttributes() { return ['active', 'variant', 'message']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    var active = this.hasAttribute('active');
    var variant = this.getAttribute('variant') || 'spinner';
    var msg = this.getAttribute('message') || '';
    this.style.display = active ? '' : 'none';
    if (variant === 'skeleton') {
      this.innerHTML = '<div class="skeleton-lines"><div class="skeleton-line"></div><div class="skeleton-line skeleton-line-short"></div><div class="skeleton-line"></div></div>';
    } else {
      this.innerHTML = '<div class="loading-spinner">' + (msg ? '<span class="loading-message">' + msg + '</span>' : '') + '</div>';
    }
  }
  show() { this.setAttribute('active', ''); }
  hide() { this.removeAttribute('active'); }
};
customElements.define('alf-loading', AlfLoading);

// --- alf-nav-row ---
var AlfNavRow = class extends HTMLElement {
  static get observedAttributes() { return ['label']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    var self = this;
    var label = this.getAttribute('label') || '';
    this.classList.add('nav-row');
    this.innerHTML = '<button class="btn-icon nav-prev">' + ICONS.chevronLeft + '</button><span class="nav-label">' + label + '</span><button class="btn-icon nav-next">' + ICONS.chevronRight + '</button>';
    this.querySelector('.nav-prev').addEventListener('click', function() { fire(self, 'alf-nav', { direction: 'prev' }); });
    this.querySelector('.nav-next').addEventListener('click', function() { fire(self, 'alf-nav', { direction: 'next' }); });
  }
};
customElements.define('alf-nav-row', AlfNavRow);

// --- alf-pagination ---
var AlfPagination = class extends HTMLElement {
  static get observedAttributes() { return ['total', 'page-size', 'current']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    var self = this;
    var total = parseInt(this.getAttribute('total') || '0');
    var size = parseInt(this.getAttribute('page-size') || '10');
    var current = parseInt(this.getAttribute('current') || '1');
    var pages = Math.ceil(total / size);
    if (pages <= 1) { this.innerHTML = ''; return; }
    var html = '<div class="pagination">';
    for (var i = 1; i <= pages; i++) {
      html += '<button class="pagination-btn' + (i === current ? ' active' : '') + '" data-page="' + i + '">' + i + '</button>';
    }
    html += '</div>';
    this.innerHTML = html;
    this.querySelectorAll('button').forEach(function(btn) {
      btn.addEventListener('click', function() {
        self.setAttribute('current', btn.dataset.page);
        fire(self, 'alf-page-change', { page: parseInt(btn.dataset.page) });
      });
    });
  }
};
customElements.define('alf-pagination', AlfPagination);

// --- alf-progress ---
var AlfProgress = class extends HTMLElement {
  static get observedAttributes() { return ['value', 'max', 'label']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    var value = parseInt(this.getAttribute('value') || '0');
    var max = parseInt(this.getAttribute('max') || '100');
    var label = this.getAttribute('label') || '';
    var pct = max > 0 ? Math.round((value / max) * 100) : 0;
    this.innerHTML = '<div class="progress-wrap">' + (label ? '<div class="progress-label">' + label + '</div>' : '') + '<div class="progress-bar"><div class="progress-fill" style="width:' + pct + '%"></div></div></div>';
  }
};
customElements.define('alf-progress', AlfProgress);

// --- alf-ring-chart ---
var AlfRingChart = class extends HTMLElement {
  static get observedAttributes() { return ['value', 'color']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    var val = parseInt(this.getAttribute('value') || '0');
    var color = this.getAttribute('color') || 'accent';
    var r = 36, c = 2 * Math.PI * r;
    var offset = c - (val / 100) * c;
    this.innerHTML = '<svg width="80" height="80" viewBox="0 0 80 80"><circle cx="40" cy="40" r="' + r + '" fill="none" stroke="var(--border)" stroke-width="6"/><circle cx="40" cy="40" r="' + r + '" fill="none" stroke="var(--' + color + ')" stroke-width="6" stroke-dasharray="' + c + '" stroke-dashoffset="' + offset + '" stroke-linecap="round" transform="rotate(-90 40 40)"/><text x="40" y="44" text-anchor="middle" fill="var(--text)" font-size="14" font-weight="600">' + val + '%</text></svg>';
  }
};
customElements.define('alf-ring-chart', AlfRingChart);

// --- alf-search-box ---
var AlfSearchBox = class extends HTMLElement {
  connectedCallback() {
    var self = this;
    var ph = this.getAttribute('placeholder') || 'Search...';
    this.innerHTML = '<div class="search-box">' + ICONS.search + '<input type="text" placeholder="' + ph + '"></div>';
    this.querySelector('input').addEventListener('input', function(e) {
      fire(self, 'alf-search', { value: e.target.value });
    });
  }
  get value() { var el = this.querySelector('input'); return el ? el.value : ''; }
  set value(v) { var el = this.querySelector('input'); if (el) el.value = v; }
};
customElements.define('alf-search-box', AlfSearchBox);

// --- alf-select ---
var AlfSelect = class extends HTMLElement {
  connectedCallback() {
    if (this.closest('alf-dialog')) return;
    this._render();
  }
  _render() {
    var label = this.getAttribute('label') || '';
    var name = this.getAttribute('name') || '';
    var ph = this.getAttribute('placeholder') || '';
    var dis = this.hasAttribute('disabled') ? 'disabled' : '';
    var options = Array.from(this.querySelectorAll('option'));
    var selectedVal = this._value;
    var optionsHtml = (ph ? '<option value="" disabled selected>' + esc(ph) + '</option>' : '') +
      options.map(function(o) {
        var sel = selectedVal !== undefined ? (o.value === selectedVal ? ' selected' : '') : (o.selected ? ' selected' : '');
        return '<option value="' + esc(o.value) + '"' + sel + '>' + o.textContent + '</option>';
      }).join('');
    this.innerHTML = '<div class="form-group">' +
      (label ? '<label class="form-label">' + esc(label) + '</label>' : '') +
      '<select class="input" name="' + esc(name) + '" ' + dis + '>' + optionsHtml + '</select></div>';
  }
  get value() {
    var el = this.querySelector('select');
    if (el) return el.value;
    return this._value !== undefined ? this._value : '';
  }
  set value(v) {
    this._value = v;
    var el = this.querySelector('select');
    if (el) el.value = v;
  }
};
customElements.define('alf-select', AlfSelect);

// --- alf-settings-row ---
var AlfSettingsRow = class extends HTMLElement {
  connectedCallback() {
    var label = this.getAttribute('label') || '';
    var desc = this.getAttribute('description') || '';
    var children = Array.from(this.childNodes).filter(function(n) { return n.nodeType === 1; });
    this.classList.add('settings-row');
    var left = document.createElement('div');
    left.className = 'settings-row-text';
    left.innerHTML = '<div class="settings-row-label">' + label + '</div>' + (desc ? '<div class="settings-row-desc">' + desc + '</div>' : '');
    var right = document.createElement('div');
    right.className = 'settings-row-action';
    children.forEach(function(c) { right.appendChild(c); });
    this.innerHTML = '';
    this.appendChild(left);
    this.appendChild(right);
  }
};
customElements.define('alf-settings-row', AlfSettingsRow);

// --- alf-sparkline ---
var AlfSparkline = class extends HTMLElement {
  connectedCallback() { this.classList.add('sparkline'); }
  set data(v) {
    this._data = v || [];
    var color = this.getAttribute('color') || 'accent';
    var max = Math.max.apply(null, this._data.concat([1]));
    var w = 100, h = 30;
    var data = this._data;
    var points = data.map(function(d, i) { return ((i / (data.length - 1)) * w) + ',' + (h - (d / max) * h); }).join(' ');
    this.innerHTML = '<svg viewBox="0 0 ' + w + ' ' + h + '" preserveAspectRatio="none" style="width:100%;height:' + h + 'px"><polyline points="' + points + '" fill="none" stroke="var(--' + color + ')" stroke-width="2"/></svg>';
  }
  get data() { return this._data || []; }
};
customElements.define('alf-sparkline', AlfSparkline);

// --- alf-stat-grid ---
var AlfStatGrid = class extends HTMLElement {
  connectedCallback() { this.classList.add('stat-grid'); }
};
var AlfStat = class extends HTMLElement {
  static get observedAttributes() { return ['value', 'label', 'color']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    var value = this.getAttribute('value') || '0';
    var label = this.getAttribute('label') || '';
    var color = this.getAttribute('color') || 'accent';
    this.className = 'stat-item';
    this.innerHTML = '<div class="stat-bar" style="background:var(--' + color + ')"></div><div class="stat-value">' + value + '</div><div class="stat-label">' + label + '</div>';
  }
};
customElements.define('alf-stat-grid', AlfStatGrid);
customElements.define('alf-stat', AlfStat);

// --- alf-tabs ---
var AlfTabs = class extends HTMLElement {
  static get observedAttributes() { return ['value']; }
  connectedCallback() {
    // Defer render to ensure child alf-tab elements are parsed
    var self = this;
    requestAnimationFrame(function() { self._render(); });
  }
  attributeChangedCallback() { if (this._rendered) this._render(); }
  _render() {
    this._rendered = true;
    var self = this;
    var variant = this.getAttribute('variant') || 'filter';
    var val = this.getAttribute('value') || '';
    var tabs = Array.from(this.querySelectorAll('alf-tab'));
    if (!tabs.length) return; // children not ready yet
    var wrapClass = variant === 'underline' ? 'tab-bar tab-underline' : 'filter-tabs';
    var existing = this.querySelector('._alf-tabs-wrap');
    if (!existing) {
      existing = document.createElement('div');
      existing.className = '_alf-tabs-wrap';
      tabs.forEach(function(t) { t.style.display = 'none'; });
      this.appendChild(existing);
    }
    existing.className = '_alf-tabs-wrap ' + wrapClass;
    existing.innerHTML = tabs.map(function(t) {
      var v = t.getAttribute('value') || '';
      var active = v === val ? ' active' : '';
      return '<button class="tab' + active + '" data-value="' + v + '">' + t.textContent + '</button>';
    }).join('');
    existing.querySelectorAll('button').forEach(function(btn) {
      btn.addEventListener('click', function() {
        self.setAttribute('value', btn.dataset.value);
        fire(self, 'alf-tab-change', { value: btn.dataset.value });
      });
    });
  }
};
var AlfTab = class extends HTMLElement {};
customElements.define('alf-tabs', AlfTabs);
customElements.define('alf-tab', AlfTab);

// --- alf-tag ---
var AlfTag = class extends HTMLElement {
  connectedCallback() {
    var variant = this.getAttribute('variant') || 'accent';
    var outline = this.hasAttribute('outline') ? ' tag-outline' : '';
    this.className = 'tag tag-' + variant + outline;
  }
};
customElements.define('alf-tag', AlfTag);

// --- alf-toggle ---
var AlfToggle = class extends HTMLElement {
  connectedCallback() {
    var self = this;
    var checked = this.hasAttribute('checked');
    var name = this.getAttribute('name') || '';
    this.innerHTML = '<label class="toggle"><input type="checkbox" ' + (checked ? 'checked' : '') + ' name="' + name + '"><span class="toggle-slider"></span></label>';
    this.querySelector('input').addEventListener('change', function(e) {
      self.toggleAttribute('checked', e.target.checked);
      fire(self, 'alf-change', { checked: e.target.checked, name: name });
    });
  }
  get checked() { var el = this.querySelector('input'); return el ? el.checked : false; }
  set checked(v) { var el = this.querySelector('input'); if (el) { el.checked = v; this.toggleAttribute('checked', v); } }
};
customElements.define('alf-toggle', AlfToggle);

// --- alf-toolbar ---
var AlfToolbar = class extends HTMLElement {
  connectedCallback() {
    var self = this;
    var searchPh = this.getAttribute('search') || '';
    this.classList.add('toolbar');
    if (searchPh) {
      var box = document.createElement('div');
      box.className = 'search-box';
      box.style.flex = '1';
      box.innerHTML = ICONS.search + '<input type="text" placeholder="' + searchPh + '">';
      var input = box.querySelector('input');
      input.addEventListener('input', function() { fire(self, 'alf-search', { value: input.value }); });
      this.insertBefore(box, this.firstChild);
      this._searchInput = input;
    }
  }
  get searchValue() { return this._searchInput ? this._searchInput.value : ''; }
};
customElements.define('alf-toolbar', AlfToolbar);

})();
