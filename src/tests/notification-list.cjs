'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const path=require('node:path');
const {element}=require('./helpers/dom.cjs');
const source=fs.readFileSync(path.join(__dirname,'../static/notifications.js'),'utf8');
function fixture({sent=false,manualRead=false,notifications,subscriptions}={}){
 const list=element(),filters=element(),reads=[],deletes=[],profile={manualRead,subscriptions},mutes=[];
 const document={createElement(tag){
  const node=element(tag);
  node.focus=()=>{document.activeElement=node};
  node.getAttribute=name=>node.attributes[name];
  node.hidePopover=()=>{node.open=false};
  return node;
 },createElementNS:(_,tag)=>element(tag),createTextNode:value=>({nodeValue:value})};
 const timers=new Map();
 const context=vm.createContext({window:{addEventListener(){}},document,requestAnimationFrame:()=>1,cancelAnimationFrame(){},setTimeout:callback=>{const id=Symbol();timers.set(id,callback);return id},clearTimeout:id=>timers.delete(id),Intl,Set,Map});
 vm.runInContext(source,context);
 const renderer=new context.window.PushieNotifications.NotificationList({list,filters,sent,getProfile:()=>profile,translate:x=>x,formatDate:x=>x,
  onRead:async notification=>{reads.push(notification.id);notification.readAt='now';renderer.update(renderer.notifications)},onDelete:notification=>deletes.push(notification.id),
  onMute:async (topic,duration)=>{mutes.push([topic,duration]);profile.topicMutes=duration==='unmute'?{}:{[topic]:new Date(Date.now()+900000).toISOString()}}});
 renderer.update(notifications||[{id:'one',title:'Alert',body:'Details',longText:'Full text',topic:'backup',createdAt:'2026-11-15'}]);
 return {list,filters,reads,deletes,renderer,profile,context,mutes,timers};
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
 assert.equal(f.list.children[0].querySelector('.notification-longtext').textContent,'Full text');
 f.renderer.update(f.renderer.notifications);assert.equal(f.list.children[0].querySelector('.notification-full').hidden,false);
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
test('filters and counts come only from current notifications, include historical topics, and separate broadcast/direct',()=>{
 const f=fixture({notifications:[{id:'a',topic:'retired'},{id:'b',topic:'retired',readAt:'yes'},{id:'c'},{id:'d',deliveryReason:'broadcast'}]});
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
test('a subscribed topic pill keeps its filter label and has a bell that opens mute and unmute options',async()=>{
 const f=fixture({subscriptions:['backup','automation'],notifications:[{id:'a'},{id:'b',topic:'retired'}]});
 const bell=()=>f.filters.querySelector('.topic-mute');
 assert.equal(bell(),null);
 f.renderer.update([...f.renderer.notifications,{id:'c',topic:'backup'}]);
 const pill=()=>f.filters.querySelector('.notification-topic-filter');
 const options=()=>pill().querySelector('.topic-mute-options');
 const group=pill();
 assert.equal(group.children[0].textContent,'backup (1)');
 assert.equal(group.children[1],bell());
 assert.equal(bell().tagName,'BUTTON');
 assert.equal(bell().attributes.popovertarget,options().id);
 assert.equal(options().attributes.popover,'auto');
 assert.deepEqual(options().children.map(option=>option.textContent),['15 Minuten','30 Minuten','60 Minuten','4 Stunden','Bis morgen']);
 await options().children[0].onclick();
 assert.match(options().children[0].textContent,/^Stumm bis /);
 assert.equal(bell().className,'topic-mute is-muted');
 assert.equal(options().children[1].textContent,'Stummschaltung aufheben');
 assert.equal(f.list.children.length,3);
 await options().children[1].onclick();
 assert.equal(bell().className,'topic-mute');
 assert.deepEqual(f.mutes,[['backup','15m'],['backup','unmute']]);
 pill().children[0].onclick();
 assert.equal(f.renderer.filter,'topic:backup');
 assert.equal(f.list.children.length,1);
 assert.equal(fixture({sent:true,subscriptions:['backup']}).filters.querySelector('.topic-mute'),null);
});
test('the bell clears the muted state when the topic mute expires',async()=>{
 const f=fixture({subscriptions:['backup']});
 const bell=()=>f.filters.querySelector('.topic-mute');
 await f.filters.querySelector('.topic-mute-options').children[0].onclick();
 assert.equal(bell().className,'topic-mute is-muted');
 assert.equal(f.timers.size,1);
 f.context.Date=class extends Date { static now(){return Date.parse(f.profile.topicMutes.backup)} };
 f.timers.values().next().value();
 assert.equal(bell().className,'topic-mute');
 assert.equal(f.filters.querySelector('.topic-mute-options').children.length,5);
 assert.deepEqual(f.mutes,[['backup','15m']]);
});
test('notification titles and bodies are plain text and clearing removes content, selections and expansions',()=>{
 const f=fixture({notifications:[{id:'x',title:'<img onerror=alert(1)>',body:'<script>private</script>',longText:'<b>details</b>'}]});
 assert.equal(f.list.children[0].querySelector('.notification-title').textContent,'<img onerror=alert(1)>');
 assert.equal(f.list.children[0].querySelector('.notification-preview').children[0].nodeValue,'<script>private</script>');
 f.renderer.expanded.add('x');f.renderer.filter='direct';f.renderer.clear();assert.equal(f.list.children.length,0);assert.equal(f.filters.children.length,0);assert.equal(f.renderer.filter,'');assert.equal(f.renderer.expanded.size,0);
});
test('read filters map topic, direct and broadcast to separate API scopes',()=>{
 const {context}=fixture(),filter=context.window.PushieNotifications.readFilter;
 for(const [key,expected] of [['',{}],['topic:backup',{topic:'backup'}],['direct',{deliveryReason:'direct'}],['broadcast',{deliveryReason:'broadcast'}]])assert.deepEqual(JSON.parse(JSON.stringify(filter(key))),expected);
});
test('short notification links do not expose less; long text remains expandable',()=>{
 const f=fixture({notifications:[{id:'short',body:'Short',title:'Title'}]});
 f.renderer.expanded.add('short');f.renderer.render();
 const entry=f.renderer.rows[0];entry.preview.isConnected=true;entry.preview.clientWidth=300;entry.preview.scrollHeight=21;
 f.context.getComputedStyle=()=>({lineHeight:'21px',fontSize:'14px'});
 f.renderer.layoutPreview(entry);
 assert.equal(entry.preview.hidden,false);assert.equal(entry.full.hidden,true);assert.equal(entry.more.hidden,true);assert.equal(entry.less.hidden,true);assert.equal(f.renderer.expanded.has('short'),false);
 const long=fixture();long.renderer.expanded.add('one');long.renderer.render();
 const expanded=long.renderer.rows[0];expanded.preview.isConnected=true;expanded.preview.clientWidth=300;expanded.preview.scrollHeight=21;long.context.getComputedStyle=f.context.getComputedStyle;
 long.renderer.layoutPreview(expanded);assert.equal(expanded.full.hidden,false);assert.equal(expanded.less.hidden,false);
});
