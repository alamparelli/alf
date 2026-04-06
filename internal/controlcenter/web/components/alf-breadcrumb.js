class AlfBreadcrumb extends HTMLElement {
  connectedCallback() {
    const crumbs = Array.from(this.querySelectorAll('alf-crumb'));
    this.classList.add('breadcrumb');
    crumbs.forEach((c, i) => {
      const href = c.getAttribute('href');
      const isLast = i === crumbs.length - 1;
      const el = href && !isLast ? document.createElement('a') : document.createElement('span');
      if (href && !isLast) el.href = href;
      el.textContent = c.textContent;
      if (isLast) el.classList.add('breadcrumb-current');
      c.replaceWith(el);
      if (!isLast) {
        const sep = document.createElement('span');
        sep.className = 'breadcrumb-sep';
        sep.textContent = '/';
        el.after(sep);
      }
    });
  }
}
class AlfCrumb extends HTMLElement {}
customElements.define('alf-breadcrumb', AlfBreadcrumb);
customElements.define('alf-crumb', AlfCrumb);
