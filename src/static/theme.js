 'use strict';
// Apply before loading styles to avoid a flash of the wrong appearance.
const systemTheme = window.matchMedia('(prefers-color-scheme: dark)');
let themePreference = 'auto';
let themeOverride = null;
try { const saved = localStorage.getItem('webpushTheme'); if (['light','dark'].includes(saved)) themePreference = saved; else localStorage.removeItem('webpushTheme'); } catch (_) {}
function renderTheme(){
 const effectiveTheme=themeOverride || themePreference;
 const dark = effectiveTheme === 'dark' || (effectiveTheme === 'auto' && systemTheme.matches);
 document.documentElement.dataset.theme = dark ? 'dark' : 'light';
 const meta = document.querySelector('meta[name="theme-color"]');
 const toggle=document.getElementById('themeToggle');
 if(toggle)toggle.setAttribute('aria-pressed',String(dark));
 if(meta)meta.content = dark ? '#101827' : '#eef2f7';
}
function applyTheme(value){
 themePreference = ['auto','light','dark'].includes(value) ? value : 'auto';
 try { if(themePreference==='auto')localStorage.removeItem('webpushTheme');else localStorage.setItem('webpushTheme',themePreference); } catch (_) {}
 renderTheme();
}
if(systemTheme.addEventListener)systemTheme.addEventListener('change',renderTheme);
else systemTheme.addListener(renderTheme);
renderTheme();
