'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
const context=vm.createContext({t:x=>x});
vm.runInContext(source.slice(source.indexOf('async function subscribeWithCurrentKey'),source.indexOf("$('subscribe').addEventListener")),context);
const subscribe=context.subscribeWithCurrentKey;
const mismatch=()=>Object.assign(new Error('key mismatch'),{name:'InvalidStateError'});
test('successful subscriptions are returned without changing existing registrations',async()=>{
 let lookedUp=false;const result={endpoint:'new'};
 const pending=subscribe({subscribe:()=>Promise.resolve(result),getSubscription(){lookedUp=true}},new Uint8Array([1]));
 assert.equal(await pending,result);assert.equal(lookedUp,false);
});
test('changed VAPID key unsubscribes before retrying',async()=>{
 const events=[];const key=new Uint8Array([1,2]);const result={endpoint:'new'};
 const manager={subscribe:async options=>{events.push('subscribe');assert.equal(options.applicationServerKey,key);if(events.length===1)throw mismatch();return result},getSubscription:async()=>({options:{applicationServerKey:new Uint8Array([3,4]).buffer},unsubscribe:async()=>{events.push('unsubscribe');return true}})};
 assert.equal(await subscribe(manager,key),result);assert.deepEqual(events,['subscribe','unsubscribe','subscribe']);
});
test('permission errors and matching or unknown keys do not unsubscribe',async()=>{
 for(const oldKey of [undefined,new Uint8Array([1]).buffer]){
  let removed=false;const error=mismatch();
  await assert.rejects(subscribe({subscribe:async()=>{throw error},getSubscription:async()=>({options:{applicationServerKey:oldKey},unsubscribe:async()=>{removed=true}})},new Uint8Array([1])),err=>err===error);
  assert.equal(removed,false);
 }
 await assert.rejects(subscribe({subscribe:async()=>{throw Object.assign(new Error('denied'),{name:'NotAllowedError'})},getSubscription(){assert.fail('permission denial must not inspect subscriptions')}},new Uint8Array([1])),{name:'NotAllowedError'});
});
test('failed unsubscribe does not retry registration',async()=>{
 let attempts=0;
 await assert.rejects(subscribe({subscribe:async()=>{attempts++;throw mismatch()},getSubscription:async()=>({options:{applicationServerKey:new Uint8Array([2]).buffer},unsubscribe:async()=>false})},new Uint8Array([1])),/Alte Push/);
 assert.equal(attempts,1);
});
