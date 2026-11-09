'use strict';
const $ = id => document.getElementById(id);
let reg, vapidPublicKey, me = null, adminTargets = null, bootstrapAvailable = false;
let identityEpoch=0;
function status(message, error = false) { $('status').textContent = t(message); $('status').className = error ? 'error' : '';const modalStatus=$('sendDialogStatus');if(modalStatus){modalStatus.textContent=t(message);modalStatus.className=error?'error':'hint'} }
function b64ToBytes(value) {
  const str = atob(value.replace(/-/g,'+').replace(/_/g,'/').padEnd(Math.ceil(value.length / 4) * 4,'='));
  return Uint8Array.from(str, c => c.charCodeAt(0));
}
function bytesToB64(value) {
  const bytes = new Uint8Array(value);
  let bin = ''; for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
}
function creationOptions(options) {
  const json = options.publicKey || options;
  if (PublicKeyCredential.parseCreationOptionsFromJSON) return PublicKeyCredential.parseCreationOptionsFromJSON(json);
  return {...json,challenge:b64ToBytes(json.challenge),user:{...json.user,id:b64ToBytes(json.user.id)},
    excludeCredentials:(json.excludeCredentials||[]).map(c=>({...c,id:b64ToBytes(c.id)}))};
}
function requestOptions(options) {
  const json = options.publicKey || options;
  if (PublicKeyCredential.parseRequestOptionsFromJSON) return PublicKeyCredential.parseRequestOptionsFromJSON(json);
  return {...json,challenge:b64ToBytes(json.challenge),
    allowCredentials:(json.allowCredentials||[]).map(c=>({...c,id:b64ToBytes(c.id)}))};
}
function credentialJSON(credential) {
  if (credential.toJSON) return credential.toJSON();
  const response = {clientDataJSON:bytesToB64(credential.response.clientDataJSON)};
  if (credential.response.attestationObject) {
    response.attestationObject = bytesToB64(credential.response.attestationObject);
    response.transports = credential.response.getTransports?.() || [];
  } else {
    response.authenticatorData = bytesToB64(credential.response.authenticatorData);
    response.signature = bytesToB64(credential.response.signature);
    response.userHandle = credential.response.userHandle ? bytesToB64(credential.response.userHandle) : null;
  }
  return {id:credential.id,rawId:bytesToB64(credential.rawId),type:credential.type,response,clientExtensionResults:credential.getClientExtensionResults()};
}
async function api(path, method = 'GET', body) {
  if(window.authRefreshStopped && (path.startsWith('/api/me') || path.startsWith('/api/admin/')))throw new Error(t('Bitte anmelden'));
  const requestEpoch=identityEpoch;
  const res = await fetch(path,{method,credentials:'same-origin',cache:'no-store',
    headers:{...window.clientTrace?.headers(),...(body===undefined?{}:{'Content-Type':'application/json'})},
    body:body===undefined?undefined:JSON.stringify(body)});
  let data;try{data=await res.json()}catch{data={error:`HTTP ${res.status}`}}
  if(requestEpoch!==identityEpoch)throw new Error(t('Sitzung geändert.'));
  if(res.status===401){await expireSession();throw new Error(t('Bitte anmelden'))}
  if (!res.ok){
    const error=new Error(t(data.error) || `HTTP ${res.status}`);
    if(res.status===429)error.retryAfterMs=Math.max(1000,(Number(res.headers.get('Retry-After')) || 60)*1000);
    throw error;
  }
  return data;
}
async function ceremony(kind,invite) {
  const base = kind === 'invite' ? '/api/auth/register' : kind === 'bootstrap' ? '/api/auth/bootstrap' : kind === 'add' ? '/api/me/passkeys' : '/api/auth/login';
  const begin = await api(`${base}/begin`,'POST',kind === 'invite'?{invite}:kind === 'bootstrap'?invite:{});
  const options = begin.options;
  const credential = kind === 'login' ?
    await navigator.credentials.get({publicKey:requestOptions(options)}) :
    await navigator.credentials.create({publicKey:creationOptions(options)});
  if (!credential) throw new Error(t('Vorgang abgebrochen'));
  await api(`${base}/finish`,'POST',{ceremonyId:begin.ceremonyId,response:credentialJSON(credential)});
  await window.authCleanup;
  window.authRefreshStopped=false;
  await refresh();
}
function row() { const d=document.createElement('div');d.className='item';return d; }
function render() {
  window.updateLiveSync?.(me?.id || null);
  $('auth').hidden=!!me; $('account').hidden=!me; $('bootstrap').hidden=!bootstrapAvailable || !!me;
  $('adminPanel').hidden=true;
  for(const id of ['logout','messagesShortcut','devicesShortcut','topicsShortcut','accountShortcut'])$(id).hidden=!me;
  $('adminShortcut').hidden=me?.role!=='admin';$('openSend').hidden=me?.role!=='admin';
  const dialog=$('sendDialog');if(me?.role!=='admin' && dialog?.open)dialog.close();
  if (me) setupViews();
  if (!me) return;
  $('accountLanguage').value=me.language || language;
  $('accountTheme').value=me.theme || 'auto';
  $('topicCreate').hidden=me.role!=='admin';
  $('welcome').textContent=(language === 'en' ? `Hello, ${me.name}` : `Hallo, ${me.name}`);
  $('passkeyCount').textContent=(language === 'en' ? `${me.passkeyCount} passkey(s) registered. Add a second passkey as a backup if possible.` : `${me.passkeyCount} Passkey(s) hinterlegt. Registriere nach Möglichkeit einen zweiten Passkey als Reserve.`);
  const topics=$('topics');topics.replaceChildren();
  if (me.topics.length===0){const e=document.createElement('p');e.className='empty';e.textContent=t('Noch keine Topics vorhanden.');topics.appendChild(e)}
  for (const topic of me.topics) {
    const item=row(),label=document.createElement('label'),check=document.createElement('input'),detail=document.createElement('div'),name=document.createElement('b'),desc=document.createElement('span');
    check.type='checkbox';check.checked=me.subscriptions.includes(topic.name);
    name.textContent=topic.name;desc.textContent=topic.description || t('Benachrichtigungen');detail.append(name,desc);label.append(check,detail);item.append(label);topics.append(item);
    if(me.role==='admin'){
      const remove=document.createElement('button');remove.type='button';remove.className='small danger';remove.textContent=t('Löschen');remove.setAttribute('aria-label',`${t('Topic löschen')}: ${topic.name}`);item.append(remove);
      remove.onclick=()=>{if(!confirm(language==='en'?`Delete topic ${topic.name} and remove all subscriptions? Existing messages remain.`:`Topic ${topic.name} löschen und alle Abos entfernen? Vorhandene Nachrichten bleiben erhalten.`))return;run(remove,async()=>{
        await api(`/api/admin/topics/${encodeURIComponent(topic.name)}/web`,'DELETE',{});await refresh();status(t('Topic gelöscht.'));
      })};
    }
    check.addEventListener('change',async()=>{
      check.disabled=true;
      try{await api(`/api/me/topics/${encodeURIComponent(topic.name)}`,check.checked?'PUT':'DELETE',{});await refresh();status(check.checked?(language === 'en' ? `Subscribed to topic ${topic.name}.` : `Topic ${topic.name} abonniert.`):(language === 'en' ? `Unsubscribed from topic ${topic.name}.` : `Topic ${topic.name} abbestellt.`))}
      catch(e){check.checked=!check.checked;status(e.message,true)}finally{check.disabled=false}
    });
  }
  const devices=$('devices');devices.replaceChildren();
  if (!me.devices.length){const e=document.createElement('p');e.className='empty';e.textContent=t('Noch keine Geräte registriert.');devices.appendChild(e)}
  for(const dev of me.devices){
    const item=row(),text=document.createElement('div'),name=document.createElement('b'),note=document.createElement('span'),button=document.createElement('button');
    name.textContent=dev.name;note.textContent=new Date(dev.createdAt).toLocaleDateString(locale());button.textContent=t('Entfernen');button.className='small';
    text.append(name,note);item.append(text,button);devices.append(item);
    button.addEventListener('click',async()=>{
      button.disabled=true;
      try{await api(`/api/me/devices/${encodeURIComponent(dev.id)}`,'DELETE',{});
        if(localStorage.getItem('webpushDeviceId')===dev.id){await (await reg?.pushManager?.getSubscription())?.unsubscribe();localStorage.removeItem('webpushDeviceId')}
        await refresh();status(t('Gerät entfernt.'))}
      catch(e){status(e.message,true)}finally{button.disabled=false}
    });
  }
}
async function refresh(openUnread=false,liveOnly=false){
  if(window.authRefreshStopped)return;
  const requestEpoch=identityEpoch;
  const res=await fetch('/api/me',{credentials:'same-origin',cache:'no-store',headers:window.clientTrace?.headers()});
  if(requestEpoch!==identityEpoch)return;
  if(res.status===401){await expireSession();return;}
  if(!res.ok && res.status!==401){
    const error=new Error(res.status===429?t('Zu viele Anfragen'):`HTTP ${res.status}`);
    if(res.status===429)error.retryAfterMs=Math.max(1000,(Number(res.headers.get('Retry-After')) || 60)*1000);
    throw error;
  }
  const nextMe=res.ok?await res.json():null;
  if(requestEpoch!==identityEpoch)return;
  if(me?.id!==nextMe?.id)clearAccountState();
  me=nextMe;
  if(me)applyTheme(me.theme || 'auto');
  if(me?.language && me.language!==language){applyLanguage(me.language);return;}
  render();
  if(me){await refreshInbox({openUnread});if(liveOnly)return;await refreshApiKeys();if(me.role==='admin'){await refreshAdminTargets();await refreshStorageStats();await refreshUsers();await refreshIntegrationKeys()}}
}
async function refreshUsers(){
  if(me?.role!=='admin')return;
  const response=await api('/api/admin/users/web');
  const list=$('adminUsers');list.replaceChildren();
  for(const user of response.users){
    const item=row(),detail=document.createElement('div'),name=document.createElement('b'),meta=document.createElement('span'),actions=document.createElement('div');
    item.classList.add('user-row');actions.className='button-stack';
    name.textContent=user.name+(user.id===me.id ? (language==='en'?' (you)':' (du)'):'');
    meta.textContent=`${user.role==='admin'?'Admin':t('Benutzer')} · ${user.devices} ${t('Geräte')}`;
    detail.append(name,meta);item.append(detail,actions);
    if(user.role!=='admin'){
      const promote=document.createElement('button');promote.textContent=t('Zum Admin machen');promote.className='small';actions.append(promote);
      promote.onclick=()=>{if(!confirm(language==='en'?`Give ${user.name} full administrator access?`:`${user.name} volle Administratorrechte geben?`))return;run(promote,async()=>{
        await api(`/api/admin/users/${encodeURIComponent(user.id)}/promote/web`,'POST',{});await refreshUsers();status(t('Administratorrechte vergeben.'));
      })};
    }
    const remove=document.createElement('button');remove.textContent=t('Löschen');remove.className='small danger';remove.disabled=user.id===me.id;actions.append(remove);
    if(remove.disabled)remove.title=t('Das eigene Konto kann nicht gelöscht werden.');
    remove.onclick=()=>{if(!confirm(language==='en'?`Permanently delete ${user.name}, including their devices, passkeys, API keys and messages?`:`${user.name} einschließlich Geräten, Passkeys, API-Schlüsseln und Nachrichten endgültig löschen?`))return;run(remove,async()=>{
      await api(`/api/admin/users/${encodeURIComponent(user.id)}/web`,'DELETE',{});await refreshUsers();await refreshAdminTargets();await refreshStorageStats();status(t('Benutzer gelöscht.'));
    })};
    list.append(item);
  }
}
$('refreshUsers').onclick=()=>run($('refreshUsers'),refreshUsers);
async function refreshIntegrationKeys(){
 const response=await api('/api/admin/integration-keys');const list=$('integrationKeys');list.replaceChildren();
 for(const key of response.keys){
  const item=row(),detail=document.createElement('div'),name=document.createElement('b'),date=document.createElement('span'),remove=document.createElement('button');
  name.textContent=key.name;date.textContent=formatDate(key.createdAt);detail.append(name,date);remove.textContent=t('Widerrufen');remove.className='small danger';item.append(detail,remove);list.append(item);
  remove.onclick=()=>{if(!confirm(t('Integrationsschlüssel widerrufen?')))return;run(remove,async()=>{await api('/api/admin/integration-keys/'+encodeURIComponent(key.id),'DELETE',{});$('integrationKeySecret').value='';$('newIntegrationKey').hidden=true;await refreshIntegrationKeys();status(t('Integrationsschlüssel widerrufen.'))})};
 }
}
$('createIntegrationKey').onclick=()=>run($('createIntegrationKey'),async()=>{
 $('integrationKeySecret').value='';$('newIntegrationKey').hidden=true;
 const result=await api('/api/admin/integration-keys','POST',{name:$('integrationKeyName').value.trim()});
 $('integrationKeySecret').value=result.token;$('newIntegrationKey').hidden=false;$('integrationKeyName').value='';await refreshIntegrationKeys();
});
$('hideIntegrationKey').onclick=()=>{$('integrationKeySecret').value='';$('newIntegrationKey').hidden=true};
async function refreshApiKeys(){
  const response=await api('/api/me/keys');
  const list=$('apiKeys');list.replaceChildren();
  for(const key of response.keys){
    const item=row(),text=document.createElement('div'),name=document.createElement('b'),date=document.createElement('span'),button=document.createElement('button');
    name.textContent=key.name;date.textContent=formatDate(key.createdAt);text.append(name,date);
    button.textContent=t('Widerrufen');button.className='small';item.append(text,button);list.append(item);
    button.addEventListener('click',()=>run(button,async()=>{
      await api('/api/me/keys/'+encodeURIComponent(key.id),'DELETE',{});
      $('apiKeySecret').value='';$('newApiKey').hidden=true;
      await refreshApiKeys();status(t('API-Schlüssel widerrufen.'));
    }));
  }
}
$('createApiKey').addEventListener('click',()=>run($('createApiKey'),async()=>{
  $('apiKeySecret').value='';$('newApiKey').hidden=true;
  const result=await api('/api/me/keys','POST',{name:$('apiKeyName').value});
  $('apiKeySecret').value=result.token;$('newApiKey').hidden=false;$('apiKeyName').value='';
  await refreshApiKeys();
}));
$('hideApiKey').addEventListener('click',()=>{$('apiKeySecret').value='';$('newApiKey').hidden=true});
async function run(button,fn){button.disabled=true;status('');try{await fn()}catch(e){status(e.message||String(e),true)}finally{button.disabled=false}}
let activeMessageTab='received';
function setMessageTab(tab,{focus=false}={}){
  activeMessageTab=tab==='sent'?'sent':'received';
  for(const name of ['received','sent']){
    const selected=name===activeMessageTab,button=$(name+'Tab');
    button.setAttribute('aria-selected',String(selected));button.tabIndex=selected?0:-1;
    $(name+'Panel').hidden=!selected;
    if(selected && focus)button.focus();
  }
  $('refreshInbox').hidden=activeMessageTab!=='received';
  $('refreshSentMessages').hidden=activeMessageTab!=='sent';
  $('readAll').hidden=activeMessageTab!=='received';
}
for(const name of ['received','sent'])$(name+'Tab').addEventListener('click',()=>setMessageTab(name));
$('messageTabs').addEventListener('keydown',event=>{
  if(!['ArrowLeft','ArrowRight','Home','End'].includes(event.key))return;
  event.preventDefault();
  setMessageTab(event.key==='Home'?'received':event.key==='End'?'sent':activeMessageTab==='received'?'sent':'received',{focus:true});
});
$('cancelSend').addEventListener('click',()=>$('sendDialog').close());
let inboxMessages=[];
let sentMessages=[];
async function refreshSentMessages(){
  if(!me)return;
  const response=await api('/api/me/sent-messages');sentMessages=response.messages||[];
  renderSentMessages();
}
function messageContent(message){
  const content=document.createElement('div'),heading=document.createElement('div'),title=document.createElement('b'),time=document.createElement('time'),body=document.createElement('p'),meta=document.createElement('div');
  content.className='message-content';heading.className='message-heading';title.className='message-title';title.textContent=message.title;
  time.dateTime=message.createdAt;time.textContent=formatDate(message.createdAt);
  body.textContent=message.body;meta.className='meta';
  meta.textContent=message.topic?`Topic: ${message.topic}`:t('Direktnachricht');
  heading.append(time);content.append(heading,title,body,meta);
  if(message.longText){
    const details=document.createElement('details'),summary=document.createElement('summary'),full=document.createElement('pre');
    details.className='message-fulltext';summary.textContent=t('Vollständige Nachricht anzeigen');full.className='message-longtext';full.textContent=message.longText;
    details.append(summary,full);content.append(details);
  }
  return content;
}
function renderSentMessages(){
  const list=$('sentMessages');list.replaceChildren();$('cleanupSentMessages').disabled=!sentMessages.length;
  if(!sentMessages.length){const empty=document.createElement('p');empty.className='empty';empty.textContent=t('Noch keine gesendeten Nachrichten.');list.append(empty)}
  for(const message of sentMessages){
    const item=row();item.classList.add('message');item.append(messageContent(message));
    const remove=document.createElement('button');remove.type='button';remove.className='small danger';remove.textContent=t('Löschen');
    remove.onclick=()=>run(remove,async()=>{await api('/api/me/sent-messages/'+encodeURIComponent(message.id),'DELETE',{});await refreshSentMessages()});
    item.append(remove);list.append(item);
  }
}
$('refreshSentMessages').addEventListener('click',()=>run($('refreshSentMessages'),refreshSentMessages));
$('cleanupSentMessages').onclick=()=>{
  if(!confirm(t('Alle gesendeten Nachrichten aus deinem Verlauf löschen? Empfänger und Zustellungen bleiben erhalten.')))return;
  run($('cleanupSentMessages'),async()=>{await api('/api/me/sent-messages/cleanup','POST',{confirm:'LÖSCHEN'});await refreshSentMessages()});
};
let lastSentMessageId = null;
const deliveryLabels = {pending:"ausstehend",sending:"wird gesendet",retry:"Retry",accepted:"vom Push-Dienst angenommen",confirmed:"vom Browser bestätigt",unconfirmed:"nicht bestätigt",failed:"fehlgeschlagen"};
function formatDate(value){return new Date(value).toLocaleString(locale(),{dateStyle:'medium',timeStyle:'short'})}
function messageLogEntries(message){
  const label=(en,de)=>language==='en'?en:de;
  const events=[{at:message.createdAt,text:label('Message queued','Nachricht eingereiht')}];
  if(message.readAt)events.push({at:message.readAt,text:label('Marked as read','Als gelesen markiert')});
  for(const device of message.deliveries||[]){
    const name=device.deviceName || device.deviceId || t('Gerät');
    if(device.acceptedAt)events.push({at:device.acceptedAt,text:`${name}: ${t(deliveryLabels.accepted)}`});
    if(device.confirmedAt)events.push({at:device.confirmedAt,text:`${name}: ${t(deliveryLabels.confirmed)}`});
  }
  events.sort((a,b)=>new Date(a.at)-new Date(b.at));
  const lines=events.map(event=>`${new Date(event.at).toLocaleString(locale())} — ${event.text}`);
  lines.unshift(`${label('Message ID','Nachrichten-ID')}: ${message.id}`);
  for(const device of message.deliveries||[]){
    const name=device.deviceName || device.deviceId || t('Gerät');
    lines.push(`${name}: ${t(deliveryLabels[device.status] || device.status)} · ${device.attempts || 0} ${label('attempts','Versuche')}`);
    if(device.nextAttemptAt)lines.push(`${label('Next attempt','Nächster Versuch')}: ${new Date(device.nextAttemptAt).toLocaleString(locale())}`);
    if(device.lastError)lines.push(`${label('Last error','Letzter Fehler')}: ${device.lastError}`);
  }
  if(!message.deliveries?.length)lines.push(t('Keine Push-Geräte zum Versandzeitpunkt'));
  return lines;
}
async function retryPushReceipts(){
  if(!('serviceWorker' in navigator))return;
  const registration=await navigator.serviceWorker.getRegistration();
  if(!registration?.active)return;
  await new Promise(resolve=>{
    const channel=new MessageChannel();
    const finish=()=>{clearTimeout(timer);channel.port1.close();resolve()};
    const timer=setTimeout(finish,5000);
    channel.port1.onmessage=finish;
    registration.active.postMessage({type:'retry-push-receipts'},[channel.port2]);
  });
}
function renderInbox(){
  const list=$('inbox');list.replaceChildren();
  const unread=inboxMessages.filter(m=>!m.readAt).length;
  $('unreadBadge').textContent=unread?String(unread):'';
  $('unreadBadge').hidden=!unread;
  $('readAll').disabled=!unread;
  if(!inboxMessages.length){const p=document.createElement('p');p.className='empty';p.textContent=t('Noch keine Nachrichten vorhanden.');list.append(p);return}
  for(const m of inboxMessages){
    const item=row();item.id='message-'+m.id;item.classList.add('message');if(!m.readAt)item.classList.add('unread');
    const content=messageContent(m);
    const controls=document.createElement('div');controls.className='message-controls';
    const readState=document.createElement('span');readState.className='message-read-state';
    readState.textContent=m.readAt?(language==='en'?'Read':'Gelesen'):t('Ungelesen');
    content.querySelector('.message-heading').prepend(readState);
    if(!m.readAt){
      const read=document.createElement('button');read.type='button';read.className='small quiet';read.textContent=t('Als gelesen markieren');controls.append(read);
      let marking=false;
      const markRead=()=>{
        if(marking || m.readAt)return;
        marking=true;
        return run(read,async()=>{
          try{
            await api(`/api/me/messages/${encodeURIComponent(m.id)}/read`,'PUT',{});
            m.readAt=new Date().toISOString();
            item.classList.remove('unread');readState.textContent=language==='en'?'Read':'Gelesen';
            await refreshInbox();
          }finally{marking=false}
        });
      };
      read.addEventListener('click',markRead);
      content.addEventListener('click',event=>{
        // Expanding the message counts as reading; log and utility controls do not.
        if(event.target.closest('button, a, input, details:not(.message-fulltext)'))return;
        markRead();
      });
    }
    const del=document.createElement('button');del.className='small quiet danger';del.textContent=t('Löschen');controls.append(del);
    del.addEventListener('click',()=>run(del,async()=>{await api(`/api/me/messages/${encodeURIComponent(m.id)}`,'DELETE',{});await refreshInbox()}));
    item.append(content,controls);list.append(item);
  }
}
function linkedMessageId(){
  if(location.hash.startsWith('#message-')){
    try{return decodeURIComponent(location.hash.slice('#message-'.length))}catch{return null}
  }
  return new URLSearchParams(location.search).get('message');
}
async function readLinkedMessage(){
  if(!me)return null;
  const id=linkedMessageId();if(!id)return null;
  const message=inboxMessages.find(message=>message.id===id);
  if(!message)return null;
  if(!message.readAt){
    await api(`/api/me/messages/${encodeURIComponent(id)}/read`,'PUT',{});
    message.readAt=new Date().toISOString();
  }
  return id;
}
let inboxRefreshQueue=Promise.resolve(),lastOpenedMessage=null,lastOpenedMessageAt=0;
async function refreshInbox(options={}){
  // Push clicks, lifecycle events and polling must not render out of order.
  const requestEpoch=identityEpoch;
  const request=inboxRefreshQueue.catch(()=>{}).then(()=>{
    if(requestEpoch!==identityEpoch)return;
    return loadInbox(options);
  });
  inboxRefreshQueue=request;
  return request;
}
async function loadInbox(){
  if(!me)return;
  const response=await api('/api/me/messages');inboxMessages=response.messages||[];
  // Only explicit message links (including notification taps) mark a message read.
  const linkedId=await readLinkedMessage();
  // A newer tap may have arrived while the read request was in flight.
  if(linkedId && linkedMessageId()!==linkedId)return;
  if(linkedId){setMessageTab('received');if(resetViews)resetViews(true);}
  renderInbox();
  if(linkedId){
    const item=document.getElementById('message-'+linkedId);
    if(item){
      const dialog=$('sendDialog');if(dialog?.open)dialog.close();
      closeHeaderMenu();
      const details=item.querySelector('.message-fulltext');if(details)details.open=true;
      item.scrollIntoView({block:'start'});
      lastOpenedMessage=linkedId;lastOpenedMessageAt=Date.now();
    }
    // Consume only the link we handled; preserve query parameters and newer links.
    if(linkedMessageId()===linkedId){
      const params=new URLSearchParams(location.search);params.delete('message');
      history.replaceState(null,'',location.pathname+(params.size?'?'+params.toString():'')+'#inboxPanel');
    }
  }
  // Read state belongs to the account, so dismiss alerts read on any device.
  // Only close known read messages; an absent message may be outside the inbox limit.
  if(reg?.getNotifications){
    const readIds=new Set(inboxMessages.filter(m=>m.readAt).map(m=>m.id));
    try{
      for(const notification of await reg.getNotifications()){
        if(readIds.has(notification.data?.id))notification.close();
      }
    }catch(_){/* Notification cleanup must not prevent inbox access. */}
  }

  await refreshSentMessages();
}
async function refreshAdminTargets(){
  if(!me || me.role!=='admin')return;
  adminTargets=await api('/api/admin/targets');renderSendTargets();
}
function renderSendTargets(){
  if(!adminTargets)return;
  const select=$('sendTarget');select.replaceChildren();
  const kind=$('targetType').value;
  const items=kind==='broadcast'?[]:kind==='topic'?adminTargets.topics:adminTargets.users;
 select.hidden=kind==='broadcast';
  $('sendTargetField').hidden=kind==='broadcast';
  for(const item of items){const option=document.createElement('option');option.value=kind==='topic'?item.name:item.id;option.textContent=kind==='topic'?`${item.name}${item.description?' – '+item.description:''}`:item.name;select.append(option)}
  $('adminSend').disabled=kind!=='broadcast'&&!items.length;
 renderTargetDevices();
}
$('targetType').addEventListener('change',renderSendTargets);
$('refreshInbox').addEventListener('click',()=>run($('refreshInbox'),async()=>{window.retryLiveSync?.();await refreshInbox();status(t('Posteingang aktualisiert.'))}));
$('readAll').addEventListener('click',()=>run($('readAll'),async()=>{await api('/api/me/messages/read-all','POST',{});await refreshInbox();status(t('Alle Nachrichten als gelesen markiert.'))}));
$('bootstrapRegister').addEventListener('click',()=>run($('bootstrapRegister'),async()=>{
  const name=$('bootstrapName').value.trim(),code=$('bootstrapCode').value;
  if(!name||!code)throw new Error(t('Name und einmaligen Einrichtungscode eingeben'));
  await ceremony('bootstrap',{name,code});$('bootstrapCode').value='';bootstrapAvailable=false;render();status(t('Administrator mit Passkey eingerichtet.'));
}));
function resetSendContext(){
  lastSentMessageId=null;
  $('sendTitle').value='Push';$('sendBody').value='';$('sendLongText').value='';
  $('sendResult').textContent='';$('refreshDeliveryStatus').hidden=true;
  const modalStatus=$('sendDialogStatus');if(modalStatus)modalStatus.textContent='';
  $('sendMore').open=false;
  $('targetType').value='topic';renderSendTargets();
}
$('adminSend').addEventListener('click',()=>run($('adminSend'),async()=>{
  const title=$('sendTitle').value.trim()||'Push',body=$('sendBody').value.trim(),longText=$('sendLongText').value;
  const target=$('sendTarget').value,kind=$('targetType').value;
  if((kind!=='broadcast'&&!target)||!body)throw new Error(t('Empfänger und Nachricht eingeben'));
  const payload={title,body};if(longText)payload.longText=longText;if(kind==='broadcast')payload.broadcast=true;else payload[kind==='topic'?'topic':'userId']=target;
  if(kind==='user'&&!$('allDevices').checked){payload.deviceIds=[...$('targetDevices').querySelectorAll('input:checked')].map(x=>x.value);if(!payload.deviceIds.length)throw new Error(t('Geräte auswählen'));}
  await api('/api/admin/send','POST',payload);
  $('sendDialog').close();
  resetSendContext();
  await refreshInbox();setMessageTab('sent',{focus:true});status(t('Nachricht verarbeitet.'));
}));
$('refreshDeliveryStatus').addEventListener('click',()=>run($('refreshDeliveryStatus'),async()=>{
  if (!lastSentMessageId) return;
  const messageId=lastSentMessageId;
  const result=await api(`/api/admin/deliveries/${encodeURIComponent(messageId)}`);
  if(messageId!==lastSentMessageId)return;
  const text=(result.devices||[]).map(d => `${d.deviceName || d.deviceId}: ${t(deliveryLabels[d.status] || d.status)} (${d.attempts} ${language === 'en' ? 'attempts' : 'Versuche'})${d.lastError ? ` — ${d.lastError}` : ''}`).join(' · ');
  $('sendResult').textContent=(language === 'en' ? `Message ${result.messageId} · ${text || t('Keine Push-Geräte')} · ${result.inboxUsers} inboxes` : `Nachricht ${result.messageId} · ${text || t('Keine Push-Geräte')} · ${result.inboxUsers} Posteingänge`);
}));
$('createInvite').addEventListener('click',()=>run($('createInvite'),async()=>{
  const name=$('inviteName').value.trim();if(!name)throw new Error(t('Benutzernamen eingeben'));
  const result=await api('/api/admin/invites/web','POST',{name});
  $('inviteResult').hidden=false;$('inviteResult').textContent=(language === 'en' ? `Invitation for ${result.name} (valid for 24h): ${result.invite}` : `Einladung für ${result.name} (24h gültig): ${result.invite}`);
  $('inviteName').value='';status(t('Einladung erzeugt. Code einmalig sicher weitergeben.'));
}));
$('createTopic').addEventListener('click',()=>run($('createTopic'),async()=>{
  const name=$('topicName').value.trim(),description=$('topicDescription').value.trim();
  await api('/api/admin/topics/web','POST',{name,description});
  $('topicName').value='';$('topicDescription').value='';
  await refresh();status((language === 'en' ? `Topic ${name} created.` : `Topic ${name} angelegt.`));
}));
$('login').addEventListener('click',()=>run($('login'),async()=>{await ceremony('login');status(t('Angemeldet.'))}));
$('register').addEventListener('click',()=>run($('register'),async()=>{
  const invite=$('invite').value.trim();if(!invite)throw new Error(t('Einladungscode eingeben'));
  await ceremony('invite',invite);$('invite').value='';status(t('Konto mit Passkey angelegt.'));
}));
$('addPasskey').addEventListener('click',()=>run($('addPasskey'),async()=>{await ceremony('add');status(t('Weiterer Passkey hinzugefügt.'))}));
$('logout').addEventListener('click',()=>run($('logout'),async()=>{clearAccountState();me=null;render();await api('/api/auth/logout','POST',{});await refresh();status(t('Abgemeldet.'))}));
async function subscribeWithCurrentKey(pushManager,applicationServerKey){
 // Start immediately from the click handler to preserve the permission gesture.
 const options={userVisibleOnly:true,applicationServerKey};
 try{return await pushManager.subscribe(options)}catch(error){
  if(error.name!=='InvalidStateError')throw error;
  const existing=await pushManager.getSubscription();
  const oldKey=existing?.options?.applicationServerKey;
  if(!oldKey)throw error;
  const bytes=ArrayBuffer.isView(oldKey)?new Uint8Array(oldKey.buffer,oldKey.byteOffset,oldKey.byteLength):new Uint8Array(oldKey);
  const matches=bytes.length===applicationServerKey.length&&bytes.every((byte,index)=>byte===applicationServerKey[index]);
  if(matches)throw error;
  if(!await existing.unsubscribe())throw new Error(t('Alte Push-Registrierung konnte nicht entfernt werden. Bitte erneut versuchen.'));
  return pushManager.subscribe(options);
 }
}
$('subscribe').addEventListener('click',()=>run($('subscribe'),async()=>{
  if (!reg || !vapidPublicKey || !('PushManager' in window)) throw new Error(t('Web Push wird auf diesem Gerät nicht unterstützt. iOS: Home-Bildschirm-App verwenden.'));
  const name=$('deviceName').value.trim();if(!name)throw new Error(t('Gerätenamen eingeben'));
  // The permission prompt must be triggered as a consequence of user interaction.
  const subscription=await subscribeWithCurrentKey(reg.pushManager,b64ToBytes(vapidPublicKey));
  const result=await api('/api/me/devices','POST',{name,subscription:subscription.toJSON()});
  localStorage.setItem('webpushDeviceId',result.id);
  await refresh();status(t('Push ist für dieses Gerät aktiviert.'));
}));
function openNotificationMessage(id,{refresh=true}={}){
  if(typeof id!=='string' || !id || id.length>128)return;
  if(id===lastOpenedMessage && Date.now()-lastOpenedMessageAt<1500)return;
  const url=new URL(location.href);url.searchParams.set('message',id);url.hash='message-'+encodeURIComponent(id);
  history.replaceState(null,'',url.href);
  if(me && refresh){if(resetViews)resetViews(true);refreshInbox().catch(error=>status(error.message,true))}
}
let recoveringNotification=null;
function recoverNotificationMessage(){
  if(recoveringNotification)return recoveringNotification;
  recoveringNotification=(async()=>{
    try{
      const cache=await caches.open('pushie-navigation');
      const response=await cache.match('/__notification_target');
      if(!response)return;
      const target=await response.json();
      await cache.delete('/__notification_target');
      // Expire abandoned clicks rather than navigating on a later manual launch.
      if(target.createdAt<=Date.now() && Date.now()-target.createdAt<5*60*1000){
        // A native launch URL is more authoritative than an older cached tap.
        const id=linkedMessageId() || target.id;
        openNotificationMessage(id,{refresh:false});
        return id;
      }
    }catch(_){/* URL and live-message navigation still work without storage. */}
  })().finally(()=>{recoveringNotification=null});
  return recoveringNotification;
}
if('serviceWorker' in navigator)navigator.serviceWorker.addEventListener('message',event=>{
  if(event.data?.type!=='open-message')return;
  recoverNotificationMessage().then(recoveredId=>{
    // The durable target can be newer than a delayed worker message.
    openNotificationMessage(recoveredId || event.data.id);
  });
});
(async()=>{
  try{
    if (!('PublicKeyCredential' in window))throw new Error(t('Passkeys sind hier nicht verfügbar. HTTPS oder localhost verwenden.'));
    const config=await api('/api/config');vapidPublicKey=config.vapidPublicKey;bootstrapAvailable=!!config.bootstrapAvailable;
    // Inbox loading must not wait for service-worker activation (which can stall on iOS).
    if('serviceWorker' in navigator){
      navigator.serviceWorker.register('/sw.js',{updateViaCache:'none'}).then(registration=>{reg=registration})
        .catch(()=>{/* The inbox remains available when worker registration fails. */});
    }
    await recoverNotificationMessage();
    await refresh(true);
  }catch(e){status(e.message||String(e),true)}
})();

