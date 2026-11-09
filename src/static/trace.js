'use strict';
(() => {
  const valid=value=>typeof value==='string' && /^[A-Za-z0-9_-]{1,80}$/.test(value);
  function randomId(){
    if(typeof crypto!=='undefined'){
      if(typeof crypto.randomUUID==='function')return crypto.randomUUID();
      if(typeof crypto.getRandomValues==='function'){
        return Array.from(crypto.getRandomValues(new Uint8Array(16)),byte=>byte.toString(16).padStart(2,'0')).join('');
      }
    }
    // Diagnostic labels only: allow tracing even without the Web Crypto API.
    return Date.now().toString(36)+'-'+Math.random().toString(36).slice(2)+'-'+Math.random().toString(36).slice(2);
  }
  let clientId;
  try{
    clientId=localStorage.getItem('pushieClientId');
    if(!valid(clientId)){clientId=randomId();localStorage.setItem('pushieClientId',clientId)}
  }catch(_){clientId=randomId()}
  // A fresh page ID distinguishes tabs and app windows sharing local storage.
  const pageId=randomId();
  function identifiers(){
    let deviceId='';
    try{deviceId=localStorage.getItem('webpushDeviceId') || ''}catch(_){}
    return {client_id:clientId,page_id:pageId,push_device_id:valid(deviceId)?deviceId:'',app_version:window.pushieVersion || '0'};
  }
  window.clientTrace={
    headers(){const ids=identifiers();return {'X-Client-ID':ids.client_id,'X-Page-ID':ids.page_id,'X-Push-Device-ID':ids.push_device_id,'X-App-Version':ids.app_version}},
    socketURL(url){for(const [key,value] of Object.entries(identifiers()))if(value)url.searchParams.set(key,value)}
  };
})();
