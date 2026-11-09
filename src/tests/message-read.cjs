'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/app.js'),'utf8');
function fixture(){
 const element=()=>({children:[],listeners:{},classList:{add(){},remove(){}},append(...items){this.children.push(...items)},prepend(item){this.children.unshift(item)},replaceChildren(){this.children=[]},addEventListener(type,fn){this.listeners[type]=fn}});
 const elements={inbox:element(),unreadBadge:element(),readAll:element()},calls=[];
 const message={id:'one'},heading=element(),content=element();content.querySelector=()=>heading;
 const context=vm.createContext({$:id=>elements[id],inboxMessages:[message],row:element,document:{createElement:element},messageContent:()=>content,t:x=>x,language:'en',Date,
  api:async(...args)=>{calls.push(args)},refreshInbox:async()=>{},run:async(_,fn)=>fn()});
 vm.runInContext(source.slice(source.indexOf('function renderInbox()'),source.indexOf('function linkedMessageId()')),context);
 context.renderInbox();
 return {message,heading,content,calls};
}
test('rendering shows an explicit unread label without marking read; tapping marks only that message',async()=>{
 const f=fixture();assert.equal(f.heading.children[0].textContent,'Ungelesen');assert.equal(f.calls.length,0);
 await f.content.listeners.click({target:{closest:()=>null}});
 // The click listener launches the async read operation.
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(f.calls.length,1);assert.equal(f.calls[0][0],'/api/me/messages/one/read');
 assert.ok(f.message.readAt);assert.equal(f.heading.children[0].textContent,'Read');
});
test('utility controls do not mark messages read',async()=>{
 const f=fixture();f.content.listeners.click({target:{closest:()=>({})}});
 await new Promise(resolve=>setImmediate(resolve));assert.equal(f.calls.length,0);assert.equal(f.message.readAt,undefined);
});
