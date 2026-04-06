class AlfHbar extends HTMLElement {
  connectedCallback() {
    const label = this.getAttribute('label') || '';
    const value = this.getAttribute('value') || '';
    const pct = this.getAttribute('percent') || '0';
    const color = this.getAttribute('color') || 'accent';
    this.classList.add('hbar');
    this.innerHTML = `<div class="hbar-header"><span>${label}</span><span>${value}</span></div><div class="hbar-track"><div class="hbar-fill" style="width:${pct}%;background:var(--${color})"></div></div>`;
  }
}
customElements.define('alf-hbar', AlfHbar);
