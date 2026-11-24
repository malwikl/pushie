'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {webcrypto,createHmac}=require('node:crypto');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/device-activity.js'),'utf8');
function fixture(){
 const key=Buffer.alloc(16,7),sent=[],listeners={};let timer;
 const document={visibilityState:'visible',addEventListener(type,fn){listeners[type]=fn}};
 const context=vm.createContext({window:{},crypto:webcrypto,document,TextEncoder,Uint8Array,
  btoa:s=>Buffer.from(s,'binary').toString('base64'),
  localStorage:{getItem:()=> 'phone'},
  navigator:{serviceWorker:{getRegistration:async()=>({pushManager:{getSubscription:async()=>({getKey:()=>key})}})}},
  fetch:async(path,options)=>{sent.push({path,...options});return {status:200}},
  setInterval:fn=>{timer=fn}});
 vm.runInContext(source,context);
 return {context,sent,key,document,listeners,timer:()=>timer()};
}
test('activity proves possession of push secret and is bound to this session and device',async()=>{
 const f=fixture();await f.context.window.deviceActivity.report('session-challenge');
 assert.equal(f.sent.length,1);
 const payload=JSON.parse(f.sent[0].body);
 assert.equal(payload.deviceId,'phone');
 assert.equal(payload.proof,createHmac('sha256',f.key).update('pushie-device-activity-v1:session-challenge:phone').digest('base64url'));
 assert.deepEqual(Object.keys(payload).sort(),['deviceId','proof']);
 assert.equal(f.sent[0].credentials,'same-origin');
 await f.context.window.deviceActivity.report('session-challenge');assert.equal(f.sent.length,1);
 f.context.window.deviceActivity.clear();await f.timer();assert.equal(f.sent.length,1);
});
test('hidden pages, missing subscriptions and missing crypto never report activity',async()=>{
 const f=fixture();f.document.visibilityState='hidden';await f.context.window.deviceActivity.report('challenge');assert.equal(f.sent.length,0);
 f.document.visibilityState='visible';f.context.navigator.serviceWorker.getRegistration=async()=>undefined;
 await f.context.window.deviceActivity.report();assert.equal(f.sent.length,0);
 f.context.crypto=undefined;await f.context.window.deviceActivity.report();assert.equal(f.sent.length,0);
});
