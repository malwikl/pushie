'use strict';
(() => {
  const filterKey=notification=>notification.topic?'topic:'+notification.topic:notification.deliveryReason==='broadcast'?'broadcast':'direct';
  const readFilter=filter=>!filter?{}:filter.startsWith('topic:')?{topic:filter.slice(6)}:{deliveryReason:filter};
  class NotificationList {
    constructor({list,filters,sent=false,expanded=new Set(),getProfile,translate,formatDate,formatMuteUntil=formatDate,onRead,onDelete,onFilter,onMute}){
      Object.assign(this,{list,filters,sent,expanded,getProfile,translate,formatDate,formatMuteUntil,onRead,onDelete,onFilter,onMute});
      this.notifications=[];this.filter='';this.rows=[];this.reading=new Set();this.savingMutes=new Map();
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
    visible(){return this.notifications.filter(notification=>!this.filter || filterKey(notification)===this.filter)}
    update(notifications){
      this.notifications=notifications;
      if(this.filter&&!notifications.some(notification=>filterKey(notification)===this.filter))this.filter='';
      for(const id of this.expanded)if(!notifications.some(notification=>notification.id===id))this.expanded.delete(id);
      this.render();
    }
    clear(){
      this.notifications=[];this.rows=[];this.filter='';this.expanded.clear();this.reading.clear();
      this.savingMutes.clear();
      if(this.muteTimer)clearTimeout(this.muteTimer);this.muteTimer=null;
      if(this.frame)cancelAnimationFrame(this.frame);this.frame=null;
      this.list.replaceChildren();this.filters.replaceChildren();
    }
    renderFilters(){
      const t=this.translate,counts=new Map();
      for(const notification of this.notifications){const key=filterKey(notification);counts.set(key,(counts.get(key)||0)+1)}
      if(this.muteTimer)clearTimeout(this.muteTimer);this.muteTimer=null;
      const profile=this.getProfile(),now=Date.now();
      let nextExpiry=Infinity,focusedControl;
      const focusedTopic=document.activeElement?.getAttribute('data-topic-mute');
      this.filters.replaceChildren();
      const choices=[['',t('Alle'),this.notifications.length],...[...counts].sort(([a],[b])=>this.label(a).localeCompare(this.label(b))).map(([key,count])=>[key,this.label(key),count])];
      for(const [key,label,count] of choices){
        const button=document.createElement('button');button.type='button';button.className='notification-filter small';
        button.textContent=`${label} (${count})`;button.setAttribute('aria-pressed',String(key===this.filter));
        button.onclick=()=>{this.filter=key;this.render()};
        const topic=readFilter(key).topic;
        if(this.sent || !topic || !this.onMute || !profile?.subscriptions?.includes(topic)){
          this.filters.append(button);continue;
        }
        const group=document.createElement('div');group.className='notification-topic-filter';
        const expires=Date.parse(profile.topicMutes?.[topic]),muted=expires>now;
        if(muted)nextExpiry=Math.min(nextExpiry,expires);
        const {control,options}=this.muteControl(topic,muted?expires:null);
        if(topic===focusedTopic)focusedControl=control;
        group.append(button,control,options);this.filters.append(group);
      }
      focusedControl?.focus();
      if(Number.isFinite(nextExpiry))this.muteTimer=setTimeout(()=>this.renderFilters(),nextExpiry-now);
    }
    muteControl(topic,expires){
      const t=this.translate,control=document.createElement('button'),options=document.createElement('div');
      const current=expires?t('Stumm bis {time}').replace('{time}',this.formatMuteUntil(expires)):t('Stummschalten');
      control.type='button';control.className=expires?'topic-mute is-muted':'topic-mute';
      control.disabled=this.savingMutes.has(topic);
      control.title=current;
      control.setAttribute('data-topic-mute',topic);
      control.setAttribute('aria-label',expires?`${topic}, ${current}`:`${t('Push für Topic stummschalten')} ${topic}`);
      const svgNS='http://www.w3.org/2000/svg',bell=document.createElementNS(svgNS,'svg'),path=document.createElementNS(svgNS,'path');
      bell.setAttribute('viewBox','0 0 24 24');bell.setAttribute('aria-hidden','true');
      path.setAttribute('d',expires
        ?'M3 3l18 18M9.1 3.7A6 6 0 0 1 18 9c0 2 .37 3.55 1 4.8M6.26 6.26A6 6 0 0 0 6 9c0 5-3 6-3 8h14m-7 3a2 2 0 0 0 4 0'
        :'M18 9a6 6 0 0 0-12 0c0 5-3 6-3 8h18c0-2-3-3-3-8ZM10 20a2 2 0 0 0 4 0');
      bell.append(path);control.append(bell);

      options.id='topic-mute-options-'+topic;
      options.className='topic-mute-options';options.setAttribute('popover','auto');
      options.setAttribute('role','group');options.setAttribute('aria-label',`${topic}, ${current}`);
      control.setAttribute('popovertarget',options.id);
      options.addEventListener('beforetoggle',event=>{
        if(event.newState!=='open')return;
        const rect=control.getBoundingClientRect(),above=rect.top,below=window.innerHeight-rect.bottom;
        const openAbove=below<255 && above>below;
        options.style.left=`clamp(8px, ${rect.left}px, calc(100% - 12rem - 8px))`;
        options.style.top=openAbove?'auto':`${rect.bottom+4}px`;
        options.style.bottom=openAbove?`${window.innerHeight-rect.top+4}px`:'auto';
        options.style.maxHeight=`${Math.max(0,(openAbove?above:below)-12)}px`;
      });
      const choices=[['15m','15 Minuten'],['30m','30 Minuten'],['60m','60 Minuten'],['4h','4 Stunden'],['tomorrow','Bis morgen']];
      if(expires){
        const status=document.createElement('p');status.textContent=current;options.append(status);
        choices.unshift(['unmute','Stummschaltung aufheben']);
      }
      for(const [duration,label] of choices){
        const option=document.createElement('button');
        option.type='button';option.textContent=t(label);options.append(option);
        option.onclick=async()=>{
          if(this.savingMutes.has(topic))return;
          options.hidePopover();control.focus();
          this.savingMutes.set(topic,control);
          try{await this.onMute(topic,duration,control)}
          finally{
            if(this.savingMutes.get(topic)===control){this.savingMutes.delete(topic);this.renderFilters()}
          }
        };
      }
      return {control,options};
    }
    render(){
      this.renderFilters();
      const t=this.translate;
      this.list.replaceChildren();this.rows=[];
      const visible=this.visible();this.onFilter?.(visible,this.filter);
      if(!visible.length){
        const empty=document.createElement('p');empty.className='empty';
        empty.textContent=t(this.sent?'Noch keine gesendeten Mitteilungen.':'Noch keine Mitteilungen vorhanden.');this.list.append(empty);return;
      }
      for(const notification of visible)this.appendNotification(notification);
      this.scheduleLayout();
    }
    appendNotification(notification){
      const t=this.translate,expanded=this.expanded.has(notification.id),item=document.createElement('article');
      item.className='notification-row';item.id=(this.sent?'sent-notification-':'notification-')+notification.id;
      const dot=document.createElement('span');dot.className='notification-dot';
      if(!this.sent&&!notification.readAt){dot.classList.add('is-unread');dot.setAttribute('role','img');dot.setAttribute('aria-label',t('Ungelesen'))}
      else dot.setAttribute('aria-hidden','true');
      const content=document.createElement('div');content.className='notification-content';
      const heading=document.createElement('div'),title=document.createElement('b'),pill=document.createElement('span');
      heading.className='notification-heading';title.className='notification-title';title.textContent=notification.title;
      pill.className='notification-reason';pill.textContent=this.label(filterKey(notification));heading.append(title,pill);
      const preview=document.createElement('p'),text=document.createTextNode(notification.body||''),more=document.createElement('button');
      preview.className='notification-preview';preview.hidden=expanded;
      more.type='button';more.className='notification-more';more.textContent='… '+t('mehr');more.hidden=true;
      const fullID=(this.sent?'sent-details-':'notification-details-')+notification.id;
      more.setAttribute('aria-expanded',String(expanded));more.setAttribute('aria-controls',fullID);
      preview.append(text,more);
      const full=document.createElement('div'),body=document.createElement('p'),less=document.createElement('button');
      full.className='notification-full';full.id=fullID;full.hidden=!expanded;body.textContent=notification.body;full.append(body);
      if(notification.longText){const long=document.createElement('pre');long.className='notification-longtext';long.textContent=notification.longText;full.append(long)}
      less.type='button';less.className='notification-more';less.textContent=t('weniger');less.hidden=true;less.setAttribute('aria-expanded','true');less.setAttribute('aria-controls',fullID);full.append(less);
      const toggle=()=>{
        const opening=!this.expanded.has(notification.id);
        if(opening)this.expanded.add(notification.id);else this.expanded.delete(notification.id);
        this.render();
        if(opening&&!this.sent&&!this.getProfile()?.manualRead&&!notification.readAt)this.requestRead(notification,more);
      };
      more.onclick=toggle;less.onclick=toggle;content.append(heading,preview,full);
      const actions=document.createElement('div'),time=document.createElement('time'),buttons=document.createElement('div');
      actions.className='notification-actions';buttons.className='notification-buttons';time.dateTime=notification.createdAt;time.textContent=this.formatDate(notification.createdAt);
      if(!this.sent&&!notification.readAt){
        const read=document.createElement('button');read.type='button';read.className='small quiet';read.textContent=t('Gelesen');
        read.setAttribute('aria-label',t('Als gelesen markieren'));read.disabled=this.reading.has(notification.id);
        read.onclick=()=>this.requestRead(notification,read);buttons.append(read);
      }
      const remove=document.createElement('button');remove.type='button';remove.className='small quiet danger';remove.textContent=t('Löschen');
      remove.onclick=()=>this.onDelete(notification,remove);buttons.append(remove);actions.append(time,buttons);
      item.append(dot,content,actions);this.list.append(item);this.rows.push({notification,preview,text,more,full,less});
    }
    async requestRead(notification,button){
      if(this.reading.has(notification.id)||notification.readAt)return;
      this.reading.add(notification.id);
      try{await this.onRead(notification,button)}finally{this.reading.delete(notification.id);button.disabled=false}
    }
    scheduleLayout(){
      if(this.frame)cancelAnimationFrame(this.frame);
      this.frame=requestAnimationFrame(()=>{this.frame=null;for(const entry of this.rows)this.layoutPreview(entry)});
    }
    layoutPreview({notification,preview,text,more,full,less}){
      if(!preview.isConnected)return;
      const wasHidden=preview.hidden;
      preview.hidden=false;
      if(preview.clientWidth<1){preview.hidden=wasHidden;return}
      const style=getComputedStyle(preview),limit=2*(parseFloat(style.lineHeight)||parseFloat(style.fontSize)*1.5)+1;
      const body=notification.body||'';
      text.nodeValue=body;more.hidden=true;
      const expandable=!!notification.longText?.trim()||preview.scrollHeight>limit;
      less.hidden=!expandable;
      if(!expandable){
        this.expanded.delete(notification.id);full.hidden=true;
        return;
      }
      if(this.expanded.has(notification.id)){preview.hidden=true;full.hidden=false;return}
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
  window.PushieNotifications={NotificationList,filterKey,readFilter};
})();
