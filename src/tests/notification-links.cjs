'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
function fixture(hash,notifications,me={id:'user'}){
 const calls=[];
 const context=vm.createContext({URLSearchParams,me,location:{hash,search:''},inboxNotifications:notifications,api:async(...args)=>{calls.push(args)}});
 vm.runInContext(source.slice(source.indexOf('function linkedNotificationId'),source.indexOf('async function refreshInbox')),context);
 return {context,calls,open:()=>context.readLinkedNotification()};
}
test('push links mark only the linked notification read, including encoded IDs',async()=>{
 const notifications=[{id:'a/b',readAt:null},{id:'other',readAt:null}];
 const f=fixture('#notification-a%2Fb',notifications);
 assert.equal(await f.open(),'a/b');assert.equal(f.calls.length,1);
 assert.equal(f.calls[0][0],'/api/me/notifications/a%2Fb/read');assert.equal(f.calls[0][1],'PUT');
 assert.ok(notifications[0].readAt);assert.equal(notifications[1].readAt,null);
 await f.open();assert.equal(f.calls.length,1);
});
test('ordinary inbox navigation, missing notifications and signed-out users do not mark read',async()=>{
 for(const [hash,notifications,me] of [['#inboxPanel',[{id:'one'}],{}],['#notification-missing',[{id:'one'}],{}],['#notification-%',[{id:'one'}],{}],['#notification-one',[{id:'one'}],null]]){
  const f=fixture(hash,notifications,me);assert.equal(await f.open(),null);assert.equal(f.calls.length,0);
 }
});
test('failed read requests leave the notification unread and the link available to retry',async()=>{
 const notification={id:'one',readAt:null};const f=fixture('#notification-one',[notification]);
 f.context.api=async()=>{throw new Error('offline')};
 await assert.rejects(f.open(),/offline/);assert.equal(notification.readAt,null);assert.equal(f.context.location.hash,'#notification-one');
});
test('manual-read preference opens notification links without marking them read',async()=>{
 const notification={id:'one',readAt:null},f=fixture('#notification-one',[notification],{id:'user',manualRead:true});
 assert.equal(await f.open(),'one');assert.equal(notification.readAt,null);assert.equal(f.calls.length,0);
});

test('cold app launch uses query target when the fragment is absent',async()=>{
 const notification={id:'one',readAt:null};const f=fixture('',[notification]);
 f.context.location.search='?lang=en&notification=one';
 assert.equal(await f.open(),'one');assert.ok(notification.readAt);assert.equal(f.calls.length,1);
});

function recoveryFixture(target){
 const opened=[];let stored=target;
 const context=vm.createContext({Date,linkedNotificationId:()=>null,caches:{open:async()=>({
  match:async()=>stored?{json:async()=>stored}:undefined,
  delete:async()=>{stored=null}
 })}});
 vm.runInContext(source.slice(source.indexOf('let recoveringNotification='),source.indexOf("if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message'")),context);
 context.openNotification=id=>opened.push(id);
 return {opened,recover:()=>context.recoverNotification()};
}
test('foreground recovery restores a click when iOS loses the URL and live notification',async()=>{
 const f=recoveryFixture({id:'ios-notification',createdAt:Date.now()});
 await Promise.all([f.recover(),f.recover()]);
 assert.deepEqual(f.opened,['ios-notification']);
 await f.recover();assert.equal(f.opened.length,1);
});
test('foreground recovery ignores abandoned notification clicks',async()=>{
 const f=recoveryFixture({id:'old',createdAt:Date.now()-6*60*1000});
 await f.recover();assert.deepEqual(f.opened,[]);
});

