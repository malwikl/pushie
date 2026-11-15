'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
function fixture(){
 const fields={};let focused=null;
 for(const id of ['receivedTab','sentTab','receivedPanel','sentPanel','refreshInbox','refreshSentMessages','readAll','messageTabs','cancelSend','messageActions','cleanupInboxMessages','cleanupSentMessages'])fields[id]={
  attributes:{},hidden:false,addEventListener(type,fn){this[type]=fn},setAttribute(k,v){this.attributes[k]=v},focus(){focused=id}
 };
 fields.sendDialog={open:true,close(){this.open=false}};
 const context=vm.createContext({$:id=>fields[id],receivedMessageList:null,sentMessageList:null,inboxMessages:[{id:'unread'}],sentMessages:[],t:x=>x});
 vm.runInContext(source.slice(source.indexOf("let activeMessageTab="),source.indexOf('let inboxMessages=')),context);
 vm.runInContext(source.slice(source.indexOf('function updateMessageActions()'),source.indexOf('function closeMessageActions(')),context);
 context.setMessageTab('received');
 return {fields,context,focused:()=>focused,key(key){let prevented=false;fields.messageTabs.keydown({key,preventDefault(){prevented=true}});return prevented}};
}
test('switching message tabs shows the relevant list and actions without moving click focus',()=>{
 const {fields,focused}=fixture();
 assert.equal(fields.sentPanel.hidden,true);assert.equal(fields.readAll.hidden,false);
 fields.sentTab.click();
 assert.equal(fields.receivedPanel.hidden,true);assert.equal(fields.sentPanel.hidden,false);
 assert.equal(fields.receivedTab.attributes['aria-selected'],'false');assert.equal(fields.sentTab.attributes['aria-selected'],'true');
 assert.equal(fields.receivedTab.tabIndex,-1);assert.equal(fields.sentTab.tabIndex,0);
 assert.equal(fields.readAll.hidden,true);assert.equal(fields.refreshInbox.hidden,true);assert.equal(fields.refreshSentMessages.hidden,false);
 assert.equal(focused(),null);
 fields.receivedTab.click();assert.equal(fields.receivedPanel.hidden,false);assert.equal(fields.sentPanel.hidden,true);assert.equal(fields.readAll.hidden,false);
});
test('arrow keys wrap between tabs; Home and End select and focus the matching tab',()=>{
 const f=fixture();
 assert.equal(f.key('ArrowRight'),true);assert.equal(f.focused(),'sentTab');
 f.key('ArrowRight');assert.equal(f.focused(),'receivedTab');
 f.key('ArrowLeft');assert.equal(f.focused(),'sentTab');
 f.key('Home');assert.equal(f.focused(),'receivedTab');
 f.key('End');assert.equal(f.focused(),'sentTab');
 assert.equal(f.key('Tab'),false);
});
test('notification navigation can reveal Received from Sent and cancellation closes compose',()=>{
 const {context,fields}=fixture();
 fields.sentTab.click();context.setMessageTab('received');assert.equal(fields.receivedPanel.hidden,false);assert.equal(fields.sentPanel.hidden,true);
 fields.cancelSend.click();assert.equal(fields.sendDialog.open,false);
});
test('read-all is hidden for read-only/empty/filtered-read lists and always hidden in Sent',()=>{
 const f=fixture();
 f.context.inboxMessages=[{id:'read',readAt:'yes'}];f.context.updateMessageActions();assert.equal(f.fields.readAll.hidden,true);
 f.context.inboxMessages=[{id:'unread'}];f.context.receivedMessageList={filter:'topic:backup',visible:()=>[{id:'read',readAt:'yes'}]};f.context.updateMessageActions();
 assert.equal(f.fields.readAll.hidden,true);assert.equal(f.fields.cleanupInboxMessages.textContent,'Angezeigte löschen …');
 f.context.receivedMessageList={filter:'topic:backup',visible:()=>[{id:'unread'}]};f.context.updateMessageActions();assert.equal(f.fields.readAll.hidden,false);assert.equal(f.fields.readAll.textContent,'Angezeigte gelesen');
 f.fields.messageActions.open=true;f.fields.sentTab.click();assert.equal(f.fields.messageActions.open,false);assert.equal(f.fields.readAll.hidden,true);assert.equal(f.fields.cleanupInboxMessages.hidden,true);assert.equal(f.fields.cleanupSentMessages.hidden,false);
});