let foregroundCheck=null,lastForegroundCheck=0;
function checkMessagesOnOpen(){
  if(document.hidden || !me)return Promise.resolve();
  if(foregroundCheck)return foregroundCheck;
  if(Date.now()-lastForegroundCheck<1500)return Promise.resolve();
  lastForegroundCheck=Date.now();
  foregroundCheck=(async()=>{
    await retryPushReceipts();
    await recoverNotificationMessage();
    await refreshInbox();
  })().catch(error=>{lastForegroundCheck=0;status(error.message,true)})
    .finally(()=>{foregroundCheck=null});
  return foregroundCheck;
}
document.addEventListener('visibilitychange',()=>{if(!document.hidden)checkMessagesOnOpen()});
window.addEventListener('focus',checkMessagesOnOpen);
window.addEventListener('pageshow',checkMessagesOnOpen);
window.addEventListener('online',checkMessagesOnOpen);
setInterval(()=>{if(!document.hidden&&me&&window.shouldPollInbox?.())refreshInbox().catch(()=>{})},30000);

// SQLite administration: explicit preview and separate confirmation for destructive cleanup.
let storageCleanupPreview = null;
const readableBytes = value => {
  const number = Number(value) || 0;
  if (number < 1024) return `${number} B`;
  const unit = Math.min(Math.floor(Math.log(number)/Math.log(1024)), 4);
  return `${(number / Math.pow(1024, unit)).toLocaleString(locale(),{maximumFractionDigits:1})} ${['B','KiB','MiB','GiB','TiB'][unit]}`;
};
function statTile(name,value){
  const tile=document.createElement('div'),v=document.createElement('strong'),label=document.createElement('span');
  tile.className='stat-tile';v.textContent=String(value);label.textContent=name;tile.append(v,label);return tile;
}
async function refreshStorageStats(){
  if(!me || me.role!=='admin')return;
  const d=await api('/api/admin/storage/stats');
  const dest=$('storageStats');dest.replaceChildren();
  for(const [label,value] of [
    [t('Benutzer'),d.users],[t('Geräte'),d.devices],['Topics',d.topics],[t('Topic-Abos'),d.topicSubscriptions],
    [t('Nachrichten'),d.messages],[t('Ungelesen'),d.unreadMessages],[t('Push-Aufträge'),d.deliveries],
    [t('Offene Push-Aufträge'),(d.deliveryStatuses.pending||0)+(d.deliveryStatuses.retry||0)+(d.deliveryStatuses.sending||0)],
    [t('vom Push-Dienst angenommen'),(d.deliveryStatuses.accepted||0)+(d.deliveryStatuses.confirmed||0)],
    [t('Fehlgeschlagene Pushs'),d.deliveryStatuses.failed||0],[t('Langtexte'),readableBytes(d.longTextBytes)],
    ['DB + WAL + SHM',readableBytes(d.totalDiskBytes)],['WAL',readableBytes(d.walBytes)],
    [t('Freie DB-Seiten'),readableBytes(d.freePagesBytes)],[t('Einladungen'),d.invites],[t('Idempotenz-Keys'),d.idempotencyKeys]
  ])dest.append(statTile(label,value));
  dest.append(statTile(t('DB-Integrität'),d.integrity==='ok'?'OK':t('Prüfen')));
}
function cleanupSettings(){
  const messageDays=Number($('cleanupMessageDays').value),deliveryDays=Number($('cleanupDeliveryDays').value);
  if(!Number.isInteger(messageDays)||!Number.isInteger(deliveryDays)||messageDays<1||deliveryDays<1||messageDays>3650||deliveryDays>3650)
    throw new Error(t('Fristen müssen zwischen 1 und 3650 Tagen liegen.'));
  return {messageDays,deliveryDays,deleteUnread:$('cleanupUnread').checked};
}
function invalidateCleanupPreview(){
  storageCleanupPreview=null;$('cleanupPreview').hidden=true;
  $('cleanupConfirmation').value='';$('executeStorageCleanup').disabled=true;
}
for(const id of ['cleanupMessageDays','cleanupDeliveryDays','cleanupUnread'])
  $(id).addEventListener('change',invalidateCleanupPreview);
