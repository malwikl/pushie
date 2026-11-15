'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const {element}=require('./helpers/dom.cjs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
const flush=()=>new Promise(resolve=>setImmediate(resolve));
function fixture(messages=[{id:'one',title:'Alert',body:'Details',topic:'backup'}],filter=''){
 const fields={};for(const id of ['inbox','inboxTopicFilter','unreadBadge','readAll'])fields[id]=element();
 fields.inboxTopicFilter.value=filter;
 const calls=[];
 const context=vm.createContext({$:id=>fields[id],me:{topics:[{name:'backup'},{name:'system'}]},inboxMessages:messages,openInboxMessages:new Set(),row:element,
  document:{createElement:element},messageContent:m=>{const content=element();content.textContent=m.body;return content},
  t:x=>x,language:'en',formatDate:x=>x,Date,
  api:async(...args)=>{calls.push(args)},refreshInbox:async()=>context.renderInbox(),run:async(_,fn)=>fn()});
 vm.runInContext(source.slice(source.indexOf('function inboxMessagesForTopic('),source.indexOf('function linkedMessageId()')),context);
 context.renderInbox();
 return {context,fields,calls,messages,rows:()=>fields.inbox.children.filter(node=>node.id?.startsWith('message-'))};
}
test('listing stays collapsed and unread; opening details marks only that message read and preserves expansion',async()=>{
 const f=fixture(),details=f.rows()[0].children[0],state=details.querySelector('.message-read-state');
 assert.equal(details.open,false);assert.equal(state.textContent,'Ungelesen');assert.equal(f.calls.length,0);
 details.open=true;await details.dispatch('toggle');await flush();
 assert.equal(f.calls.length,1);assert.equal(f.calls[0][0],'/api/me/messages/one/read');assert.ok(f.messages[0].readAt);
 assert.equal(f.rows()[0].children[0].open,true);assert.equal(f.rows()[0].querySelector('.message-read-state').textContent,'Read');
});
test('deleting a row does not mark it read',async()=>{
 const f=fixture(),controls=f.rows()[0].children[1];
 await controls.children[1].dispatch('click');await flush();
 assert.equal(f.calls.length,1);assert.equal(f.calls[0][0],'/api/me/messages/one');assert.equal(f.calls[0][1],'DELETE');assert.equal(f.messages[0].readAt,undefined);
});
test('explicit read action leaves details collapsed',async()=>{
 const f=fixture();await f.rows()[0].children[1].children[0].dispatch('click');await flush();
 assert.ok(f.messages[0].readAt);assert.equal(f.rows()[0].children[0].open,false);
});
test('topic filters include historical topics, preserve selection and leave the global unread count intact',()=>{
 const f=fixture([{id:'backup',topic:'backup',readAt:'yes'},{id:'retired',topic:'retired'},{id:'direct'}],'topic:backup');
 assert.equal(f.rows().length,1);assert.equal(f.rows()[0].id,'message-backup');
 assert.equal(f.fields.unreadBadge.textContent,'2');assert.equal(f.fields.readAll.disabled,true);assert.equal(f.fields.readAll.textContent,'Angezeigte gelesen');
 assert.ok(f.fields.inboxTopicFilter.children.some(option=>option.value==='topic:retired'));
 f.context.renderInbox();assert.equal(f.fields.inboxTopicFilter.value,'topic:backup');
 f.fields.inboxTopicFilter.value='direct';f.context.renderInbox();assert.equal(f.rows()[0].id,'message-direct');assert.equal(f.fields.readAll.disabled,false);
 f.fields.inboxTopicFilter.value='topic:system';f.context.renderInbox();assert.equal(f.rows().length,0);
 assert.equal(f.fields.inbox.children[0].textContent,'Keine Nachrichten für dieses Topic.');
 assert.equal(f.messages[1].readAt,undefined);
});
test('collapsed rows expose date, topic, title, read state and controls without interpreting HTML',()=>{
 const f=fixture([{id:'one',createdAt:'2026-11-15',topic:'<topic>',title:'<script>alert(1)</script>',body:'Body'}]);
 const row=f.rows()[0],summary=row.children[0].children[0];
 assert.equal(summary.children[0].dateTime,'2026-11-15');assert.equal(summary.children[1].textContent,'<topic>');
 assert.equal(summary.children[2].children[0].textContent,'<script>alert(1)</script>');
 assert.equal(row.children[1].children.length,2);
});
test('mark shown as read sends the selected topic or direct-message scope',async()=>{
 for(const [filter,expected] of [['topic:backup',{topic:'backup'}],['direct',{topic:''}],['',{}]]){
  const fields={readAll:element(),inboxTopicFilter:{value:filter}},calls=[];
  const context=vm.createContext({$:id=>fields[id],run:async(_,fn)=>fn(),api:async(...args)=>calls.push(args),refreshInbox:async()=>{},status(){},t:x=>x});
  vm.runInContext(source.slice(source.indexOf("$('readAll').addEventListener"),source.indexOf("$('bootstrapRegister').addEventListener")),context);
  await fields.readAll.dispatch('click');await flush();
  assert.equal(calls[0][0],'/api/me/messages/read-all');assert.deepEqual(JSON.parse(JSON.stringify(calls[0][2])),expected);
 }
});
