// Notification fixes should activate without waiting for every app window to close.
self.addEventListener('install', event => {event.waitUntil(self.skipWaiting())});
const appBuildVersion='__PUSHIE_VERSION__';
self.addEventListener('activate', event => {event.waitUntil((async()=>{
  await clients.claim();
  const windows=await clients.matchAll({type:'window'});
  await Promise.all(windows.map(async client=>{
    const url=new URL(client.url);
    if(url.origin!==self.location.origin)return;
    const previous=url.searchParams.get('_build') || '0';
    if(!/^\d{1,20}$/.test(appBuildVersion) || !/^\d{1,20}$/.test(previous))return;
    if(previous.length>appBuildVersion.length || (previous.length===appBuildVersion.length && previous>=appBuildVersion))return;
    url.searchParams.set('_build',appBuildVersion);
    try{await client.navigate(url.href)}catch(_){/* The page version check retries on return. */}
  }));
})())});
// Keep receipts until the server accepts them; never repeat the notification.
async function sendReceipt(receipt) {
  const response=await fetch('/api/push/ack', {
    method:'POST', credentials:'omit', cache:'no-store',
    headers:{'Content-Type':'application/json','X-Push-Device-ID':receipt.deviceId},
    body:JSON.stringify(receipt)
  });
  if(!response.ok)throw new Error(`Receipt HTTP ${response.status}`);
}
async function recordReceipt(receipt) {
  const key='/__push_receipt/'+encodeURIComponent(receipt.messageId)+'/'+encodeURIComponent(receipt.deviceId);
  let cache;
  try{cache=await caches.open('pushie-receipts');await cache.put(key,new Response(JSON.stringify(receipt)))}catch(_){}
  try{await sendReceipt(receipt);if(cache)await cache.delete(key)}catch(_){/* Retry when the app returns online. */}
}
async function retryReceipts() {
  try{
    const cache=await caches.open('pushie-receipts');
    for(const key of await cache.keys()){
      try{
        const receipt=await (await cache.match(key)).json();
        await sendReceipt(receipt);await cache.delete(key);
      }catch(_){/* Keep failed receipts for the next check. */}
    }
  }catch(_){/* Storage may be unavailable. */}
}
self.addEventListener('message',event=>{
  if(event.data?.type==='retry-push-receipts')event.waitUntil(retryReceipts().finally(()=>event.ports?.[0]?.postMessage({done:true})));
});
self.addEventListener('push', event => {
  let message = { title: 'Push', body: 'Neue Benachrichtigung' };
  try { if (event.data) message = event.data.json(); } catch (_) {}
  event.waitUntil((async () => {
    // The stable message ID prevents notifications from stacking during retries.
    const proposed=message.notification;
    const target=new URL('/',self.location.origin);
    if(message.id)target.searchParams.set('message',message.id);
    // Preserve native navigation when replacing a declarative notification.
    await self.registration.showNotification(proposed?.title || message.title || 'Push', {
      body: proposed?.body || message.body || '',
      navigate: proposed?.navigate || target.href,
      tag: message.id || undefined,
      renotify: false,
      icon: '/icon-192.png',
      data: { id: message.id || '' }
    });
    // This confirms execution of showNotification(), NOT that a human has read
    // it or that the operating system displayed an alert.
    if (message.id && message.deviceId && message.receiptSecret) {
      await recordReceipt({messageId:message.id,deviceId:message.deviceId,receiptSecret:message.receiptSecret});
    }
  })());
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  const id=event.notification.data?.id || event.notification.tag || '';
  const url=new URL('/',self.location.origin);
  if(id){url.searchParams.set('message',id);url.hash='message-'+encodeURIComponent(id)}else{url.hash='inboxPanel'}
  event.waitUntil((async()=>{
    // iOS may discard the launch URL and messages sent before the page is ready.
    // Persist the click so the foreground app can recover it independently.
    try{
      const cache=await caches.open('pushie-navigation');
      await cache.put('/__notification_target',new Response(JSON.stringify({id,createdAt:Date.now()})));
    }catch(_){/* Keep URL navigation available if storage is unavailable. */}
    const windows=await clients.matchAll({type:'window',includeUncontrolled:true});
    const existing=windows.find(client=>new URL(client.url).origin===url.origin && new URL(client.url).pathname==='/');
    if(existing){
      try{
        const target=await existing.navigate(url.href);
        if(target){await target.focus();target.postMessage({type:'open-message',id});return}
      }catch(_){/* An iOS launch client may not accept navigation yet. */}
      // Reuse the app even when WebKit cannot navigate its existing window.
      try{
        existing.postMessage({type:'open-message',id});
        await existing.focus();
        return;
      }catch(_){/* Fall back to opening a window if the client disappeared. */}
    }
    const opened=await clients.openWindow(url.href);
    if(opened){opened.postMessage({type:'open-message',id});await opened.focus()}
  })());
});