$('cleanupConfirmation').addEventListener('input',()=>{
  $('executeStorageCleanup').disabled=!storageCleanupPreview||$('cleanupConfirmation').value!=='LÖSCHEN';
});
$('refreshStorageStats').addEventListener('click',()=>run($('refreshStorageStats'),async()=>{
  await refreshStorageStats();status(t('Datenbankstatistiken aktualisiert.'));
}));
$('previewStorageCleanup').addEventListener('click',()=>run($('previewStorageCleanup'),async()=>{
  const opt=cleanupSettings();const result=await api('/api/admin/storage/preview','POST',opt);
  storageCleanupPreview=opt;
  $('cleanupPreview').hidden=false;
  $('cleanupPreview').textContent=(language === 'en' ? `Preview (nothing deleted):\n${result.messages} messages (${result.unreadMessages} unread), ${result.deliveries} completed push jobs, ${result.expiredInvites} expired invitations, ${result.expiredIdempotencyKeys} expired idempotency keys.\nLong texts: ${readableBytes(result.longTextBytes)}. Active deliveries are preserved.` : `Vorschau (noch nichts gelöscht):\n${result.messages} Nachrichten (davon ${result.unreadMessages} ungelesen), ${result.deliveries} abgeschlossene Push-Aufträge, ${result.expiredInvites} abgelaufene Einladungen, ${result.expiredIdempotencyKeys} abgelaufene Idempotenzschlüssel.\nEnthaltene Langtexte: ${readableBytes(result.longTextBytes)}. Laufende Zustellversuche werden nicht gelöscht.`);
  $('cleanupConfirmation').value='';$('executeStorageCleanup').disabled=true;
  status(t('Vorschau erstellt. Zum Ausführen LÖSCHEN eingeben.'));
}));
$('executeStorageCleanup').addEventListener('click',()=>run($('executeStorageCleanup'),async()=>{
  if(!storageCleanupPreview||$('cleanupConfirmation').value!=='LÖSCHEN')throw new Error(t('Neue Vorschau und Bestätigung erforderlich.'));
  // Browser confirmation is independent of the server-side confirmation.
  if(!window.confirm(t('Alte Datensätze endgültig löschen? Vorher eine Sicherung der SQLite-Datei anlegen.')))return;
  const result=await api('/api/admin/storage/cleanup','POST',{...storageCleanupPreview,confirm:'LÖSCHEN'});
  $('storageResult').textContent=(language === 'en' ? `Deleted: ${result.messages} messages, ${result.deliveries} completed deliveries, ${result.expiredInvites} invitations, ${result.expiredIdempotencyKeys} idempotency keys. Run VACUUM if needed to reclaim disk space.` : `Gelöscht: ${result.messages} Nachrichten, ${result.deliveries} abgeschlossene Zustellungen, ${result.expiredInvites} Einladungen, ${result.expiredIdempotencyKeys} Idempotenzschlüssel. Für Speicherplatzfreigabe bei Bedarf VACUUM ausführen.`);
  invalidateCleanupPreview();await refreshStorageStats();await refreshInbox();
  status(t('Datenbank bereinigt.'));
}));
$('compactStorage').addEventListener('click',()=>run($('compactStorage'),async()=>{
  if(!window.confirm(t('SQLite VACUUM ausführen? Der Vorgang sperrt während der Optimierung kurzzeitig Datenbankzugriffe und benötigt freien Speicherplatz.')))return;
  const result=await api('/api/admin/storage/compact','POST',{confirm:'OPTIMIEREN'});
  $('storageResult').textContent=(language === 'en' ? `Optimized: ${readableBytes(result.beforeBytes)} before VACUUM, ${readableBytes(result.afterBytes)} after.` : `Optimiert: ${readableBytes(result.beforeBytes)} vor VACUUM, ${readableBytes(result.afterBytes)} danach.`);
  await refreshStorageStats();status(t('SQLite optimiert.'));
}));

