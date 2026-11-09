'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/trace.js'),'utf8');
test('browser ID persists while page IDs distinguish tabs and reloads',()=>{
 const saved=new Map();let next=0;
 const open=()=>{const window={};vm.runInNewContext(source,{window,crypto:{randomUUID:()=>`random-${++next}`},localStorage:{getItem:k=>saved.get(k),setItem:(k,v)=>saved.set(k,v)}});return window.clientTrace};
 const first=open(),second=open();
 assert.equal(first.headers()['X-App-Version'],'0');
 assert.equal(first.headers()['X-Client-ID'],second.headers()['X-Client-ID']);
 assert.notEqual(first.headers()['X-Page-ID'],second.headers()['X-Page-ID']);
 saved.set('webpushDeviceId','registered-device');
 assert.equal(first.headers()['X-Push-Device-ID'],'registered-device');
 const url=new URL('https://push.example/api/me/sync');first.socketURL(url);
 assert.equal(url.searchParams.get('client_id'),first.headers()['X-Client-ID']);
});
test('the loaded client version is sent with HTTP and WebSocket requests',()=>{
 const window={pushieVersion:'20261108195430123456'};
 vm.runInNewContext(source,{window});
 assert.equal(window.clientTrace.headers()['X-App-Version'],window.pushieVersion);
 const url=new URL('https://push.example/api/me/sync');window.clientTrace.socketURL(url);
 assert.equal(url.searchParams.get('app_version'),window.pushieVersion);
});
test('tracing works without randomUUID and with blocked storage',()=>{
 let next=0;
 const window={};
 vm.runInNewContext(source,{window,crypto:{getRandomValues:bytes=>{bytes.fill(++next);return bytes}},localStorage:{getItem(){throw new Error('blocked')}}});
 const headers=window.clientTrace.headers();
 assert.match(headers['X-Client-ID'],/^[A-Za-z0-9_-]{1,80}$/);
 assert.match(headers['X-Page-ID'],/^[A-Za-z0-9_-]{1,80}$/);
 assert.notEqual(headers['X-Client-ID'],headers['X-Page-ID']);
});
test('tracing works without Web Crypto or local storage',()=>{
 const window={};
 vm.runInNewContext(source,{window});
 const headers=window.clientTrace.headers();
 assert.match(headers['X-Client-ID'],/^[A-Za-z0-9_-]{1,80}$/);
 assert.match(headers['X-Page-ID'],/^[A-Za-z0-9_-]{1,80}$/);
 assert.notEqual(headers['X-Client-ID'],headers['X-Page-ID']);
});
