'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
test('compact summary handles all, selected, empty and zero-device recipients',()=>{
 const fields={targetType:{value:'user'},sendTarget:{value:'alice'},allDevices:{checked:true},targetDevices:{querySelectorAll:()=>[{}]},deviceSummary:{},changeDevices:{},recipientSummary:{},adminSend:{}};
 const context=vm.createContext({$:id=>fields[id],language:'en',t:x=>x,adminTargets:{users:[{id:'alice'}],devices:[{userId:'alice'},{userId:'alice'}]}});
 vm.runInContext(source.slice(source.indexOf('function updateRecipientSummary'),source.indexOf("$('sendTarget').addEventListener")),context);
 context.updateRecipientSummary();assert.equal(fields.deviceSummary.textContent,'All devices (2)');assert.equal(fields.recipientSummary.hidden,true);assert.equal(fields.adminSend.disabled,false);
 fields.allDevices.checked=false;context.updateRecipientSummary();assert.equal(fields.deviceSummary.textContent,'1 of 2 devices');
 fields.targetDevices.querySelectorAll=()=>[];context.updateRecipientSummary();assert.equal(fields.adminSend.disabled,true);
 fields.allDevices.checked=true;context.adminTargets.devices=[];context.updateRecipientSummary();assert.equal(fields.adminSend.disabled,false);assert.equal(fields.changeDevices.hidden,true);
});
