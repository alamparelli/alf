// Apply saved palette immediately (before first paint) to prevent FOUC.
(function(){var p=localStorage.getItem('alf-palette')||'sage';if(p==='catppuccin'){p='studio';localStorage.setItem('alf-palette',p);}document.getElementById('alf-theme-link').href='/static/theme-'+p+'.css';})();
