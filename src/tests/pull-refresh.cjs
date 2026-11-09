'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'../static/pull-refresh.js'),'utf8');
function fixture(){
 const handlers={},indicator={hidden:true,dataset:{},style:{setProperty(){}}},label={};let reloads=0;
 const body={dataset:{},style:{setProperty(){}}},document={body,getElementById:id=>id==='pullRefresh'?indicator:label,querySelector:()=>null,addEventListener:(name,fn)=>handlers[name]=fn};
 const window={requestAnimationFrame:fn=>fn(),scrollY:0,location:{reload(){reloads++}}};
 vm.runInNewContext(source,{document,window,t:x=>x,getComputedStyle:()=>({overflowY:'visible'})});
 const target={closest:()=>null,parentElement:body};
 const touch=(x,y)=>({target,touches:[{clientX:x,clientY:y}],cancelable:true,preventDefault(){}});
 return {handlers,indicator,label,window,document,target,touch,get reloads(){return reloads}};
}
test('pull past threshold reloads once on release, not while dragging',()=>{
 const f=fixture();f.handlers.touchstart(f.touch(0,0));f.handlers.touchmove(f.touch(0,100));assert.equal(f.reloads,0);assert.equal(f.indicator.dataset.ready,'true');f.handlers.touchend();f.handlers.touchend();assert.equal(f.reloads,1);
});
test('short pulls, horizontal swipes, cancellation and scrolling do not reload',()=>{
 for(const kind of ['short','horizontal','cancel','scrolled','dialog','control']){
  const f=fixture();if(kind==='scrolled')f.window.scrollY=20;if(kind==='dialog')f.document.querySelector=()=>({});if(kind==='control')f.target.closest=()=>({});
  f.handlers.touchstart(f.touch(0,0));f.handlers.touchmove(f.touch(kind==='horizontal'?150:0,kind==='short'?40:100));if(kind==='cancel')f.handlers.touchcancel();f.handlers.touchend();assert.equal(f.reloads,0,kind);
 }
});
test('first small downward move is intercepted before WebKit takes the gesture',()=>{
 const f=fixture();let prevented=false;f.handlers.touchstart(f.touch(0,0));
 const move=f.touch(0,3);move.preventDefault=()=>{prevented=true};f.handlers.touchmove(move);
 assert.equal(prevented,true);f.handlers.touchmove(f.touch(0,100));f.handlers.touchend();assert.equal(f.reloads,1);
});