function renderTargetDevices(){
 const userMode=$('targetType').value==='user';$('deviceSelection').hidden=!userMode;
 setDevicePicker(false);$('allDevices').checked=true;const list=$('targetDevices');list.replaceChildren();
 for(const d of (adminTargets?.devices||[]).filter(d=>d.userId===$('sendTarget').value).sort((a,b)=>a.name.localeCompare(b.name))){const label=document.createElement('label'),input=document.createElement('input');input.type='checkbox';input.value=d.id;input.checked=true;label.append(input,document.createTextNode(d.name));list.append(label)}
 updateRecipientSummary();
}
function updateRecipientSummary(){
 const kind=$('targetType').value,devices=adminTargets?.devices||[];
 let users=kind==='broadcast'?adminTargets.users:kind==='topic'?null:[{id:$('sendTarget').value}];
 let count=kind==='broadcast'?devices.length:kind==='user'?$('allDevices').checked?devices.filter(d=>d.userId===$('sendTarget').value).length:$('targetDevices').querySelectorAll('input:checked').length:null;
 if(kind==='user'){
  const total=devices.filter(device=>device.userId===$('sendTarget').value).length;
  $('changeDevices').hidden=total<2;
  $('deviceSummary').textContent=total===0?t('Keine Push-Geräte · Nachricht wird im Posteingang gespeichert'):language==='en'?($('allDevices').checked?`All devices (${total})`:`${count} of ${total} devices`):($('allDevices').checked?`Alle Geräte (${total})`:`${count} von ${total} Geräten`);
 }
 $('recipientSummary').hidden=kind==='user';
 $('recipientSummary').textContent=users?(language === 'en' ? `${users.length} users · ${count} devices` : `${users.length} Benutzer · ${count} Geräte`):t('Alle Abonnenten des Topics');
 $('adminSend').disabled=kind==='user'&&(!$('sendTarget').value||(!$('allDevices').checked&&!count))||kind==='topic'&&!$('sendTarget').value;
}
$('sendTarget').addEventListener('change',renderTargetDevices);
function setDevicePicker(open){$('devicePicker').hidden=!open;$('changeDevices').setAttribute('aria-expanded',String(open))}
$('changeDevices').onclick=()=>setDevicePicker($('devicePicker').hidden);
$('doneDevices').onclick=()=>{setDevicePicker(false);$('changeDevices').focus()};
$('allDevices').addEventListener('change',()=>{for(const input of $('targetDevices').querySelectorAll('input'))input.checked=$('allDevices').checked;updateRecipientSummary()});
$('targetDevices').addEventListener('change',()=>{
 const inputs=[...$('targetDevices').querySelectorAll('input')];
 $('allDevices').checked=inputs.length>0&&inputs.every(input=>input.checked);updateRecipientSummary();
});
let viewsReady=false, resetViews=null;
function setupViews(){
 if(viewsReady){resetViews(false);return}viewsReady=true;
 const account=$('account'),sections=[...account.children],admin=$('adminPanel');
 const children=[...admin.children],invite=children.findIndex(n=>n.id==='inviteSection'),topic=children.findIndex(n=>n.id==='topicSection'),storage=children.findIndex(n=>n.id==='storageSection');
 const views=[];let activeView='messagesView';
 function view(id,nodes,adminOnly=false){
  const panel=document.createElement('section');panel.id=id;panel.tabIndex=-1;
  panel.append(...nodes);account.append(panel);views.push({panel,adminOnly});return panel;
 }
 function card(nodes){const el=document.createElement('div');el.className='panel';el.append(...nodes);return el}
 view('messagesView',[sections[1]]);
 view('accountView',[sections[0],sections[3]]);
 view('devicesView',[sections[2]]);
 const topicsCard=document.createElement('div');topicsCard.className='panel';
 const heading=document.createElement('h2');heading.textContent='Topics';
 const hint=document.createElement('p');hint.className='hint';hint.textContent=t('Topic-Abos gelten für alle Geräte deines Kontos.');
 const topicsList=document.createElement('div');topicsList.id='topics';topicsList.className='list';
 const create=document.createElement('div');create.id='topicCreate';create.append(...children.slice(topic,storage));
 topicsCard.append(heading,hint,topicsList,create);view('topicsView',[topicsCard]);
 view('administrationView',[createCentralLog(),card(children.slice(invite,topic)),card(children.slice(storage))],true);
 const dialog=document.createElement('dialog');dialog.id='sendDialog';dialog.setAttribute('aria-labelledby','sendDialogTitle');
 const headingRow=document.createElement('div');headingRow.className='heading-row';
 const title=document.createElement('h2');title.id='sendDialogTitle';title.textContent=t('Nachricht senden');
 const close=document.createElement('button');close.type='button';close.className='icon-button';close.textContent='×';close.setAttribute('aria-label',t('Schließen'));close.onclick=()=>dialog.close();
 headingRow.append(title,close);
 const modalStatus=document.createElement('p');modalStatus.id='sendDialogStatus';modalStatus.setAttribute('role','status');
 dialog.append(headingRow,...children.slice(2,invite),modalStatus);account.append(dialog);
 $('openSend').onclick=()=>{if(me?.role!=='admin')return;lastSentMessageId=null;$('sendResult').textContent='';$('refreshDeliveryStatus').hidden=true;renderTargetDevices();modalStatus.textContent='';dialog.showModal();refreshAdminTargets().catch(e=>status(e.message,true))};
 admin.hidden=true;
 function activate(id,focus=false){
  if(!me)return;
  const target=views.find(view=>view.panel.id===id);
  if(!target || (target.adminOnly && me.role!=='admin'))id='messagesView';
  activeView=id;
  for(const view of views)view.panel.hidden=view.panel.id!==id || (view.adminOnly && me.role!=='admin');
  for(const [button,view] of [['messagesShortcut','messagesView'],['devicesShortcut','devicesView'],['topicsShortcut','topicsView'],['accountShortcut','accountView'],['adminShortcut','administrationView']]){
   if(id===view)$(button).setAttribute('aria-current','page');else $(button).removeAttribute('aria-current');
  }
  if(id==='administrationView')refreshCentralLog().catch(error=>status(error.message,true));
  if(focus)$(id).focus();
 }
 for(const [button,id] of [['messagesShortcut','messagesView'],['devicesShortcut','devicesView'],['topicsShortcut','topicsView'],['accountShortcut','accountView'],['adminShortcut','administrationView']])$(button).onclick=()=>{closeHeaderMenu();activate(id,true)};
 resetViews=(reset=true)=>activate(reset?'messagesView':activeView);
 window.addEventListener('hashchange',()=>{if(location.hash.startsWith('#message-')||location.hash==='#inboxPanel'){activate('messagesView');refreshInbox().catch(error=>status(error.message,true))}});activate('messagesView');
}
function closeHeaderMenu(focus=false){$('headerMenu').hidden=true;$('menuToggle').setAttribute('aria-expanded','false');if(focus)$('menuToggle').focus()}
$('menuToggle').onclick=()=>{const open=$('headerMenu').hidden;$('headerMenu').hidden=!open;$('menuToggle').setAttribute('aria-expanded',String(open));if(open)$('headerMenu').querySelector('button:not([hidden]),select').focus()};
document.addEventListener('click',event=>{if(!event.target.closest('.header-menu'))closeHeaderMenu()});
document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!$('headerMenu').hidden){closeHeaderMenu(true);event.preventDefault()}});
$('headerMenu').addEventListener('keydown',event=>{
 if(event.target.tagName==='SELECT')return;
 if(!['ArrowDown','ArrowUp','Home','End'].includes(event.key))return;
 const items=[...$('headerMenu').querySelectorAll('button:not([hidden]),select')];
 const current=items.indexOf(document.activeElement);
 const next=event.key==='Home'?0:event.key==='End'?items.length-1:(current+(event.key==='ArrowUp'?-1:1)+items.length)%items.length;
 event.preventDefault();items[next]?.focus();
});
document.addEventListener('focusin',event=>{if(!event.target.closest('.header-menu'))closeHeaderMenu()});

