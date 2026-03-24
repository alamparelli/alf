// Compatibility shim for legacy apps that load /static/theme-init.js
(function(){var p=localStorage.getItem('alf-palette')||'sage';var valid=['sage','studio','catppuccin','dracula','solarized','tokyo-night','github','nord'];if(valid.indexOf(p)<0)p='sage';var link=document.getElementById('alf-theme-link')||document.getElementById('alf-theme');if(link)link.href='/static/theme-'+p+'.css';})();
