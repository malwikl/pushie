'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const path=require('node:path');
const {element}=require('./helpers/dom.cjs');
const source=fs.readFileSync(path.join(__dirname,'../static/messages.js'),'utf8');
function fixture({sent=false,manualRead=false,messages}={}){
 const list=element(),filters=element(),reads=[],deletes=[],profile={manualRead};
 const context=vm.createContext({window:{addEventListener(){}},document:{createElement:element,createTextNode:value=>({nodeValue:value})},requestAnimationFrame:()=>1,cancelAnimationFrame(){},Intl,Set,Map});
 vm.runInContext(source,context);
 const renderer=new context.window.PushieMessages.MessageList({list,filters,sent,getProfile:()=>profile,translate:x=>x,formatDate:x=>x,
  onRead:async message=>{reads.push(message.id);message.readAt='now';renderer.update(renderer.messages)},onDelete:message=>deletes.push(message.id)});
 renderer.update(messages||[{id:'one',title:'Alert',body:'Details',longText:'Full text',topic:'backup',createdAt:'2026-11-15'}]);
 return {list,filters,reads,deletes,renderer,profile,context};
}
const more=f=>f.list.children[0].querySelector('.notification-preview').children[1];
test('unread state is only a leading accessible dot; rendering does not mark read',()=>{
 const f=fixture(),dot=f.list.children[0].children[0];
 assert.equal(dot.className,'notification-dot is-unread');assert.equal(dot.attributes['aria-label'],'Ungelesen');assert.equal(dot.textContent,undefined);
 assert.deepEqual(f.reads,[]);assert.equal(f.list.children[0].querySelector('.notification-full').hidden,true);
});
test('more opens full body and long text, marks read automatically and preserves expansion on refresh',async()=>{
 const f=fixture();more(f).onclick();await new Promise(resolve=>setImmediate(resolve));
 assert.deepEqual(f.reads,['one']);assert.equal(f.list.children[0].querySelector('.notification-full').hidden,false);
 assert.equal(f.list.children[0].querySelector('.message-longtext').textContent,'Full text');
 f.renderer.update(f.renderer.messages);assert.equal(f.list.children[0].querySelector('.notification-full').hidden,false);
 assert.equal(f.list.children[0].children[0].className,'notification-dot');
});
test('manual preference leaves expansion unread until the short Read action is used',async()=>{
 const f=fixture({manualRead:true});more(f).onclick();await new Promise(resolve=>setImmediate(resolve));
 assert.deepEqual(f.reads,[]);assert.equal(f.list.children[0].querySelector('.notification-full').hidden,false);
 const read=f.list.children[0].querySelector('.notification-buttons').children[0];assert.equal(read.textContent,'Gelesen');await read.onclick();
 assert.deepEqual(f.reads,['one']);assert.equal(f.list.children[0].querySelector('.notification-buttons').children.length,1);
});
test('delete is independent of reading or expanding',()=>{
 const f=fixture();f.list.children[0].querySelector('.notification-buttons').children[1].onclick();
 assert.deepEqual(f.deletes,['one']);assert.deepEqual(f.reads,[]);assert.equal(f.renderer.expanded.size,0);
});
test('filters and counts come only from current messages, include historical topics, and separate broadcast/direct',()=>{
 const f=fixture({messages:[{id:'a',topic:'retired'},{id:'b',topic:'retired',readAt:'yes'},{id:'c'},{id:'d',deliveryReason:'broadcast'}]});
 assert.ok(f.filters.children.some(button=>button.textContent==='retired (2)'));
 assert.ok(f.filters.children.some(button=>button.textContent==='Direkt (1)'));
 assert.ok(f.filters.children.some(button=>button.textContent==='Rundsendung (1)'));
 f.filters.children.find(button=>button.textContent==='retired (2)').onclick();assert.equal(f.list.children.length,2);assert.equal(f.renderer.filter,'topic:retired');
 f.renderer.update([{id:'c'}]);assert.equal(f.renderer.filter,'');assert.equal(f.filters.children.length,2);
});
test('Sent uses the same pills, previews, details, filters and delete layout without unread or Read actions',()=>{
 const f=fixture({sent:true});assert.equal(f.list.children[0].children[0].className,'notification-dot');
 assert.equal(f.list.children[0].querySelector('.notification-reason').textContent,'backup');
 assert.equal(f.list.children[0].querySelector('.notification-buttons').children.length,1);
 more(f).onclick();assert.equal(f.list.children[0].querySelector('.notification-full').hidden,false);assert.deepEqual(f.reads,[]);
});
test('message titles and bodies are plain text and clearing removes content, selections and expansions',()=>{
 const f=fixture({messages:[{id:'x',title:'<img onerror=alert(1)>',body:'<script>private</script>',longText:'<b>details</b>'}]});
 assert.equal(f.list.children[0].querySelector('.notification-title').textContent,'<img onerror=alert(1)>');
 assert.equal(f.list.children[0].querySelector('.notification-preview').children[0].nodeValue,'<script>private</script>');
 f.renderer.expanded.add('x');f.renderer.filter='direct';f.renderer.clear();assert.equal(f.list.children.length,0);assert.equal(f.filters.children.length,0);assert.equal(f.renderer.filter,'');assert.equal(f.renderer.expanded.size,0);
});
test('read filters map topic, direct and broadcast to separate API scopes',()=>{
 const {context}=fixture(),filter=context.window.PushieMessages.readFilter;
 for(const [key,expected] of [['',{}],['topic:backup',{topic:'backup'}],['direct',{deliveryReason:'direct'}],['broadcast',{deliveryReason:'broadcast'}]])assert.deepEqual(JSON.parse(JSON.stringify(filter(key))),expected);
});
