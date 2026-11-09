'use strict';
(() => {
  let account=null,socket=null,retry=null,refreshTimer=null,delay=1000;
  let refreshing=false,dirty=false,generation=0,cooldownUntil=0,failures=0;
  const failureLimit=3;
  function showState(state){
    const indicator=document.getElementById('syncIndicator');if(!indicator)return;
    const labels={connected:'Live-Sync verbunden',connecting:'Live-Sync verbindet …',disconnected:'Live-Sync getrennt',failed:'Live-Sync getrennt – zum Verbinden aktualisieren'};
    indicator.hidden=!account;indicator.dataset.state=state;
    const label=t(labels[state]);indicator.title=label;indicator.setAttribute('aria-label',label);
  }
  window.isLiveSyncConnected=()=>socket?.readyState===1;
  window.shouldPollInbox=()=>Boolean(account) && !document.hidden && !window.isLiveSyncConnected() && failures>=failureLimit;
  function connectionFailed(){
    failures++;
    showState(failures>=failureLimit?'failed':'connecting');
    scheduleReconnect();
  }
  function stop(){
    generation++;
    clearTimeout(retry);clearTimeout(refreshTimer);retry=null;refreshTimer=null;
    showState('disconnected');
    const previous=socket;socket=null;if(previous)previous.close();
  }
  async function update(){
    if(!account || document.hidden)return;
    if(Date.now()<cooldownUntil){scheduleUpdate();return}
    if(refreshing){dirty=true;return}
    refreshing=true;const current=generation;
    try{await refresh(false,true)}catch(error){
      if(error.retryAfterMs){cooldownUntil=Date.now()+error.retryAfterMs;dirty=true}
    }
    finally{
      refreshing=false;
      if(dirty && current===generation){dirty=false;scheduleUpdate()}
    }
  }
  function scheduleUpdate(){
    clearTimeout(refreshTimer);refreshTimer=setTimeout(update,Math.max(150,cooldownUntil-Date.now()));
  }
  function connect(){
    if(!account || document.hidden || socket || failures>=failureLimit)return;
    if(!('WebSocket' in window)){failures=failureLimit;showState('failed');return}
    clearTimeout(retry);retry=null;
    const url=new URL('/api/me/sync',location.href);url.protocol=url.protocol==='https:'?'wss:':'ws:';
    window.clientTrace?.socketURL(url);
    showState('connecting');
    let connection;
    try{connection=new WebSocket(url.href)}catch(_){connectionFailed();return}
    socket=connection;
    connection.onopen=()=>{if(socket!==connection)return;delay=1000;failures=0;showState('connected')};
    connection.onmessage=event=>{
      if(socket!==connection)return;
      try{if(JSON.parse(event.data).type==='sync')scheduleUpdate()}catch(_){}
    };
    connection.onerror=()=>connection.close();
    connection.onclose=()=>{if(socket!==connection)return;socket=null;connectionFailed()};
  }
  function scheduleReconnect(){
    if(!account || document.hidden || retry || failures>=failureLimit)return;
    retry=setTimeout(()=>{retry=null;connect()},delay+Math.random()*500);
    delay=Math.min(delay*2,30000);
  }
  window.updateLiveSync=id=>{
    if(account!==id){stop();account=id;delay=1000;dirty=false;failures=0}
    showState(socket?.readyState===1?'connected':failures>=failureLimit?'failed':socket?'connecting':'disconnected');
    connect();
  };
  window.retryLiveSync=()=>{stop();failures=0;delay=1000;connect()};
  document.addEventListener('visibilitychange',()=>{
    if(document.hidden)stop();else{showState(failures>=failureLimit?'failed':'connecting');connect()}
  });
  window.addEventListener('online',()=>{if(failures<failureLimit){stop();connect()}});
  window.addEventListener('pagehide',stop);
  window.addEventListener('pageshow',connect);
})();
