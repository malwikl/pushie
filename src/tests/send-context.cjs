'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
test('success clears compose context before inbox refresh; failure retains draft',async()=>{
 const fields={};
 for(const id of ['adminSend','sendTitle','sendBody','sendLongText','targetType','sendTarget','sendResult','sendDialogStatus','refreshDeliveryStatus','sendMore'])fields[id]={value:'',addEventListener(type,fn){this[type]=fn}};
 fields.sendDialog={close(){this.closed=true}};
 const sent=[];let fail=false,refreshes=0;
 const context=vm.createContext({$:id=>fields[id],lastSentMessageId:'previous',language:'en',t:x=>x,
  setMessageTab(tab){assert.equal(tab,'sent')},renderSendTargets(){},run:async(button,fn)=>fn(),
  api:async(path,method,payload)=>{if(fail)throw Error('offline');sent.push(payload);return {messageId:'new'}},
  refreshInbox:async()=>{refreshes++;assert.equal(fields.sendBody.value,'');assert.equal(context.lastSentMessageId,null)},status(){}});
 vm.runInContext(source.slice(source.indexOf('function resetSendContext'),source.indexOf("$('createInvite').addEventListener")),context);
 function draft(body){fields.targetType.value='topic';fields.sendTarget.value='news';fields.sendTitle.value='Title';fields.sendBody.value=body;fields.sendLongText.value='Details'}
 draft('First');await fields.adminSend.click();
 assert.equal(fields.sendDialog.closed,true);assert.equal(fields.sendTitle.value,'Push');assert.equal(fields.sendLongText.value,'');assert.equal(fields.sendResult.textContent,'');assert.equal(fields.refreshDeliveryStatus.hidden,true);
 draft('Second');await fields.adminSend.click();assert.deepEqual(sent.map(p=>p.body),['First','Second']);assert.equal(refreshes,2);
 fail=true;draft('Retry');await assert.rejects(fields.adminSend.click(),/offline/);assert.equal(fields.sendBody.value,'Retry');assert.equal(fields.sendLongText.value,'Details');
});