function launchFixture(notifications,hash=''){
 const calls=[],scrolled=[],tabs=[],url=new URL('https://push.example/'+hash);
 const context=vm.createContext({URL,URLSearchParams,Date,identityEpoch:0,$:()=>null,receivedNotificationList:{filter:'topic:backup'},closeHeaderMenu(){},me:{id:'user'},reg:null,inboxNotifications:[],openInboxNotifications:new Set(),
  location:url,history:{replaceState(_,__,next){url.href=new URL(next,url).href}},
  api:async(path)=>{calls.push(path);return {notifications}},refreshSentNotifications:async()=>{},setNotificationTab(tab){tabs.push(tab)},renderInbox(){},resetViews(){},
  document:{getElementById:id=>({querySelector:()=>null,scrollIntoView:()=>scrolled.push(id)})}});
 vm.runInContext(source.slice(source.indexOf('function linkedNotificationId'),source.indexOf('async function refreshAdminTargets')),context);
 return {context,calls,scrolled,tabs,refresh:options=>context.refreshInbox(options)};
}
test('opening without a notification URL leaves every notification unread',async()=>{
 const notifications=[{id:'older',createdAt:'2026-01-01'},{id:'newest',createdAt:'2026-02-01'},{id:'read',createdAt:'2026-03-01',readAt:'yes'}];
 const f=launchFixture(notifications);await f.refresh({openUnread:true});
 assert.deepEqual(f.scrolled,[]);assert.deepEqual(f.tabs,[]);assert.equal(notifications[1].readAt,undefined);assert.equal(notifications[0].readAt,undefined);assert.deepEqual(f.calls,['/api/me/notifications']);
});
test('a worker push refreshes both unread badges without marking notifications read',async()=>{
 const notifications=[{id:'unread'},{id:'read',readAt:'yes'}];
 const f=launchFixture(notifications),badges={unreadBadge:{},headerUnreadBadge:{}};
 let onWorkerMessage;const iconCounts=[];
 Object.assign(f.context,{$:id=>badges[id],getNotificationList:()=>({update(){}}),navigator:{
  setAppBadge:async count=>iconCounts.push(count),serviceWorker:{addEventListener(_,handler){onWorkerMessage=handler}}
 }});
 vm.runInContext(source.slice(source.indexOf('function updateUnreadBadges('),source.indexOf('function linkedNotificationId')),f.context);
 const start=source.indexOf("if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message'");
 vm.runInContext(source.slice(start,source.indexOf('\n(async()=>{',start)),f.context);
 await Promise.all(Array.from({length:3},()=>onWorkerMessage({data:{type:'inbox-changed'}})));
 assert.deepEqual(f.calls,['/api/me/notifications']);assert.equal(notifications[0].readAt,undefined);
 for(const badge of Object.values(badges)){assert.equal(badge.textContent,'1');assert.equal(badge.hidden,false)}
 notifications[0].readAt='yes';await f.refresh();
 for(const badge of Object.values(badges)){assert.equal(badge.textContent,'');assert.equal(badge.hidden,true)}
 assert.deepEqual(iconCounts,[1,0]);
});
test('a background sync loads the current unread count without loading the inbox',async()=>{
 const f=launchFixture([{id:'unread'}]),counts=[];
 let unread=2;
 f.context.api=async path=>{f.calls.push(path);return {unread}};
 f.context.updateUnreadBadges=count=>counts.push(count);
 await f.refresh({badgeOnly:true});unread=0;await f.refresh({badgeOnly:true});
 assert.deepEqual(f.calls,Array(2).fill('/api/me/notifications/unread'));
 assert.deepEqual(counts,[2,0]);assert.equal(f.context.inboxNotifications.length,0);
});
test('notification target wins over newer unread notifications on opening',async()=>{
 const f=launchFixture([{id:'selected',createdAt:'2026-01-01'},{id:'newest',createdAt:'2026-02-01'}],'#notification-selected');
 await f.refresh({openUnread:true});assert.deepEqual(f.scrolled,['notification-selected']);
 assert.equal(f.context.receivedNotificationList.filter,'');assert.equal(f.context.openInboxNotifications.has('selected'),true);
});
test('ordinary polling and opening with no unread notifications do not navigate',async()=>{
 const f=launchFixture([{id:'unread'}]);await f.refresh();assert.deepEqual(f.scrolled,[]);
 const read=launchFixture([{id:'read',readAt:'yes'}]);await read.refresh({openUnread:true});assert.deepEqual(read.scrolled,[]);
});

test('simultaneous foreground checks leave all unread notifications untouched',async()=>{
 const notifications=[{id:'selected',createdAt:'2026-02-01'},{id:'other',createdAt:'2026-01-01'}];
 const f=launchFixture(notifications);
 await Promise.all([f.refresh({openUnread:true}),f.refresh({openUnread:true})]);
 assert.deepEqual(f.scrolled,[]);assert.equal(notifications[0].readAt,undefined);assert.equal(notifications[1].readAt,undefined);
});
test('newer tap during a read request is the final navigation target',async()=>{
 const notifications=[{id:'first'},{id:'second'}];const f=launchFixture(notifications,'#notification-first');
 let release,started;const reading=new Promise(resolve=>{started=resolve});
 f.context.api=async path=>{
  if(path==='/api/me/notifications')return {notifications};
  if(path.endsWith('/first/read')){started();await new Promise(resolve=>{release=resolve})}
  return {};
 };
 const first=f.refresh();await reading;
 f.context.location.hash='notification-second';const second=f.refresh();release();
 await Promise.all([first,second]);assert.deepEqual(f.scrolled,['notification-second']);
});
test('queued inbox work from an old login does not fetch notifications for another account',async()=>{
 const f=launchFixture([{id:'one'}]);const queued=f.refresh({openUnread:true});
 f.context.identityEpoch++;await queued;assert.deepEqual(f.calls,[]);assert.deepEqual(f.scrolled,[]);
});
test('iOS foreground recovery opens the tapped notification once without opening the next unread',async()=>{
 const notifications=[{id:'newest',createdAt:'2026-02-01'},{id:'tapped',createdAt:'2026-01-01'}];
 const f=launchFixture(notifications);let stored={id:'tapped',createdAt:Date.now()};
 f.context.caches={open:async()=>({match:async()=>stored?{json:async()=>stored}:undefined,delete:async()=>{stored=null}})};
 f.context.status=notification=>assert.fail(notification);
 f.context.retryPushReceipts=async()=>{};
 vm.runInContext(source.slice(source.indexOf('function openNotification'),source.indexOf("if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message'")),f.context);
 vm.runInContext(source.slice(source.indexOf('let foregroundCheck='),source.indexOf("document.addEventListener('visibilitychange'")),f.context);
 await Promise.all([f.context.checkNotificationsOnOpen(),f.context.checkNotificationsOnOpen()]);
 assert.deepEqual(f.scrolled,['notification-tapped']);assert.equal(notifications[0].readAt,undefined);
 // A late postMessage for the same tap must not reopen it or move to another notification.
 f.context.openNotification('tapped');await f.refresh({openUnread:true});
 assert.deepEqual(f.scrolled,['notification-tapped']);
});
test('native launch URL takes priority over an earlier cached click',async()=>{
 const f=launchFixture([{id:'native'},{id:'cached'}],'#notification-native');
 f.context.caches={open:async()=>({match:async()=>({json:async()=>({id:'cached',createdAt:Date.now()})}),delete:async()=>{}})};
 vm.runInContext(source.slice(source.indexOf('function openNotification'),source.indexOf("if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message'")),f.context);
 assert.equal(await f.context.recoverNotification(),'native');await f.refresh({openUnread:true});
 assert.deepEqual(f.scrolled,['notification-native']);
});
