class AlfSettingsRow extends HTMLElement {
  connectedCallback() {
    const label = this.getAttribute('label') || '';
    const desc = this.getAttribute('description') || '';
    const children = Array.from(this.childNodes).filter(n => n.nodeType === 1);
    this.classList.add('settings-row');
    const left = document.createElement('div');
    left.className = 'settings-row-text';
    left.innerHTML = `<div class="settings-row-label">${label}</div>${desc ? `<div class="settings-row-desc">${desc}</div>` : ''}`;
    const right = document.createElement('div');
    right.className = 'settings-row-action';
    children.forEach(c => right.appendChild(c));
    this.innerHTML = '';
    this.appendChild(left);
    this.appendChild(right);
  }
}
customElements.define('alf-settings-row', AlfSettingsRow);
