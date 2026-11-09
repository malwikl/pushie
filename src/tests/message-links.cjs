'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
function fixture(hash,messages,me={id:'user'}){
 const calls=[];
 const context=vm.createContext({URLSearchParams,me,location:{hash,search:''},inboxMessages:messages,api:async(...args)=>{calls.push(args)}});
 vm.runInContext(source.slice(source.indexOf('function linkedMessageId'),source.indexOf('async function refreshInbox')),context);
 return {context,calls,open:()=>context.readLinkedMessage()};
}
test('push links mark only the linked message read, including encoded IDs',async()=>{
 const messages=[{id:'a/b',readAt:null},{id:'other',readAt:null}];
 const f=fixture('#message-a%2Fb',messages);
 assert.equal(await f.open(),'a/b');assert.equal(f.calls.length,1);
 assert.equal(f.calls[0][0],'/api/me/messages/a%2Fb/read');assert.equal(f.calls[0][1],'PUT');
 assert.ok(messages[0].readAt);assert.equal(messages[1].readAt,null);
 await f.open();assert.equal(f.calls.length,1);
});
test('ordinary inbox navigation, missing messages and signed-out users do not mark read',async()=>{
 for(const [hash,messages,me] of [['#inboxPanel',[{id:'one'}],{}],['#message-missing',[{id:'one'}],{}],['#message-%',[{id:'one'}],{}],['#message-one',[{id:'one'}],null]]){
  const f=fixture(hash,messages,me);assert.equal(await f.open(),null);assert.equal(f.calls.length,0);
 }
});
test('failed read requests leave the message unread and the link available to retry',async()=>{
 const message={id:'one',readAt:null};const f=fixture('#message-one',[message]);
 f.context.api=async()=>{throw new Error('offline')};
 await assert.rejects(f.open(),/offline/);assert.equal(message.readAt,null);assert.equal(f.context.location.hash,'#message-one');
});

test('cold app launch uses query target when the fragment is absent',async()=>{
 const message={id:'one',readAt:null};const f=fixture('',[message]);
 f.context.location.search='?lang=en&message=one';
 assert.equal(await f.open(),'one');assert.ok(message.readAt);assert.equal(f.calls.length,1);
});

function recoveryFixture(target){
 const opened=[];let stored=target;
 const context=vm.createContext({Date,linkedMessageId:()=>null,caches:{open:async()=>({
  match:async()=>stored?{json:async()=>stored}:undefined,
  delete:async()=>{stored=null}
 })}});
 vm.runInContext(source.slice(source.indexOf('let recoveringNotification='),source.indexOf("if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message'")),context);
 context.openNotificationMessage=id=>opened.push(id);
 return {opened,recover:()=>context.recoverNotificationMessage()};
}
test('foreground recovery restores a click when iOS loses the URL and live message',async()=>{
 const f=recoveryFixture({id:'ios-message',createdAt:Date.now()});
 await Promise.all([f.recover(),f.recover()]);
 assert.deepEqual(f.opened,['ios-message']);
 await f.recover();assert.equal(f.opened.length,1);
});
test('foreground recovery ignores abandoned notification clicks',async()=>{
 const f=recoveryFixture({id:'old',createdAt:Date.now()-6*60*1000});
 await f.recover();assert.deepEqual(f.opened,[]);
});

