'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const {element}=require('./helpers/dom.cjs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/onboarding.js'),'utf8');
function fixture({nav={},installed=false,devices=[],complete=false,register=async()=>({id:'device'})}={}){
 const nodes=new Map(),calls=[],events={};
 const $=id=>{if(!nodes.has(id)){const e=element();e.focus=()=>{};e.showModal=()=>{e.open=true};e.close=()=>{e.open=false};e.click=()=>e.onclick?.();nodes.set(id,e)}return nodes.get(id)};
 const profile={id:'alice',devices,onboardingComplete:complete};
 const navigator={userAgent:'Mozilla Chrome/140',platform:'Win32',...nav};
 const window={matchMedia:()=>({matches:installed}),PushManager:function(){},Notification:{permission:'default'},addEventListener:(name,fn)=>{events[name]=fn}};
 const context=vm.createContext({window,navigator,localStorage:{getItem:()=>null},document:{getElementById:$,createElement:element}});
 vm.runInContext(source,context);
 const onboarding=window.createPushieOnboarding({getProfile:()=>profile,getRegistration:()=>({pushManager:{}}),register,api:async(...args)=>calls.push(args),translate:x=>x});
 return {context,onboarding,profile,$,calls,events,info:window.PushieDevice.deviceInfo,suggest:window.PushieDevice.suggestedName};
}
test('iPhone/iPad require Home Screen installation; Android and desktop do not',()=>{
 const f=fixture();
 for(const [nav,kind,required] of [[{userAgent:'iPhone Safari',platform:'iPhone'},'iphone',true],[{userAgent:'Macintosh Safari',platform:'MacIntel',maxTouchPoints:5},'ipad',true],[{userAgent:'Android Chrome',platform:'Linux'},'android',false],[{platform:'MacIntel'},'mac',false],[{platform:'Win32'},'windows',false]]){
  const result=f.info(nav);assert.equal(result.kind,kind);assert.equal(result.requiresInstall,required);
 }
 assert.equal(f.info({userAgent:'iPhone',platform:'iPhone'},true).requiresInstall,false);
});
test('suggested names use the available model or a platform fallback and remain editable',async()=>{
 let resolveModel;
 const f=fixture({nav:{userAgent:'Android Chrome',userAgentData:{platform:'Android',getHighEntropyValues:()=>new Promise(resolve=>{resolveModel=resolve})}}});
 f.onboarding.update(f.profile);assert.match(f.$('onboardingDeviceName').value,/Mein Android-Gerät/);
 f.$('onboardingDeviceName').value='Kitchen tablet';resolveModel({model:'Pixel Tablet'});await new Promise(resolve=>setImmediate(resolve));
 assert.equal(f.$('onboardingDeviceName').value,'Kitchen tablet');assert.match(f.$('deviceName').value,/Pixel Tablet/);
});
test('first login opens setup; registration is called directly from the button and completion is saved',async()=>{
 const registered=[];const f=fixture({register:name=>{registered.push(name);return Promise.resolve({id:'device'})}});
 f.onboarding.update(f.profile);assert.equal(f.$('onboardingDialog').open,true);
 f.$('onboardingNext').onclick();f.$('onboardingDeviceName').value='Office PC';
 const pending=f.$('onboardingRegister').onclick();assert.deepEqual(registered,['Office PC']);await pending;
 assert.equal(f.profile.onboardingComplete,true);assert.equal(f.$('onboardingDialog').open,false);
 assert.equal(f.calls[0][0],'/api/me/settings');assert.equal(f.calls[0][2].onboardingComplete,true);
});
test('iOS browser setup explains installation and blocks registration until opened as an app',()=>{
 const f=fixture({nav:{userAgent:'iPhone Safari',platform:'iPhone'}});f.onboarding.update(f.profile);
 assert.equal(f.$('onboardingNext').disabled,true);assert.match(f.$('onboardingInstallInfo').textContent,/Home-Bildschirm-App/);
 assert.equal(f.$('onboardingInstructions').children.length,3);
});
test('permission denial keeps setup open, preserves the name and offers retry/settings guidance',async()=>{
 const f=fixture({register:async()=>{throw Object.assign(new Error('denied'),{name:'NotAllowedError'})}});
 f.onboarding.update(f.profile);f.$('onboardingNext').onclick();f.$('onboardingDeviceName').value='My laptop';await f.$('onboardingRegister').onclick();
 assert.equal(f.$('onboardingDialog').open,true);assert.equal(f.$('onboardingDeviceName').value,'My laptop');assert.match(f.$('onboardingStatus').textContent,/blockiert/);assert.equal(f.calls.length,0);
});
test('Later saves completion; existing users can reopen device setup and logout clears it',async()=>{
 const f=fixture();f.onboarding.update(f.profile);await f.$('onboardingLater').onclick();assert.equal(f.$('onboardingDialog').open,false);assert.equal(f.profile.onboardingComplete,true);
 f.onboarding.update(f.profile);assert.equal(f.$('onboardingDialog').open,false);f.$('setupDevice').onclick();assert.equal(f.$('onboardingDialog').open,true);
 f.onboarding.reset();f.$('restartOnboarding').onclick();assert.equal(f.$('onboardingDialog').open,true);
 f.onboarding.reset();assert.equal(f.$('onboardingDialog').open,false);assert.equal(f.$('onboardingDeviceName').value,'');
 const existing=fixture({devices:[{id:'registered'}]});existing.onboarding.update(existing.profile);assert.equal(existing.$('onboardingDialog').open,false);
});