async function setUiLanguage(value){
 if(!['de','en'].includes(value))return;
 if(me){await api('/api/me/settings','PUT',{language:value});me.language=value;}
 applyLanguage(value);
}
$('accountLanguage').addEventListener('change',()=>run($('accountLanguage'),()=>setUiLanguage($('accountLanguage').value)));

async function setThemePreference(theme){
 const previous=me?.theme || themePreference;
 try{
  if(me){const result=await api('/api/me/settings','PUT',{theme});me.theme=result.theme;}
  themeOverride=null;applyTheme(theme);$('accountTheme').value=theme;
 }catch(error){$('accountTheme').value=previous || 'auto';throw error;}
}
$('accountTheme').addEventListener('change',()=>run($('accountTheme'),()=>setThemePreference($('accountTheme').value)));
$('themeToggle').addEventListener('click',()=>{
 themeOverride=document.documentElement.dataset.theme==='dark'?'light':'dark';
 renderTheme();
});
renderTheme();

function clearAccountState(){
 window.updateLiveSync?.(null);
 identityEpoch++;
 const dialog=$('sendDialog');if(dialog?.open)dialog.close();
 closeHeaderMenu();
 if(resetViews)resetViews(true);
 setMessageTab('received');
 $('centralLogList')?.replaceChildren();
 $('centralLogFilters')?.reset();centralLogOffset=0;centralLogRequest++;
 if($('centralLogCount'))$('centralLogCount').textContent='';
 inboxMessages=[];sentMessages=[];$('sentMessages').replaceChildren();adminTargets=null;lastSentMessageId=null;storageCleanupPreview=null;
 for(const id of ['inbox','topics','devices','adminUsers','integrationKeys','storageStats','sendResult','sendDialogStatus','recipientSummary','inviteResult','cleanupPreview','storageResult','passkeyCount','welcome','unreadBadge','sendTarget','targetDevices','apiKeys'])$(id)?.replaceChildren();
 $('newApiKey').hidden=true;$('newIntegrationKey').hidden=true;
 for(const input of $('account').querySelectorAll('input[type="text"],input:not([type]),input[type="password"],textarea'))input.value='';
 $('deviceName').value=t('Mein Gerät');
 $('cleanupConfirmation').value='';$('executeStorageCleanup').disabled=true;
 $('inviteResult').hidden=true;$('cleanupPreview').hidden=true;$('refreshDeliveryStatus').hidden=true;
 $('bootstrapCode').value='';$('invite').value='';
}

