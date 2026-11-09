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
test('existing app is navigated, focused and receives the message target',async()=>{
 const events=[];const existing={url:'https://push.example.com/?lang=en',async navigate(url){events.push(['navigate',url]);return this},async focus(){events.push(['focus'])},postMessage(message){events.push(['message',message.id])}};
 await click({matchAll:async()=>[existing],openWindow(){assert.fail('must reuse existing app')}},{id:'a/b'});
 assert.match(events[0][1],/message=a%2Fb/);assert.match(events[0][1],/#message-a%2Fb$/);assert.equal(events[1][0],'focus');assert.deepEqual(events[2],['message','a/b']);
});
test('cold launch opens durable query link and falls back to notification tag',async()=>{
 let target;
 await click({matchAll:async()=>[],openWindow:async url=>{target=url;return null}},{},'tag-id');
 assert.equal(new URL(target).searchParams.get('message'),'tag-id');
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
  assert.equal(saved.id,'ios-message');return null;
 }},{id:'ios-message'},'',cache);
 assert.equal(saved.key,'/__notification_target');assert.ok(saved.createdAt);
});

test('displaying a push preserves native navigation and browser acknowledgement',async()=>{
 const handlers={};let pending,shown,ack;
 vm.runInNewContext(source,{URL,fetch:async(url,options)=>{ack={url,body:JSON.parse(options.body)}},self:{
  location:{origin:'https://push.example.com'},
  registration:{showNotification:async(title,options)=>{shown={title,...options}}},
  addEventListener:(name,fn)=>{handlers[name]=fn}
 }});
 const navigate='https://push.example.com/?message=one';
 handlers.push({data:{json:()=>({id:'one',deviceId:'device',receiptSecret:'receipt',notification:{title:'Native',body:'Body',navigate}})},waitUntil(task){pending=task}});
 await pending;
 assert.equal(shown.navigate,navigate);assert.equal(shown.title,'Native');assert.equal(shown.body,'Body');
 assert.equal(shown.data.id,'one');assert.equal(ack.body.messageId,'one');
});

test('iOS window that refuses navigation still receives the target and is focused',async()=>{
 const events=[];
 await click({matchAll:async()=>[{
  url:'https://push.example.com/',navigate:async()=>{throw new Error('inert launch client')},
  postMessage:message=>events.push(['message',message.id]),focus:async()=>events.push(['focus'])
 }],openWindow:async()=>assert.fail('reuse the installed app')},{id:'ios-message'});
 assert.deepEqual(events,[['message','ios-message'],['focus']]);
});
test('null navigation result can still reuse a surviving app window',async()=>{
 let focused=false,message;
 await click({matchAll:async()=>[{
  url:'https://push.example.com/',navigate:async()=>null,
  postMessage:data=>{message=data},focus:async()=>{focused=true}
 }],openWindow:async()=>assert.fail('reuse the surviving client')});
 assert.equal(focused,true);assert.equal(message.id,'one');
});
test('cache errors and disappearing clients do not prevent opening the notification URL',async()=>{
 let target;
 await click({matchAll:async()=>[{
  url:'https://push.example.com/',navigate:async()=>{throw Error('gone')},
  postMessage(){throw Error('gone')}
 }],openWindow:async url=>{target=url;return null}}, {id:'one'},'',{put:async()=>{throw Error('storage blocked')}});
 assert.equal(new URL(target).searchParams.get('message'),'one');
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
