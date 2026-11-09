'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
function fixture(role){
 const elements=new Map();
 function node(id=''){
  const el={children:[],attributes:{},hidden:false,open:false,
   append(...nodes){for(const n of nodes){n.remove();el.children.push(n);n.parent=el}},
   remove(){if(el.parent)el.parent.children=el.parent.children.filter(n=>n!==el);el.parent=null},
   setAttribute(k,v){el.attributes[k]=v},removeAttribute(k){delete el.attributes[k]},focus(){},
   showModal(){el.open=true},close(){el.open=false}};
  Object.defineProperty(el,'id',{get:()=>id,set:value=>{id=value;elements.set(id,el)}});
  if(id)elements.set(id,el);return el;
 }
 const account=node('account');account.append(node('profile'),node('inboxPanel'),node('devicesPanel'),node('settingsPanel'));
 const admin=node('adminPanel');account.append(admin);
 admin.append(node('heading'),node('sendHeading'),node('sendForm'),node('inviteSection'),node('adminUsers'),node('topicSection'),node('topicForm'),node('storageSection'),node('storageStats'));
 for(const id of ['sendResult','refreshDeliveryStatus','openSend','messagesShortcut','devicesShortcut','topicsShortcut','accountShortcut','adminShortcut'])node(id);
 const context=vm.createContext({document:{createElement:()=>node()},$:id=>elements.get(id),me:{role},t:x=>x,window:{addEventListener(){}},location:{hash:''},closeHeaderMenu(){},renderTargetDevices(){},refreshAdminTargets:async()=>{},createCentralLog:()=>node(),refreshCentralLog:async()=>{},status(){}});
 vm.runInContext(source.slice(source.indexOf('let viewsReady='),source.indexOf('function closeHeaderMenu')),context);
 vm.runInContext('setupViews()',context);
 return {account,context,$:id=>elements.get(id)};
}
test('one menu switches views; subscriptions and creation share the topic view',()=>{
 const {account,context,$}=fixture('admin');
 assert.equal($('messagesView').hidden,false);
 assert.equal($('accountView').hidden,true);
 $('devicesShortcut').onclick();assert.equal($('devicesView').hidden,false);assert.equal($('devicesPanel').parent,$('devicesView'));assert.equal($('accountView').children.length,2);
 $('topicsShortcut').onclick();assert.equal($('topicsView').hidden,false);assert.equal($('messagesView').hidden,true);
 assert.equal($('topics').parent,$('topicCreate').parent);
 assert.equal($('topicCreate').children[0].id,'topicSection');
 $('adminShortcut').onclick();assert.equal($('administrationView').hidden,false);
 vm.runInContext('setupViews()',context);assert.equal($('administrationView').hidden,false);
 assert.equal($('storageSection').parent.parent,$('administrationView'));
 assert.equal($('sendDialog').children[1].id,'sendForm');
 $('openSend').onclick();assert.equal($('sendDialog').open,true);
 vm.runInContext("me.role='';setupViews()",context);assert.equal($('administrationView').hidden,true);assert.equal($('messagesView').hidden,false);
 assert.equal(account.children.some(el=>el.attributes.role==='tablist'),false);
});
test('ordinary accounts cannot open admin view or compose dialog',()=>{
 const {$}=fixture('');
 $('adminShortcut').onclick();assert.equal($('administrationView').hidden,true);assert.equal($('messagesView').hidden,false);
 $('openSend').onclick();assert.equal($('sendDialog').open,false);
 $('topicsShortcut').onclick();assert.equal($('topicsView').hidden,false);
});

test('header menu opens with focus and closes on Escape, outside click or focus leaving',()=>{
 const elements=new Map(),listeners={};
 for(const id of ['headerMenu','menuToggle'])elements.set(id,{hidden:true,attributes:{},addEventListener(type,fn){this[type]=fn},setAttribute(k,v){this.attributes[k]=v},focus(){this.focused=true},querySelector(){return {focus(){elements.get('headerMenu').childFocused=true}}}});
 const context=vm.createContext({$:id=>elements.get(id),document:{addEventListener(type,fn){listeners[type]=fn}}});
 vm.runInContext(source.slice(source.indexOf('function closeHeaderMenu'),source.indexOf('async function setUiLanguage')),context);
 const menu=elements.get('headerMenu'),toggle=elements.get('menuToggle');
 toggle.onclick();assert.equal(menu.hidden,false);assert.equal(toggle.attributes['aria-expanded'],'true');assert.equal(menu.childFocused,true);
 listeners.keydown({key:'Escape',preventDefault(){}});assert.equal(menu.hidden,true);assert.equal(toggle.focused,true);
 toggle.onclick();listeners.click({target:{closest:()=>null}});assert.equal(menu.hidden,true);
 toggle.onclick();listeners.focusin({target:{closest:()=>null}});assert.equal(menu.hidden,true);
});