async function expireSession(){
 if(window.authRefreshStopped)return window.authCleanup;
 window.authRefreshStopped=true;
 clearAccountState();me=null;render();
 // Stop queued work through identityEpoch before asynchronous cache deletion.
 window.authCleanup=(async()=>{
  try{if(typeof caches!=='undefined')await Promise.all((await caches.keys()).map(key=>caches.delete(key)))}catch(_){}
  try{sessionStorage.clear()}catch(_){}
 })();
 status(t('Bitte anmelden'),true);
 $('login').focus();
 return window.authCleanup;
}

let centralLogOffset=0,centralLogRequest=0;
const logLabel=(en,de)=>language==='en'?en:de;
function createCentralLog(){
 const panel=document.createElement('div');panel.className='panel';
 const heading=document.createElement('h2');heading.textContent=logLabel('Central message log','Zentrales Nachrichtenprotokoll');
 const hint=document.createElement('p');hint.className='hint';hint.textContent=logLabel('All sent messages, full text, senders, recipients and delivery details. Historical entries contain only data still available.','Alle gesendeten Nachrichten mit Langtext, Absendern, Empfängern und Zustelldetails. Historische Einträge enthalten nur noch verfügbare Daten.');
 const controls=document.createElement('div');controls.className='settings-grid';
 const enabledLabel=document.createElement('label'),enabled=document.createElement('input');enabled.type='checkbox';enabled.id='centralLogEnabled';enabledLabel.className='check-row';enabledLabel.append(enabled,document.createTextNode(logLabel('Write message log','Nachrichtenlog schreiben')));
 const save=document.createElement('button');save.type='button';save.textContent=logLabel('Save setting','Einstellung speichern');
 const feedback=document.createElement('p');feedback.setAttribute('role','status');
 save.onclick=()=>run(save,async()=>{await api('/api/admin/message-log/settings','PUT',{enabled:enabled.checked});feedback.textContent=enabled.checked?logLabel('Logging enabled.','Protokollierung eingeschaltet.'):logLabel('Logging disabled. Existing entries are preserved.','Protokollierung ausgeschaltet. Vorhandene Einträge bleiben erhalten.')});
 const daysLabel=document.createElement('label'),days=document.createElement('input');days.type='number';days.min='0';days.max='3650';days.value='30';daysLabel.textContent=logLabel('Delete log entries older than (days; 0 = all)','Logeinträge löschen, die älter sind als (Tage; 0 = alle)');daysLabel.append(days);
 const preview=document.createElement('button');preview.type='button';preview.textContent=logLabel('Preview cleanup','Bereinigung prüfen');
 const remove=document.createElement('button');remove.type='button';remove.className='danger';remove.disabled=true;remove.textContent=logLabel('Delete log entries','Logeinträge löschen');
 let previewDays=null;
 days.oninput=()=>{previewDays=null;remove.disabled=true;feedback.textContent=''};
 preview.onclick=()=>run(preview,async()=>{const value=Number(days.value);if(days.value===''||!Number.isInteger(value)||value<0||value>3650)throw new Error(logLabel('Enter 0–3650 days.','Bitte 0–3650 Tage eingeben.'));const result=await api('/api/admin/message-log/cleanup-preview','POST',{days:value});if(Number(days.value)!==value)return;previewDays=value;feedback.textContent=logLabel(`${result.entries} log entries will be deleted. Inboxes and deliveries remain unchanged.`,`${result.entries} Logeinträge werden gelöscht. Posteingänge und Zustellungen bleiben erhalten.`);remove.disabled=result.entries===0});
 remove.onclick=()=>run(remove,async()=>{if(previewDays===null||Number(days.value)!==previewDays)return;if(!window.confirm(logLabel('Delete the selected log entries permanently?','Die ausgewählten Logeinträge endgültig löschen?')))return;const result=await api('/api/admin/message-log/cleanup','POST',{days:previewDays,confirm:'LÖSCHEN'});previewDays=null;remove.disabled=true;feedback.textContent=logLabel(`${result.entries} log entries deleted.`,`${result.entries} Logeinträge gelöscht.`);centralLogOffset=0;await refreshCentralLog()});
 controls.append(enabledLabel,save,daysLabel,preview,remove,feedback);
 const form=document.createElement('form');form.id='centralLogFilters';form.className='log-filters';
 for(const [key,label,type] of [['q',logLabel('Search full text / ID','Volltext / ID suchen'),'search'],['sender',logLabel('From','Von'),'search'],['recipient',logLabel('To / topic','An / Topic'),'search'],['from',logLabel('From date (UTC)','Ab Datum (UTC)'),'date'],['to',logLabel('To date (UTC)','Bis Datum (UTC)'),'date']]){
  const wrapper=document.createElement('label'),input=document.createElement('input');wrapper.textContent=label;input.name=key;input.type=type;wrapper.append(input);form.append(wrapper);
 }
 const wrapper=document.createElement('label'),select=document.createElement('select');wrapper.textContent=logLabel('Delivery status','Zustellstatus');select.name='status';
 for(const state of ['',...Object.keys(deliveryLabels),'no_devices']){const option=document.createElement('option');option.value=state;option.textContent=state===''?logLabel('All','Alle'):state==='no_devices'?t('Keine Push-Geräte'):t(deliveryLabels[state]);select.append(option)}wrapper.append(select);form.append(wrapper);
 const apply=document.createElement('button');apply.type='submit';apply.textContent=logLabel('Filter / refresh','Filtern / aktualisieren');form.append(apply);
 const reset=document.createElement('button');reset.type='button';reset.className='quiet';reset.textContent=logLabel('Reset','Zurücksetzen');form.append(reset);
 form.onsubmit=event=>{event.preventDefault();centralLogOffset=0;run(apply,refreshCentralLog)};
 reset.onclick=()=>{form.reset();centralLogOffset=0;run(reset,refreshCentralLog)};
 const count=document.createElement('p');count.id='centralLogCount';count.setAttribute('role','status');
 const list=document.createElement('div');list.id='centralLogList';list.className='list';
 const pages=document.createElement('div');pages.className='actions';
 for(const [id,label,delta] of [['centralLogPrevious',logLabel('Previous','Zurück'),-50],['centralLogNext',logLabel('Next','Weiter'),50]]){
  const button=document.createElement('button');button.id=id;button.type='button';button.textContent=label;button.onclick=()=>run(button,async()=>{centralLogOffset=Math.max(0,centralLogOffset+delta);await refreshCentralLog()});pages.append(button);
 }
 panel.append(heading,hint,controls,form,count,list,pages);return panel;
}
async function refreshCentralLog(){
 if(me?.role!=='admin'||!$('centralLogFilters'))return;
 const sequence=++centralLogRequest;
 const query=new URLSearchParams(new FormData($('centralLogFilters')));query.set('offset',String(centralLogOffset));
 const [data,settings]=await Promise.all([api('/api/admin/message-log?'+query),api('/api/admin/message-log/settings')]);
 if(sequence!==centralLogRequest)return;
 $('centralLogEnabled').checked=settings.enabled;
 const list=$('centralLogList');list.replaceChildren();
 centralLogOffset=data.offset;
 $('centralLogCount').textContent=logLabel(`${data.total} messages · showing ${data.total?data.offset+1:0}–${data.offset+data.messages.length}`,`${data.total} Nachrichten · ${data.total?data.offset+1:0}–${data.offset+data.messages.length}`);
 $('centralLogPrevious').disabled=data.offset===0;$('centralLogNext').disabled=data.offset+data.messages.length>=data.total;
 for(const record of data.messages){
  const details=document.createElement('details');details.className='central-log-entry';
  const summary=document.createElement('summary');const n=record.notification;
  summary.textContent=`${formatDate(record.createdAt)} · ${n.title} · ${record.sender.name} → ${record.recipients.map(p=>p.name).join(', ')||record.target.topic||logLabel('No recipients','Keine Empfänger')}`;
  const content=document.createElement('div');details.append(summary,content);list.append(details);
  details.addEventListener('toggle',()=>{if(!details.open||content.childNodes.length)return;
   const loading=document.createElement('p');loading.textContent=logLabel('Loading…','Lädt…');content.append(loading);
   api('/api/admin/message-log/'+encodeURIComponent(n.id)).then(current=>{
    content.replaceChildren();const message=current.notification;
    const metadata=document.createElement('pre');metadata.className='message-longtext';
    const target=current.target;
    metadata.textContent=[`ID: ${message.id}`,`${logLabel('From','Von')}: ${current.sender.name} (${current.sender.id||current.source})`,`${logLabel('Source','Quelle')}: ${current.source}`,`${logLabel('To','An')}: ${current.recipients.map(p=>`${p.name} (${p.id})`).join(', ')||'—'}`,`Topic: ${target.topic||'—'}`,`${logLabel('Target','Ziel')}: ${target.broadcast?'Broadcast':target.deviceId||target.userId||target.topic||logLabel('Historical / unknown','Historisch / unbekannt')}`,`${logLabel('Selected devices','Ausgewählte Geräte')}: ${target.deviceIds?.join(', ')||'—'}`,`${logLabel('Device selection','Geräteermittlung')}: ${current.recovered?logLabel('Original selection unknown (historical entry).','Ursprüngliche Auswahl unbekannt (historischer Eintrag).'):target.broadcast?logLabel('All registered devices of all accounts.','Alle registrierten Geräte aller Konten.'):target.topic?logLabel('All registered devices of accounts subscribed to this topic.','Alle registrierten Geräte der Konten, die dieses Topic abonniert hatten.'):target.deviceId?logLabel('The explicitly targeted device.','Das explizit adressierte Gerät.'):target.deviceIds?.length?logLabel('Only the selected devices of the targeted account.','Nur die ausgewählten Geräte des adressierten Kontos.'):logLabel('All registered devices of the targeted account.','Alle registrierten Geräte des adressierten Kontos.')}`,current.recovered?'':logLabel('Recipients and devices were resolved when the message was accepted; retries use that same device set. Accounts without devices receive only an inbox message.','Empfänger und Geräte wurden bei Annahme der Nachricht ermittelt; Wiederholungen verwenden dieselben Geräte. Konten ohne Geräte erhalten nur eine Posteingangsnachricht.'),`Tag: ${message.tag||'—'}`].join('\n');
    const body=document.createElement('pre');body.className='message-longtext';body.textContent=message.body;
    content.append(metadata,body);
    if(message.longText){const title=document.createElement('h3'),full=document.createElement('pre');title.textContent=logLabel('Full text','Langtext');full.className='message-longtext';full.textContent=message.longText;content.append(title,full)}
    const delivery=document.createElement('pre');delivery.className='message-longtext';delivery.textContent=messageLogEntries({...message,createdAt:current.createdAt,deliveries:current.deliveries}).join('\n');content.append(delivery);
    if(current.recovered){const note=document.createElement('p');note.className='hint';note.textContent=logLabel('Recovered historical entry; original target or full text may be unavailable.','Wiederhergestellter historischer Eintrag; ursprüngliches Ziel oder Langtext möglicherweise nicht mehr verfügbar.');content.append(note)}
   }).catch(error=>{content.replaceChildren();const retry=document.createElement('button');retry.type='button';retry.textContent=error.message+' · '+logLabel('Retry','Erneut versuchen');retry.onclick=()=>{content.replaceChildren();details.open=false;details.open=true};content.append(retry)});
  });
 }
}
