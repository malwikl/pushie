'use strict';
(() => {
  const version=document.querySelector('meta[name="pushie-version"]')?.content || '0';
  window.pushieVersion=version;
  const valid=value=>typeof value==='string' && /^\d{1,20}$/.test(value);
  const newer=(next,current)=>next.length>current.length || (next.length===current.length && next>current);
  // Mark this window for service-worker upgrades without losing notification links.
  const currentURL=new URL(location.href);
  const requestedVersion=currentURL.searchParams.get('_build');
  currentURL.searchParams.set('_build',version);
  history.replaceState(null,'',currentURL.href);
  let pending=null,reloading=false,lastCheck=0;
  async function check(){
    if(document.hidden || window.authRefreshStopped || reloading)return;
    if(pending)return pending;
    if(Date.now()-lastCheck<5000)return;
    lastCheck=Date.now();
    pending=(async()=>{
      try{
        const response=await fetch('/api/version?_='+Date.now(),{cache:'no-store',credentials:'same-origin',headers:window.clientTrace?.headers()});
        if(!response.ok)return;
        const {version:next}=await response.json();
        if(window.authRefreshStopped || !valid(next) || !valid(version) || !newer(next,version))return;
        if(requestedVersion===next)return;
        // A stale intermediary must not trap the browser in an endless reload loop.
        const key='pushieReloadVersion';
        try{if(sessionStorage.getItem(key)===next)return;sessionStorage.setItem(key,next)}catch(_){}
        reloading=true;
        const url=new URL(location.href);url.searchParams.set('_build',next);
        location.replace(url.href);
      }catch(_){/* Offline clients keep their current page. */}
    })().finally(()=>{pending=null});
    return pending;
  }
  window.checkAppVersion=check;
  document.addEventListener('visibilitychange',()=>{if(!document.hidden)check()});
  for(const event of ['focus','pageshow','online'])window.addEventListener(event,check);
  setInterval(check,60000);
  check();
})();
