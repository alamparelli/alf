// Apply saved palette immediately (before first paint) to prevent FOUC.
(function(){var p=localStorage.getItem('alf-palette')||'sage';document.getElementById('alf-theme-link').href='/static/theme-'+p+'.css';})();
