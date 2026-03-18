// Apply saved palette immediately (before first paint) to prevent FOUC.
(function(){var p=localStorage.getItem('alf-palette')||'sage';var valid=['sage','studio','catppuccin','dracula','solarized','tokyo-night','github','nord'];if(valid.indexOf(p)<0)p='sage';document.getElementById('alf-theme-link').href='/static/theme-'+p+'.css';})();
