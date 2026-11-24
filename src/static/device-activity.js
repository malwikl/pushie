'use strict';
(() => {
 let challenge='',lastSent=0,inFlight=false,epoch=0;
 const base64url=bytes=>btoa(String.fromCharCode(...new Uint8Array(bytes))).replaceAll('+','-').replaceAll('/','_').replace(/=+$/,'');
 async function report(nextChallenge){
  if(nextChallenge && nextChallenge!==challenge){challenge=nextChallenge;lastSent=0;epoch++}
  if(!challenge || inFlight || Date.now()-lastSent<60000 || document.visibilityState!=='visible')return;
  const currentEpoch=epoch,currentChallenge=challenge;
  inFlight=true;
  try{
   const deviceId=localStorage.getItem('webpushDeviceId');
   if(!deviceId || !globalThis.crypto?.subtle || !navigator.serviceWorker)return;
   const registration=await navigator.serviceWorker.getRegistration();
   const subscription=await registration?.pushManager?.getSubscription();
   const secret=subscription?.getKey('auth');
   if(!secret || currentEpoch!==epoch)return;
   const key=await crypto.subtle.importKey('raw',secret,{name:'HMAC',hash:'SHA-256'},false,['sign']);
   const proof=base64url(await crypto.subtle.sign('HMAC',key,new TextEncoder().encode('pushie-device-activity-v1:'+currentChallenge+':'+deviceId)));
   if(currentEpoch!==epoch)return;
   // Set before sending: live-sync refreshes must not cause a reporting loop.
   lastSent=Date.now();
   const response=await fetch('/api/me/device-activity',{method:'POST',credentials:'same-origin',cache:'no-store',headers:{...window.clientTrace?.headers(),'Content-Type':'application/json'},body:JSON.stringify({deviceId,proof})});
   if(response.status===401 && currentEpoch===epoch)clear();
  }catch(_){}finally{inFlight=false}
 }
 function clear(){challenge='';lastSent=0;epoch++}
 window.deviceActivity={report,clear};
 setInterval(()=>report(),60000);
 document.addEventListener('visibilitychange',()=>report());
})();
