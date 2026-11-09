'use strict';
(() => {
  const indicator=document.getElementById('pullRefresh');
  const label=document.getElementById('pullRefreshLabel');
  const threshold=85;
  let start=null,distance=0,locked=false,reloading=false;
  function setPull(offset,progress){
    document.body.style.setProperty('--pull-offset',offset+'px');
    indicator.style.setProperty('--pull-progress',progress);
    indicator.style.setProperty('--pull-rotation',(progress*90)+'deg');
  }
  function reset(){
    start=null;distance=0;locked=false;
    delete document.body.dataset.pulling;
    indicator.dataset.ready='false';
    indicator.dataset.refreshing='false';
    setPull(0,0);
  }
  function blocked(target){
    if(document.querySelector('dialog[open]') || target.closest('input,textarea,select,button,a,summary,[contenteditable="true"],.header-menu'))return true;
    for(let node=target;node && node!==document.body;node=node.parentElement){
      if(node.scrollHeight>node.clientHeight && node.scrollTop>0 && /auto|scroll/.test(getComputedStyle(node).overflowY))return true;
    }
    return false;
  }
  document.addEventListener('touchstart',event=>{
    if(reloading)return;
    reset();
    if(event.touches.length!==1 || window.scrollY>0 || blocked(event.target))return;
    start={x:event.touches[0].clientX,y:event.touches[0].clientY};
  },{passive:true});
  document.addEventListener('touchmove',event=>{
    if(!start || reloading)return;
    if(event.touches.length!==1 || window.scrollY>0){reset();return}
    const dx=event.touches[0].clientX-start.x,dy=event.touches[0].clientY-start.y;
    if(!locked){
      if(Math.abs(dx)>12 && Math.abs(dx)>Math.abs(dy)){reset();return}
      if(dy<0){reset();return}
      // Claim the first downward move before WebKit commits to native scrolling.
      if(dy>0 && Math.abs(dy)>=Math.abs(dx) && event.cancelable)event.preventDefault();
      if(dy<12)return;
      if(!event.cancelable){reset();return}
      locked=true;
    }
    if(event.cancelable)event.preventDefault();
    distance=Math.max(0,dy);
    indicator.hidden=false;
    document.body.dataset.pulling='true';
    setPull(Math.min(110,distance*.5),Math.min(1,distance/threshold));
    label.textContent=t(distance>=threshold?'Zum Neuladen loslassen':'Zum Neuladen herunterziehen');
    indicator.dataset.ready=String(distance>=threshold);
  },{passive:false});
  document.addEventListener('touchend',()=>{
    if(!start || reloading)return;
    const reload=locked && distance>=threshold && window.scrollY<=0;
    reset();
    if(reload){
      reloading=true;indicator.hidden=false;indicator.dataset.refreshing='true';
      setPull(60,1);
      label.textContent=t('Seite wird neu geladen …');
      // Give the release position and loading animation a frame before navigating.
      window.requestAnimationFrame(()=>window.requestAnimationFrame(()=>window.location.reload()));
    }
  },{passive:true});
  document.addEventListener('touchcancel',()=>{if(!reloading)reset()},{passive:true});
})();
