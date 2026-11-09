'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../static/version.js'),'utf8');
function fixture({version='20261108190000000000',next='20261108200000000000',saved=new Map(),offline=false,href='https://push.example/?message=one#message-one',blockedStorage=false}={}){
 const listeners={},timers=[],navigations=[],requests=[];let now=100000;
 const location={href,replace:url=>navigations.push(url)};
 const document={hidden:false,querySelector:()=>({content:version}),addEventListener:(name,fn)=>{listeners[name]=fn}};
 const window={addEventListener:(name,fn)=>{listeners[name]=fn}};
 vm.runInNewContext(source,{window,document,location,URL,Date:{now:()=>now},history:{replaceState:(_,__,url)=>{location.href=url}},
  sessionStorage:{getItem:key=>{if(blockedStorage)throw Error('blocked');return saved.get(key)},setItem:(key,value)=>saved.set(key,value)},
  setInterval:(fn,ms)=>timers.push({fn,ms}),fetch:async(url,options)=>{requests.push({url,options});if(offline)throw Error('offline');return {ok:true,json:async()=>({version:next})}}});
 return {window,document,listeners,timers,navigations,requests,saved,location,advance(){now+=60000},async flush(){await new Promise(resolve=>setImmediate(resolve))}};
}
test('newer build forces fresh navigation and preserves message links',async()=>{
 const f=fixture();await f.flush();assert.equal(f.navigations.length,1);
 const target=new URL(f.navigations[0]);assert.equal(target.searchParams.get('_build'),'20261108200000000000');
 assert.equal(target.searchParams.get('message'),'one');assert.equal(target.hash,'#message-one');
 assert.equal(f.requests[0].options.cache,'no-store');assert.equal(f.window.pushieVersion,'20261108190000000000');
});
test('same and older builds, invalid replies and offline failures do not reload',async()=>{
 for(const options of [{next:'20261108190000000000'},{next:'20261108180000000000'},{next:'invalid'},{offline:true}]){
  const f=fixture(options);await f.flush();assert.equal(f.navigations.length,0);
 }
});
test('stale HTML cannot repeatedly reload to the same target version',async()=>{
 const f=fixture();await f.flush();const reopened=fixture({saved:f.saved});await reopened.flush();
 assert.equal(reopened.navigations.length,0);
});
test('URL guard prevents reload loops even when session storage is unavailable',async()=>{
 const f=fixture({blockedStorage:true});await f.flush();assert.equal(f.navigations.length,1);
 const reopened=fixture({href:f.navigations[0],blockedStorage:true});await reopened.flush();
 assert.equal(reopened.navigations.length,0);
});
test('version checks pause when hidden or signed out and resume on return',async()=>{
 const f=fixture({next:'20261108190000000000'});await f.flush();assert.equal(f.timers[0].ms,60000);
 f.advance();f.document.hidden=true;await f.timers[0].fn();assert.equal(f.requests.length,1);
 f.document.hidden=false;f.window.authRefreshStopped=true;await f.listeners.focus();assert.equal(f.requests.length,1);
 f.window.authRefreshStopped=false;await f.listeners.pageshow();assert.equal(f.requests.length,2);
});
test('worker activation upgrades legacy windows but leaves current and newer windows alone',async()=>{
 const handlers={},urls=[];
 const clients={claim:async()=>{},matchAll:async()=>['https://push.example/?message=one#message-one','https://push.example/?_build=20261108200000000000','https://push.example/?_build=20261108300000000000'].map(url=>({url,navigate:async target=>urls.push(target)}))};
 const worker=fs.readFileSync(path.join(__dirname,'../static/sw.js'),'utf8').replaceAll('__PUSHIE_VERSION__','20261108200000000000');
 vm.runInNewContext(worker,{clients,URL,self:{location:{origin:'https://push.example'},addEventListener:(name,fn)=>{handlers[name]=fn}}});
 let task;handlers.activate({waitUntil:pending=>{task=pending}});await task;
 assert.equal(urls.length,1);assert.equal(new URL(urls[0]).searchParams.get('message'),'one');
 assert.equal(new URL(urls[0]).hash,'#message-one');
});
