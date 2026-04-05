class AlfAppShell extends HTMLElement {
  connectedCallback() {
    const titleEl = this.querySelector('[slot="title"]');
    const actionEls = Array.from(this.querySelectorAll('[slot="actions"]'));
    const body = Array.from(this.childNodes).filter(n => {
      if (n.nodeType !== 1) return n.nodeType === 3 && n.textContent.trim();
      return !n.hasAttribute('slot');
    });
    const shell = document.createElement('div');
    shell.className = 'app-shell';
    const header = document.createElement('div');
    header.className = 'app-header';
    if (titleEl) {
      const h1 = document.createElement('h1');
      h1.className = 'app-header-title';
      h1.textContent = titleEl.textContent;
      header.appendChild(h1);
    }
    const spacer = document.createElement('span');
    spacer.className = 'spacer';
    header.appendChild(spacer);
    if (actionEls.length) {
      const acts = document.createElement('div');
      acts.className = 'app-header-actions';
      actionEls.forEach(a => { a.removeAttribute('slot'); acts.appendChild(a); });
      header.appendChild(acts);
    }
    shell.appendChild(header);
    const appBody = document.createElement('div');
    appBody.className = 'app-body';
    body.forEach(n => appBody.appendChild(n));
    shell.appendChild(appBody);
    this.innerHTML = '';
    if (titleEl) titleEl.remove();
    this.appendChild(shell);
  }
}
customElements.define('alf-app-shell', AlfAppShell);
