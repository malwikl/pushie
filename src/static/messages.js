'use strict';
(() => {
  const filterKey=message=>message.topic?'topic:'+message.topic:message.deliveryReason==='broadcast'?'broadcast':'direct';
  const readFilter=filter=>!filter?{}:filter.startsWith('topic:')?{topic:filter.slice(6)}:{deliveryReason:filter};
  class MessageList {
    constructor({list,filters,sent=false,expanded=new Set(),getProfile,translate,formatDate,onRead,onDelete,onFilter}){
      Object.assign(this,{list,filters,sent,expanded,getProfile,translate,formatDate,onRead,onDelete,onFilter});
      this.messages=[];this.filter='';this.rows=[];this.reading=new Set();
      if(typeof ResizeObserver!=='undefined'){
        this.observer=new ResizeObserver(entries=>{
          const width=entries[0]?.contentRect.width;
          if(width>0 && width!==this.width){this.width=width;this.scheduleLayout()}
        });
        this.observer.observe(list);
      }else window.addEventListener('resize',()=>this.scheduleLayout());
      document.fonts?.ready.then(()=>this.scheduleLayout());
    }
    label(key){return key.startsWith('topic:')?key.slice(6):this.translate(key==='broadcast'?'Rundsendung':'Direkt')}
    visible(){return this.messages.filter(message=>!this.filter || filterKey(message)===this.filter)}
    update(messages){
      this.messages=messages;
      if(this.filter&&!messages.some(message=>filterKey(message)===this.filter))this.filter='';
      for(const id of this.expanded)if(!messages.some(message=>message.id===id))this.expanded.delete(id);
      this.render();
    }
    clear(){
      this.messages=[];this.rows=[];this.filter='';this.expanded.clear();this.reading.clear();
      if(this.frame)cancelAnimationFrame(this.frame);this.frame=null;
      this.list.replaceChildren();this.filters.replaceChildren();
    }
    render(){
      const t=this.translate,counts=new Map();
      for(const message of this.messages){const key=filterKey(message);counts.set(key,(counts.get(key)||0)+1)}
      this.filters.replaceChildren();
      const choices=[['',t('Alle'),this.messages.length],...[...counts].sort(([a],[b])=>this.label(a).localeCompare(this.label(b))).map(([key,count])=>[key,this.label(key),count])];
      for(const [key,label,count] of choices){
        const button=document.createElement('button');button.type='button';button.className='message-filter small';
        button.textContent=`${label} (${count})`;button.setAttribute('aria-pressed',String(key===this.filter));
        button.onclick=()=>{this.filter=key;this.render()};this.filters.append(button);
      }
      this.list.replaceChildren();this.rows=[];
      const visible=this.visible();this.onFilter?.(visible,this.filter);
      if(!visible.length){
        const empty=document.createElement('p');empty.className='empty';
        empty.textContent=t(this.sent?'Noch keine gesendeten Nachrichten.':'Noch keine Nachrichten vorhanden.');this.list.append(empty);return;
      }
      for(const message of visible)this.appendMessage(message);
      this.scheduleLayout();
    }
    appendMessage(message){
      const t=this.translate,expanded=this.expanded.has(message.id),item=document.createElement('article');
      item.className='notification-row';item.id=(this.sent?'sent-message-':'message-')+message.id;
      const dot=document.createElement('span');dot.className='notification-dot';
      if(!this.sent&&!message.readAt){dot.classList.add('is-unread');dot.setAttribute('role','img');dot.setAttribute('aria-label',t('Ungelesen'))}
      else dot.setAttribute('aria-hidden','true');
      const content=document.createElement('div');content.className='notification-content';
      const heading=document.createElement('div'),title=document.createElement('b'),pill=document.createElement('span');
      heading.className='notification-heading';title.className='notification-title';title.textContent=message.title;
      pill.className='notification-reason';pill.textContent=this.label(filterKey(message));heading.append(title,pill);
      const preview=document.createElement('p'),text=document.createTextNode(message.body||''),more=document.createElement('button');
      preview.className='notification-preview';preview.hidden=expanded;
      more.type='button';more.className='message-more';more.textContent='… '+t('mehr');more.hidden=true;
      const fullID=(this.sent?'sent-details-':'message-details-')+message.id;
      more.setAttribute('aria-expanded',String(expanded));more.setAttribute('aria-controls',fullID);
      preview.append(text,more);
      const full=document.createElement('div'),body=document.createElement('p'),less=document.createElement('button');
      full.className='notification-full';full.id=fullID;full.hidden=!expanded;body.textContent=message.body;full.append(body);
      if(message.longText){const long=document.createElement('pre');long.className='message-longtext';long.textContent=message.longText;full.append(long)}
      less.type='button';less.className='message-more';less.textContent=t('weniger');less.hidden=true;less.setAttribute('aria-expanded','true');less.setAttribute('aria-controls',fullID);full.append(less);
      const toggle=()=>{
        const opening=!this.expanded.has(message.id);
        if(opening)this.expanded.add(message.id);else this.expanded.delete(message.id);
        this.render();
        if(opening&&!this.sent&&!this.getProfile()?.manualRead&&!message.readAt)this.requestRead(message,more);
      };
      more.onclick=toggle;less.onclick=toggle;content.append(heading,preview,full);
      const actions=document.createElement('div'),time=document.createElement('time'),buttons=document.createElement('div');
      actions.className='notification-actions';buttons.className='notification-buttons';time.dateTime=message.createdAt;time.textContent=this.formatDate(message.createdAt);
      if(!this.sent&&!message.readAt){
        const read=document.createElement('button');read.type='button';read.className='small quiet';read.textContent=t('Gelesen');
        read.setAttribute('aria-label',t('Als gelesen markieren'));read.disabled=this.reading.has(message.id);
        read.onclick=()=>this.requestRead(message,read);buttons.append(read);
      }
      const remove=document.createElement('button');remove.type='button';remove.className='small quiet danger';remove.textContent=t('Löschen');
      remove.onclick=()=>this.onDelete(message,remove);buttons.append(remove);actions.append(time,buttons);
      item.append(dot,content,actions);this.list.append(item);this.rows.push({message,preview,text,more,full,less});
    }
    async requestRead(message,button){
      if(this.reading.has(message.id)||message.readAt)return;
      this.reading.add(message.id);
      try{await this.onRead(message,button)}finally{this.reading.delete(message.id);button.disabled=false}
    }
    scheduleLayout(){
      if(this.frame)cancelAnimationFrame(this.frame);
      this.frame=requestAnimationFrame(()=>{this.frame=null;for(const entry of this.rows)this.layoutPreview(entry)});
    }
    layoutPreview({message,preview,text,more,full,less}){
      if(!preview.isConnected)return;
      const wasHidden=preview.hidden;
      preview.hidden=false;
      if(preview.clientWidth<1){preview.hidden=wasHidden;return}
      const style=getComputedStyle(preview),limit=2*(parseFloat(style.lineHeight)||parseFloat(style.fontSize)*1.5)+1;
      const body=message.body||'';
      text.nodeValue=body;more.hidden=true;
      const expandable=!!message.longText?.trim()||preview.scrollHeight>limit;
      less.hidden=!expandable;
      if(!expandable){
        this.expanded.delete(message.id);full.hidden=true;
        return;
      }
      if(this.expanded.has(message.id)){preview.hidden=true;full.hidden=false;return}
      full.hidden=true;
      more.hidden=false;
      const chars=typeof Intl.Segmenter==='function'?[...new Intl.Segmenter(undefined,{granularity:'grapheme'}).segment(body)].map(part=>part.segment):Array.from(body);
      let low=0,high=chars.length;
      // Fit actual rendered lines, including the inline action, at the current width.
      while(low<high){
        const mid=Math.ceil((low+high)/2);text.nodeValue=chars.slice(0,mid).join('').trimEnd()+' ';
        if(preview.scrollHeight<=limit)low=mid;else high=mid-1;
      }
      text.nodeValue=chars.slice(0,low).join('').trimEnd()+' ';
    }
  }
  window.PushieMessages={MessageList,filterKey,readFilter};
})();
