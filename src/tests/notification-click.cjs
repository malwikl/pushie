'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/sw.js'),'utf8');
async function click(clients,data={id:'one'},tag='',cache){
 const handlers={};let pending,closed=false;
 vm.runInNewContext(source,{URL,Response,caches:{open:async()=>cache},clients,self:{location:{origin:'https://push.example.com'},addEventListener:(name,fn)=>{handlers[name]=fn}}});
 handlers.notificationclick({notification:{data,tag,close(){closed=true}},waitUntil(task){pending=task}});
 await pending;assert.equal(closed,true);
}
test('existing app is navigated, focused and receives the notification target',async()=>{
 const events=[];const existing={url:'https://push.example.com/?lang=en',async navigate(url){events.push(['navigate',url]);return this},async focus(){events.push(['focus'])},postMessage(notification){events.push(['message',notification.id])}};
 await click({matchAll:async()=>[existing],openWindow(){assert.fail('must reuse existing app')}},{id:'a/b'});
 assert.match(events[0][1],/notification=a%2Fb/);assert.match(events[0][1],/#notification-a%2Fb$/);assert.equal(events[1][0],'focus');assert.deepEqual(events[2],['message','a/b']);
});
test('cold launch opens durable query link and falls back to notification tag',async()=>{
 let target;
 await click({matchAll:async()=>[],openWindow:async url=>{target=url;return null}},{},'tag-id');
 assert.equal(new URL(target).searchParams.get('notification'),'tag-id');
});
test('failed existing-window navigation falls back to a new window',async()=>{
 let opened=false;
 await click({matchAll:async()=>[{url:'https://push.example.com/',navigate:async()=>{throw new Error('unavailable')}}],openWindow:async()=>{opened=true;return null}});
 assert.equal(opened,true);
});

test('click target is persisted before iOS opens the app without returning a client',async()=>{
 let saved;
 const cache={async put(key,response){saved={key,...await response.json()}}};
 await click({matchAll:async()=>[],openWindow:async()=>{
  assert.equal(saved.id,'ios-notification');return null;
 }},{id:'ios-notification'},'',cache);
 assert.equal(saved.key,'/__notification_target');assert.ok(saved.createdAt);
});

test('displaying a push preserves native navigation and browser acknowledgement',async()=>{
 const handlers={};let pending,shown,ack;
 vm.runInNewContext(source,{URL,fetch:async(url,options)=>{ack={url,body:JSON.parse(options.body)}},self:{
  location:{origin:'https://push.example.com'},
  registration:{showNotification:async(title,options)=>{shown={title,...options}}},
  addEventListener:(name,fn)=>{handlers[name]=fn}
 }});
 const navigate='https://push.example.com/?notification=one';
 handlers.push({data:{json:()=>({id:'one',deviceId:'device',receiptSecret:'receipt',notification:{title:'Native',body:'Body',navigate}})},waitUntil(task){pending=task}});
 await pending;
 assert.equal(shown.navigate,navigate);assert.equal(shown.title,'Native');assert.equal(shown.body,'Body');
 assert.equal(shown.data.id,'one');assert.equal(ack.body.notificationId,'one');
});

test('push refreshes app windows and sets the current icon count in the background',async()=>{
 const handlers={},messages=[],counts=[],requests=[];let pending,shown=0,unread=3;
 const app={url:'https://push.example.com/?lang=en',visibilityState:'hidden',postMessage:data=>messages.push(data.type)};
 const other={url:'https://push.example.com/icon.svg',postMessage:()=>messages.push('wrong window')};
 let windows=[app,other];
 vm.runInNewContext(source,{
  URL,clients:{matchAll:async()=>windows},
  fetch:async(url,options)=>{requests.push([url,options.credentials,options.cache]);return {ok:true,json:async()=>({unread})}},
  self:{location:{origin:'https://push.example.com'},navigator:{setAppBadge:async count=>counts.push(count)},
   registration:{showNotification:async()=>{shown++}},addEventListener:(name,fn)=>{handlers[name]=fn}}
 });
 async function push(){handlers.push({data:{json:()=>({id:'one'})},waitUntil(task){pending=task}});await pending}
 await push();await push();
 app.visibilityState='visible';await push();
 assert.deepEqual(counts,[3,3]);assert.equal(requests.length,2);
 windows=[];unread=0;await push();
 assert.deepEqual(counts,[3,3,0]);
 assert.deepEqual(requests,Array(3).fill(['/api/me/notifications/unread','same-origin','no-store']));
 assert.equal(shown,4);assert.deepEqual(messages,Array(3).fill('inbox-changed'));
});

test('a delayed background response yields to a newer push or a visible app window',async()=>{
 const handlers={},counts=[],responses=[];let windows=[];
 vm.runInNewContext(source,{
  URL,clients:{matchAll:async()=>windows},fetch:()=>new Promise(resolve=>responses.push(resolve)),
  self:{location:{origin:'https://push.example.com'},navigator:{setAppBadge:async count=>counts.push(count)},
   registration:{showNotification:async()=>{}},addEventListener:(name,fn)=>{handlers[name]=fn}}
 });
 function push(){let pending;handlers.push({waitUntil(task){pending=task}});return pending}
 const flush=()=>new Promise(resolve=>setImmediate(resolve));
 const response=unread=>({ok:true,json:async()=>({unread})});
 const first=push(),second=push();await flush();
 responses[1](response(2));await second;
 responses[0](response(1));await first;
 const third=push();await flush();
 windows=[{url:'https://push.example.com/',visibilityState:'visible'}];
 responses[2](response(3));await third;
 assert.deepEqual(counts,[2]);
});

test('iOS window that refuses navigation still receives the target and is focused',async()=>{
 const events=[];
 await click({matchAll:async()=>[{
  url:'https://push.example.com/',navigate:async()=>{throw new Error('inert launch client')},
  postMessage:notification=>events.push(['message',notification.id]),focus:async()=>events.push(['focus'])
 }],openWindow:async()=>assert.fail('reuse the installed app')},{id:'ios-notification'});
 assert.deepEqual(events,[['message','ios-notification'],['focus']]);
});
test('null navigation result can still reuse a surviving app window',async()=>{
 let focused=false,notification;
 await click({matchAll:async()=>[{
  url:'https://push.example.com/',navigate:async()=>null,
  postMessage:data=>{notification=data},focus:async()=>{focused=true}
 }],openWindow:async()=>assert.fail('reuse the surviving client')});
 assert.equal(focused,true);assert.equal(notification.id,'one');
});
test('cache errors and disappearing clients do not prevent opening the notification URL',async()=>{
 let target;
 await click({matchAll:async()=>[{
  url:'https://push.example.com/',navigate:async()=>{throw Error('gone')},
  postMessage(){throw Error('gone')}
 }],openWindow:async url=>{target=url;return null}}, {id:'one'},'',{put:async()=>{throw Error('storage blocked')}});
 assert.equal(new URL(target).searchParams.get('notification'),'one');
});

test('failed receipt is persisted and rechecked without displaying another notification',async()=>{
 const handlers={},stored=new Map();let pending,requests=0,shown=0,done=false;
 const cache={put:async(key,value)=>stored.set(key,value),delete:async key=>stored.delete(key),keys:async()=>[...stored.keys()],match:async key=>stored.get(key).clone()};
 vm.runInNewContext(source,{URL,Response,caches:{open:async()=>cache},fetch:async()=>({ok:++requests>1,status:503}),self:{
  location:{origin:'https://push.example.com'},registration:{showNotification:async()=>{shown++}},
  addEventListener:(name,fn)=>{handlers[name]=fn}
 }});
 handlers.push({data:{json:()=>({id:'one',deviceId:'device',receiptSecret:'receipt'})},waitUntil(task){pending=task}});
 await pending;assert.equal(stored.size,1);assert.equal(shown,1);
 handlers.message({data:{type:'retry-push-receipts'},ports:[{postMessage(){done=true}}],waitUntil(task){pending=task}});
 await pending;assert.equal(stored.size,0);assert.equal(requests,2);assert.equal(shown,1);assert.equal(done,true);
});
