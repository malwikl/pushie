'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
function fixture(){
 const fields=Array.from({length:4},()=>({value:'private draft'}));
 const elements=new Map();
 const $=id=>{if(!elements.has(id))elements.set(id,{value:'private',children:['private'],replaceChildren(){this.children=[]},reset(){this.wasReset=true},querySelectorAll(){return fields}});return elements.get(id)};
 const context=vm.createContext({window:{},$,t:x=>x,identityEpoch:0,resetViews:null,closeHeaderMenu(){},setMessageTab(){},inboxMessages:[{body:'private'}],adminTargets:{users:['private']},lastSentMessageId:'private',storageCleanupPreview:{private:true}});
 vm.runInContext(source.slice(source.indexOf('function clearAccountState()')),context);
 vm.runInContext(source.slice(source.indexOf('async function api('),source.indexOf('async function ceremony(')),context);
 return {context,$,fields};
}
test('logout clears cached messages, invitation codes and drafts',()=>{
 const {context,$,fields}=fixture();
 vm.runInContext('clearAccountState()',context);
 assert.equal(context.identityEpoch,1);
 assert.deepEqual($('centralLogList').children,[]);
 assert.equal($('centralLogFilters').wasReset,true);
 assert.equal(context.inboxMessages.length,0);
 assert.equal(context.adminTargets,null);
 assert.equal(context.storageCleanupPreview,null);
 for(const id of ['inbox','inviteResult','sendResult','cleanupPreview'])assert.deepEqual($(id).children,[]);
 for(const field of fields)assert.equal(field.value,'');
 assert.equal($('bootstrapCode').value,'');
 assert.equal($('invite').value,'');
 assert.deepEqual($('apiKeys').children,[]);
 assert.equal($('newApiKey').hidden,true);
 assert.deepEqual($('integrationKeys').children,[]);
 assert.equal($('newIntegrationKey').hidden,true);
 assert.equal($('executeStorageCleanup').disabled,true);
});
test('response started before logout is rejected after logout',async()=>{
 const {context}=fixture();let release;
 context.fetch=async()=>({ok:true,json:()=>new Promise(resolve=>{release=resolve})});
 const pending=vm.runInContext("api('/api/admin/targets')",context);
 await new Promise(resolve=>setImmediate(resolve));
 vm.runInContext('clearAccountState()',context);
 release({users:[{name:'private'}]});
 await assert.rejects(pending,/Sitzung geändert/);
});

test('rate-limited profile refresh keeps the login and exposes Retry-After',async()=>{
 const account={id:'alice'};
 const context=vm.createContext({window:{},me:account,identityEpoch:0,t:x=>x,
  fetch:async()=>({ok:false,status:429,headers:{get:()=> '60'}}),
  clearAccountState(){assert.fail('429 must not clear login')}});
 vm.runInContext(source.slice(source.indexOf('async function refresh('),source.indexOf('async function refreshUsers')),context);
 await assert.rejects(context.refresh(false,true),error=>error.retryAfterMs===60000);
 assert.equal(context.me,account);
});

test('401 stops background refresh, clears caches and account data and shows login',async()=>{
 const {context,$}=fixture();const deleted=[];let calls=0,rendered=false,focused=false;
 Object.assign(context,{me:{id:'alice'},status(){},render(){rendered=context.me===null},
  caches:{keys:async()=>['inbox','pushie-navigation'],delete:async key=>deleted.push(key)},
  sessionStorage:{clear(){deleted.push('session')}},fetch:async()=>{calls++;return {ok:false,status:401,json:async()=>({error:'Bitte anmelden'})}}});
 $('login').focus=()=>{focused=true};
 await assert.rejects(context.api('/api/me/messages'),/Bitte anmelden/);
 assert.equal(context.window.authRefreshStopped,true);assert.equal(context.me,null);assert.ok(rendered);assert.ok(focused);
 assert.deepEqual(deleted,['inbox','pushie-navigation','session']);assert.equal(context.inboxMessages.length,0);
 await assert.rejects(context.api('/api/me/messages'),/Bitte anmelden/);assert.equal(calls,1);
 vm.runInContext(source.slice(source.indexOf('async function refresh('),source.indexOf('async function refreshUsers')),context);
 await context.refresh();assert.equal(calls,1);
});
test('403 does not end a valid session',async()=>{
 const {context}=fixture();const account={id:'alice'};context.me=account;
 context.fetch=async()=>({ok:false,status:403,json:async()=>({error:'Forbidden'})});
 await assert.rejects(context.api('/api/admin/targets'),/Forbidden/);
 assert.equal(context.me,account);assert.equal(context.window.authRefreshStopped,undefined);
});
test('successful reauthentication resumes refreshes after cache cleanup',async()=>{
 const window={authRefreshStopped:true,authCleanup:Promise.resolve()};let refreshed=false;
 const context=vm.createContext({window,navigator:{credentials:{get:async()=>({})}},
  requestOptions:value=>value,credentialJSON:()=>({}),api:async()=>({options:{}}),
  refresh:async()=>{assert.equal(window.authRefreshStopped,false);refreshed=true}});
 vm.runInContext(source.slice(source.indexOf('async function ceremony('),source.indexOf('function row()')),context);
 await context.ceremony('login');assert.ok(refreshed);
});
