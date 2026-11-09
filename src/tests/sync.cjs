'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/sync.js'),'utf8');
function fixture(){
 const sockets=[],timers=new Map(),listeners={};let next=0,refreshes=0;
 class WebSocket{
  constructor(url){this.url=url;sockets.push(this)}
  close(){this.closed=true;this.onclose?.()}
 }
 const window={WebSocket,addEventListener:(name,fn)=>{listeners[name]=fn}};
 const document={getElementById:()=>null,hidden:false,addEventListener:(name,fn)=>{listeners[name]=fn}};
 vm.runInNewContext(source,{window,document,WebSocket,URL,location:{href:'https://push.example/'},Math,JSON,Date,
  refresh:async()=>{refreshes++},setTimeout:(fn,ms)=>{timers.set(++next,{fn,ms});return next},clearTimeout:id=>timers.delete(id)});
 async function flush(){const pending=[...timers.values()];timers.clear();for(const {fn} of pending)await fn();await Promise.resolve()}
 return {window,document,sockets,timers,listeners,flush,get refreshes(){return refreshes}};
}
test('live changes coalesce and refresh through HTTPS without navigation',async()=>{
 const f=fixture();f.window.updateLiveSync('one');assert.equal(f.sockets[0].url,'wss://push.example/api/me/sync');
 const socket=f.sockets[0];socket.onopen();socket.onmessage({data:'{"type":"sync"}'});await f.flush();assert.equal(f.refreshes,1);
 socket.onmessage({data:'{"type":"sync"}'});socket.onmessage({data:'{"type":"sync"}'});
 assert.equal(f.timers.size,1);await f.flush();assert.equal(f.refreshes,2);
});
test('reconnect catches up and logout cancels reconnects and old messages',async()=>{
 const f=fixture();f.window.updateLiveSync('one');const old=f.sockets[0];old.close();
 assert.equal(f.timers.size,1);await f.flush();assert.equal(f.sockets.length,2);
 f.sockets[1].onopen();f.sockets[1].onmessage({data:'{"type":"sync"}'});await f.flush();assert.equal(f.refreshes,1);
 f.window.updateLiveSync(null);old.onmessage({data:'{"type":"sync"}'});
 assert.equal(f.timers.size,0);assert.equal(f.sockets[1].closed,true);
});
test('background suspends socket and foreground resumes with fresh state',async()=>{
 const f=fixture();f.window.updateLiveSync('one');f.document.hidden=true;f.listeners.visibilitychange();
 assert.equal(f.sockets[0].closed,true);assert.equal(f.timers.size,0);
 f.document.hidden=false;f.listeners.visibilitychange();assert.equal(f.sockets.length,2);
 f.sockets[1].onmessage({data:'{"type":"sync"}'});
 await f.flush();assert.equal(f.refreshes,1);
});

test('fallback polling runs only without an open WebSocket',async()=>{
 const app=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
 let poll,calls=0,connected=false;
 const document={hidden:false};
 const context=vm.createContext({document,me:{id:'one'},window:{shouldPollInbox:()=>!connected},
  refreshInbox:async()=>{calls++},setInterval:fn=>{poll=fn}});
 const start=app.indexOf('setInterval(()=>{if(!document.hidden&&me');
 vm.runInContext(app.slice(start,app.indexOf('\n',start)),context);
 connected=true;poll();assert.equal(calls,0);
 connected=false;poll();assert.equal(calls,1);
 document.hidden=true;poll();assert.equal(calls,1);
 document.hidden=false;context.me=null;poll();assert.equal(calls,1);
});
test('live connection check follows the actual socket state',()=>{
 const f=fixture();f.window.updateLiveSync('one');const socket=f.sockets[0];
 socket.readyState=0;assert.equal(f.window.isLiveSyncConnected(),false);
 socket.readyState=1;assert.equal(f.window.isLiveSyncConnected(),true);
 socket.readyState=2;assert.equal(f.window.isLiveSyncConnected(),false);
 socket.close();assert.equal(f.window.isLiveSyncConnected(),false);
});

test('three failures enable fallback and stop automatic retries until manual refresh',async()=>{
 const f=fixture();f.window.updateLiveSync('one');
 for(let attempt=0;attempt<3;attempt++){
  assert.equal(f.window.shouldPollInbox(),false);
  f.sockets[attempt].close();
  if(attempt<2)await f.flush();
 }
 assert.equal(f.window.shouldPollInbox(),true);assert.equal(f.timers.size,0);
 f.window.updateLiveSync('one');assert.equal(f.sockets.length,3);
 f.window.retryLiveSync();assert.equal(f.sockets.length,4);assert.equal(f.window.shouldPollInbox(),false);
 f.sockets[3].readyState=1;f.sockets[3].onopen();assert.equal(f.window.shouldPollInbox(),false);
 f.sockets[3].close();assert.equal(f.window.shouldPollInbox(),false);
});