function launchFixture(messages,hash=''){
 const calls=[],scrolled=[],tabs=[],url=new URL('https://push.example/'+hash);
 const context=vm.createContext({URL,URLSearchParams,Date,identityEpoch:0,$:()=>null,closeHeaderMenu(){},me:{id:'user'},reg:null,inboxMessages:[],
  location:url,history:{replaceState(_,__,next){url.href=new URL(next,url).href}},
  api:async(path)=>{calls.push(path);return {messages}},refreshSentMessages:async()=>{},setMessageTab(tab){tabs.push(tab)},renderInbox(){},resetViews(){},
  document:{getElementById:id=>({querySelector:()=>null,scrollIntoView:()=>scrolled.push(id)})}});
 vm.runInContext(source.slice(source.indexOf('function linkedMessageId'),source.indexOf('async function refreshAdminTargets')),context);
 return {context,calls,scrolled,tabs,refresh:options=>context.refreshInbox(options)};
}
test('opening without a notification URL leaves every message unread',async()=>{
 const messages=[{id:'older',createdAt:'2026-01-01'},{id:'newest',createdAt:'2026-02-01'},{id:'read',createdAt:'2026-03-01',readAt:'yes'}];
 const f=launchFixture(messages);await f.refresh({openUnread:true});
 assert.deepEqual(f.scrolled,[]);assert.deepEqual(f.tabs,[]);assert.equal(messages[1].readAt,undefined);assert.equal(messages[0].readAt,undefined);assert.deepEqual(f.calls,['/api/me/messages']);
});
test('notification target wins over newer unread messages on opening',async()=>{
 const f=launchFixture([{id:'selected',createdAt:'2026-01-01'},{id:'newest',createdAt:'2026-02-01'}],'#message-selected');
 await f.refresh({openUnread:true});assert.deepEqual(f.scrolled,['message-selected']);
});
test('ordinary polling and opening with no unread messages do not navigate',async()=>{
 const f=launchFixture([{id:'unread'}]);await f.refresh();assert.deepEqual(f.scrolled,[]);
 const read=launchFixture([{id:'read',readAt:'yes'}]);await read.refresh({openUnread:true});assert.deepEqual(read.scrolled,[]);
});

test('simultaneous foreground checks leave all unread messages untouched',async()=>{
 const messages=[{id:'selected',createdAt:'2026-02-01'},{id:'other',createdAt:'2026-01-01'}];
 const f=launchFixture(messages);
 await Promise.all([f.refresh({openUnread:true}),f.refresh({openUnread:true})]);
 assert.deepEqual(f.scrolled,[]);assert.equal(messages[0].readAt,undefined);assert.equal(messages[1].readAt,undefined);
});
test('newer tap during a read request is the final navigation target',async()=>{
 const messages=[{id:'first'},{id:'second'}];const f=launchFixture(messages,'#message-first');
 let release,started;const reading=new Promise(resolve=>{started=resolve});
 f.context.api=async path=>{
  if(path==='/api/me/messages')return {messages};
  if(path.endsWith('/first/read')){started();await new Promise(resolve=>{release=resolve})}
  return {};
 };
 const first=f.refresh();await reading;
 f.context.location.hash='message-second';const second=f.refresh();release();
 await Promise.all([first,second]);assert.deepEqual(f.scrolled,['message-second']);
});
test('queued inbox work from an old login does not fetch messages for another account',async()=>{
 const f=launchFixture([{id:'one'}]);const queued=f.refresh({openUnread:true});
 f.context.identityEpoch++;await queued;assert.deepEqual(f.calls,[]);assert.deepEqual(f.scrolled,[]);
});
test('iOS foreground recovery opens the tapped message once without opening the next unread',async()=>{
 const messages=[{id:'newest',createdAt:'2026-02-01'},{id:'tapped',createdAt:'2026-01-01'}];
 const f=launchFixture(messages);let stored={id:'tapped',createdAt:Date.now()};
 f.context.caches={open:async()=>({match:async()=>stored?{json:async()=>stored}:undefined,delete:async()=>{stored=null}})};
 f.context.status=message=>assert.fail(message);
 f.context.retryPushReceipts=async()=>{};
 vm.runInContext(source.slice(source.indexOf('function openNotificationMessage'),source.indexOf("if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message'")),f.context);
 vm.runInContext(source.slice(source.indexOf('let foregroundCheck='),source.indexOf("document.addEventListener('visibilitychange'")),f.context);
 await Promise.all([f.context.checkMessagesOnOpen(),f.context.checkMessagesOnOpen()]);
 assert.deepEqual(f.scrolled,['message-tapped']);assert.equal(messages[0].readAt,undefined);
 // A late postMessage for the same tap must not reopen it or move to another message.
 f.context.openNotificationMessage('tapped');await f.refresh({openUnread:true});
 assert.deepEqual(f.scrolled,['message-tapped']);
});
test('native launch URL takes priority over an earlier cached click',async()=>{
 const f=launchFixture([{id:'native'},{id:'cached'}],'#message-native');
 f.context.caches={open:async()=>({match:async()=>({json:async()=>({id:'cached',createdAt:Date.now()})}),delete:async()=>{}})};
 vm.runInContext(source.slice(source.indexOf('function openNotificationMessage'),source.indexOf("if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message'")),f.context);
 assert.equal(await f.context.recoverNotificationMessage(),'native');await f.refresh({openUnread:true});
 assert.deepEqual(f.scrolled,['message-native']);
});
