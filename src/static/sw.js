// Notification fixes should activate without waiting for every app window to close.
self.addEventListener('install', event => {event.waitUntil(self.skipWaiting())});
const appBuildVersion='__PUSHIE_VERSION__';
self.addEventListener('activate', event => {event.waitUntil((async()=>{
  await migrateNotificationReceipts();
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
// Upgrade queued receipts once; all live API requests use notificationId.
async function migrateNotificationReceipts() {
  try {
    const cache=await caches.open('pushie-receipts');
    for(const key of await cache.keys()){
      const receipt=await (await cache.match(key)).json();
      if(Object.hasOwn(receipt,'messageId')){
        receipt.notificationId=receipt.messageId;delete receipt.messageId;
        await cache.put(key,new Response(JSON.stringify(receipt)));
      }
    }
  }catch(_){/* Cache access can be unavailable in private mode. */}
}
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
  const key='/__push_receipt/'+encodeURIComponent(receipt.notificationId)+'/'+encodeURIComponent(receipt.deviceId);
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
async function appWindows() {
  try{
    const windows=await clients.matchAll({type:'window',includeUncontrolled:true});
    return windows.filter(client=>{
      const url=new URL(client.url);
      return url.origin===self.location.origin && url.pathname==='/';
    });
  }catch(_){return []}
}
let badgeRefreshVersion=0;
async function refreshUnreadBadgesOnPush() {
  const version=++badgeRefreshVersion;
  const windows=await appWindows();
  for(const client of windows){
    try{client.postMessage({type:'inbox-changed'})}catch(_){/* The window may have closed. */}
  }
  const isVisible=client=>client.visibilityState==='visible';
  if(windows.some(isVisible) || !self.navigator?.setAppBadge)return;
  try{
    // Fetch the current count so retries and delayed pushes cannot add unread items twice.
    const response=await fetch('/api/me/notifications/unread',{credentials:'same-origin',cache:'no-store'});
    if(!response.ok && response.status!==401)return;
    const unread=response.status===401?0:(await response.json()).unread;
    if(!Number.isSafeInteger(unread) || unread<0)return;
    // A window opened while the count was loading can apply a newer count after reading a notification.
    const currentWindows=await appWindows();
    if(version===badgeRefreshVersion && !currentWindows.some(isVisible))await self.navigator.setAppBadge(unread);
  }catch(_){/* A badge refresh must not interfere with push delivery. */}
}
self.addEventListener('push', event => {
  let notification = { title: 'Push', body: 'Neue Mitteilung' };
  try { if (event.data) notification = event.data.json(); } catch (_) {}
  event.waitUntil((async () => {
    // The stable notification ID prevents notifications from stacking during retries.
    const proposed=notification.notification;
    const target=new URL('/',self.location.origin);
    if(notification.id)target.searchParams.set('notification',notification.id);
    // Preserve native navigation when replacing a declarative notification.
    await self.registration.showNotification(proposed?.title || notification.title || 'Push', {
      body: proposed?.body || notification.body || '',
      navigate: proposed?.navigate || target.href,
      tag: notification.id || undefined,
      renotify: false,
      icon: '/icon-192.png',
      data: { id: notification.id || '' }
    });
    const badgeRefresh=refreshUnreadBadgesOnPush();
    // This confirms execution of showNotification(), NOT that a human has read
    // it or that the operating system displayed an alert.
    if(notification.id && notification.deviceId && notification.receiptSecret){
      await recordReceipt({notificationId:notification.id,deviceId:notification.deviceId,receiptSecret:notification.receiptSecret});
    }
    await badgeRefresh;
  })());
});
self.addEventListener('notificationclick', event => {
  event.notification.close();
  const id=event.notification.data?.id || event.notification.tag || '';
  const url=new URL('/',self.location.origin);
  if(id){url.searchParams.set('notification',id);url.hash='notification-'+encodeURIComponent(id)}else{url.hash='inboxPanel'}
  event.waitUntil((async()=>{
    // iOS may discard the launch URL and notifications sent before the page is ready.
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
        if(target){await target.focus();target.postMessage({type:'open-notification',id});return}
      }catch(_){/* An iOS launch client may not accept navigation yet. */}
      // Reuse the app even when WebKit cannot navigate its existing window.
      try{
        existing.postMessage({type:'open-notification',id});
        await existing.focus();
        return;
      }catch(_){/* Fall back to opening a window if the client disappeared. */}
    }
    const opened=await clients.openWindow(url.href);
    if(opened){opened.postMessage({type:'open-notification',id});await opened.focus()}
  })());
});
