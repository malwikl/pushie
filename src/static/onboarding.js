'use strict';
(() => {
  function deviceInfo(nav,installed=false){
    const ua=nav.userAgent||'',platform=nav.userAgentData?.platform||nav.platform||'';
    const ipad=/iPad/i.test(ua)||(/Mac/i.test(platform)&&nav.maxTouchPoints>1);
    const ios=ipad||/iPhone|iPod/i.test(ua);
    const kind=ios?(ipad?'ipad':'iphone'):/Android/i.test(ua+' '+platform)?'android':/Mac/i.test(platform)?'mac':/Win/i.test(platform)?'windows':'other';
    const browser=/Edg/i.test(ua)?'Edge':/Firefox|FxiOS/i.test(ua)?'Firefox':/Chrome|CriOS/i.test(ua)?'Chrome':/Safari/i.test(ua)?'Safari':'Browser';
    return {kind,browser,installed,requiresInstall:ios&&!installed};
  }
  function suggestedName(info,translate,model=''){
    const fallback={iphone:'Mein iPhone',ipad:'Mein iPad',android:'Mein Android-Gerät',mac:'Mein Mac',windows:'Mein Windows-PC',other:'Mein Gerät'};
    return `${model.trim()||translate(fallback[info.kind])} · ${info.browser}`.slice(0,80);
  }
  window.PushieDevice={deviceInfo,suggestedName};
  window.createPushieOnboarding=({getProfile,getRegistration,register,api,translate:t})=>{
    const $=id=>document.getElementById(id),dialog=$('onboardingDialog');
    let profile=null,shownFor=null,step=1,installPrompt=null,busy=false,lastSuggested='',suggestionStarted=false;
    const installed=()=>!!navigator.standalone||!!window.matchMedia?.('(display-mode: standalone)').matches;
    const info=()=>deviceInfo(navigator,installed());
    function feedback(message,error=false){$('onboardingStatus').textContent=message;$('onboardingStatus').className=error?'error':'hint'}
    function suggest(model=''){
      const value=suggestedName(info(),t,model);
      for(const id of ['deviceName','onboardingDeviceName']){
        const input=$(id);
        if(!input.value||input.value===lastSuggested||['Mein Gerät','My device'].includes(input.value))input.value=value;
      }
      lastSuggested=value;
    }
    function instructions(){
      const device=info();let intro,steps=[];
      if(device.requiresInstall){
        intro='Auf iPhone und iPad ist für Push eine Home-Bildschirm-App erforderlich (iOS/iPadOS 16.4 oder neuer).';
        steps=['Öffne Pushie in Safari und öffne das Teilen-Menü.','Wähle Zum Home-Bildschirm. Falls angeboten, lasse Als Web-App öffnen aktiviert.','Tippe auf Hinzufügen und öffne Pushie über das neue Symbol. Melde dich bei Bedarf erneut an.'];
      }else if(device.installed){
        intro='Pushie ist bereits als App geöffnet. Du kannst jetzt Push für dieses Gerät aktivieren.';
      }else if(device.kind==='android'){
        intro='Auf Android funktioniert Push in unterstützten Browsern auch ohne Installation. Ein Home-Bildschirm-Symbol ist optional.';
        steps=['Öffne das Browser-Menü und wähle App installieren oder Zum Startbildschirm hinzufügen, falls angeboten.','Du kannst auch direkt fortfahren und Benachrichtigungen für diese Website erlauben.'];
      }else{
        intro='Auf Mac und Windows ist keine Installation für Push erforderlich. Verwende einen Browser mit Web-Push-Unterstützung.';
        steps=device.kind==='mac'&&device.browser==='Safari'?['Optional: Wähle in Safari Ablage → Zum Dock hinzufügen, falls verfügbar.']:['Optional: Verwende das Installieren-Symbol oder App installieren im Browser-Menü, falls angeboten.'];
      }
      $('onboardingInstallInfo').textContent=t(intro);$('onboardingInstructions').replaceChildren();
      for(const value of steps){const li=document.createElement('li');li.textContent=t(value);$('onboardingInstructions').append(li)}
      $('onboardingInstall').hidden=!installPrompt||device.requiresInstall||device.installed;
      $('onboardingNext').disabled=device.requiresInstall||busy;
    }
    function renderStep(){
      instructions();
      $('onboardingProgress').textContent=step===1?t('Schritt 1 von 2 · App öffnen'):t('Schritt 2 von 2 · Gerät registrieren');
      $('onboardingInstallStep').hidden=step!==1;$('onboardingRegisterStep').hidden=step!==2;
      $('onboardingNext').hidden=step!==1;$('onboardingRegister').hidden=step!==2;$('onboardingBack').hidden=step!==2;
      const ready=!!getRegistration()?.pushManager&&'PushManager' in window;
      $('onboardingRegister').disabled=busy||!ready||info().requiresInstall;
      $('onboardingLater').disabled=busy;$('onboardingBack').disabled=busy;
      if(step===2&&!busy){
        if(window.Notification?.permission==='denied')feedback(t('Benachrichtigungen sind blockiert. Erlaube sie in den Website- oder Systemeinstellungen und versuche es erneut.'));
        else if(!ready)feedback(t('Push ist noch nicht bereit oder wird in diesem Browser nicht unterstützt. Du kannst den Posteingang trotzdem verwenden.'));
        else if($('onboardingStatus').textContent===t('Push ist noch nicht bereit oder wird in diesem Browser nicht unterstützt. Du kannst den Posteingang trotzdem verwenden.'))feedback('');
      }
    }
    async function finish(){
      const current=getProfile();if(!current)return;
      await api('/api/me/settings','PUT',{onboardingComplete:true});
      if(getProfile()?.id!==current.id)return;
      getProfile().onboardingComplete=true;dialog.close();
    }
    function open(){
      if(!getProfile())return;
      profile=getProfile();shownFor=profile.id;step=1;feedback('');
      let currentID;try{currentID=localStorage.getItem('webpushDeviceId')}catch(_){}
      const current=profile.devices?.find(device=>device.id===currentID);
      if(current){$('onboardingDeviceName').value=current.name;$('deviceName').value=current.name}
      else suggest();
      renderStep();if(!dialog.open)dialog.showModal();
    }
    $('setupDevice').onclick=open;
    $('restartOnboarding').onclick=open;
    $('onboardingNext').onclick=()=>{step=2;feedback('');renderStep();$('onboardingDeviceName').focus()};
    $('onboardingBack').onclick=()=>{step=1;feedback('');renderStep()};
    $('onboardingLater').onclick=async()=>{
      busy=true;renderStep();try{await finish()}catch(error){feedback(error.message,true)}finally{busy=false;renderStep()}
    };
    dialog.addEventListener('cancel',event=>{event.preventDefault();if(!busy)$('onboardingLater').click()});
    $('onboardingRegister').onclick=async()=>{
      if(busy)return;
      const name=$('onboardingDeviceName').value.trim();
      if(!name){feedback(t('Gerätenamen eingeben'),true);$('onboardingDeviceName').focus();return}
      busy=true;renderStep();feedback('');
      try{
        // Invoke registration before awaiting anything else, preserving the permission gesture.
        await register(name);await finish();
      }catch(error){
        feedback(error.name==='NotAllowedError'?t('Benachrichtigungen sind blockiert. Erlaube sie in den Website- oder Systemeinstellungen und versuche es erneut.'):error.message,true);
      }finally{busy=false;renderStep()}
    };
    $('onboardingInstall').onclick=async()=>{
      const prompt=installPrompt;if(!prompt)return;
      installPrompt=null;$('onboardingInstall').hidden=true;
      try{await prompt.prompt();await prompt.userChoice;instructions()}catch(error){feedback(error.message,true)}
    };
    window.addEventListener('beforeinstallprompt',event=>{event.preventDefault();installPrompt=event;if(dialog.open)instructions()});
    window.addEventListener('appinstalled',()=>{installPrompt=null;if(dialog.open)instructions()});
    const result={
      open,
      update(next){
        if(!next){result.reset();return}
        profile=next;
        if(!suggestionStarted){
          suggestionStarted=true;suggest();
          navigator.userAgentData?.getHighEntropyValues?.(['model']).then(values=>{
            if(profile?.id===next.id&&values.model)suggest(values.model);
          }).catch(()=>{});
        }
        if(!next.onboardingComplete&&!next.devices?.length&&shownFor!==next.id)open();
        else if(dialog.open)renderStep();
      },
      reset(){
        profile=null;shownFor=null;step=1;busy=false;suggestionStarted=false;lastSuggested='';
        if(dialog.open)dialog.close();$('onboardingDeviceName').value='';feedback('');
      }
    };
    return result;
  };
})();
