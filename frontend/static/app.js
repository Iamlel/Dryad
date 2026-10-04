// The Dryad page: the selected plant's live readings and journal, QR scanning,
// talking to the plant, and the caretaker wallet.
'use strict';
const $ = id => document.getElementById(id);
const POLL_MS = 1500;
let plants = [], selected = '', generation = 0, timer, controller, listUpdated = 0;
let cameraStream, cameraTimer, scannerGeneration = 0;
const journals = new Map();
const number = v => typeof v === 'number' && Number.isFinite(v);
const text = (id, value) => { $(id).textContent = value; };
const plant = () => plants.find(p => p.id === selected);
const statuses = {ok:'In balance',thirsty:'Thirsty',overwatered:'Too much water',cold:'Too cold',hot:'Too warm',dark:'Needs light',offline:'Sensor offline'};
async function api(path, signal) {
  const r = await fetch(path, {cache:'no-store', signal});
  let data;
  try { data = await r.json(); } catch { throw Error('The server returned an unreadable response.'); }
  if (!r.ok) throw Error(data.error || `Request failed (${r.status})`);
  return data;
}
function notify(message, error=false) {
  text('connection',message);$('connection').classList.toggle('error',error);
}
function reset() {
  for (const key of ['moisture','light','temp']) {text(key,'—');$(key+'-meter').hidden=true;}
  text('environment','Waiting for readings');text('updated','No measurement received for this selection.');
  text('status-label','Waiting');text('plant-dialog','Waiting for the plant API.');text('report-time','');
  text('light-state','☀ Light —');text('water-state','◉ Soil —');text('temp-state','♨ Air —');
  document.body.classList.remove('night','dry','wet','hot','cold','stale');
}
function renderRoster() {
  $('plants').replaceChildren();
  for (const p of plants) {
    const b=document.createElement('button');b.dataset.id=p.id;b.classList.toggle('active',p.id===selected);
    b.setAttribute('aria-pressed',String(p.id===selected));
    const icon=document.createElement('span');icon.className='avatar';icon.textContent='❧';
    const label=document.createElement('span');label.textContent=p.name;
    const sub=document.createElement('small');sub.textContent='Woodland companion';label.append(sub);
    b.append(icon,label);b.onclick=()=>selectPlant(p.id);$('plants').append(b);
  }
}
function showPlant() {
  text('plant-name',plant().name);text('talk-name',plant().name);text('wallet-plant',plant().name);text('tagline','A woodland spirit watching over '+plant().name+'.');
  $('qr-link').href='/qr/'+encodeURIComponent(selected)+'.png';$('qr-link').hidden=false;
  $('ranges').replaceChildren();
  const p=plant(), range=(a,b,unit)=>number(a)&&number(b)?`${a}–${b}${unit}`:'Not provided';
  for(const [label,value] of [['Soil moisture',range(p.moisture_min_pct,p.moisture_max_pct,'%')],['Temperature',range(p.temp_min_c,p.temp_max_c,' °C')],['Minimum light',number(p.light_min_pct)?p.light_min_pct+'%':'Not provided']]) {
    const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=label;dd.textContent=value;$('ranges').append(dt,dd);
  }
}
function selectPlant(id) {
  if(!plants.some(p=>p.id===id))throw Error('This label does not match a plant in the list.');
  selected=id;reset();showPlant();renderRoster();drawCharts();
  history.replaceState(null,'','?plant='+encodeURIComponent(id));poll();
}
// API answers are checked before they're shown, so a bad one becomes an error message.
function validateState(s) {
  if (!s || typeof s!=='object' || !Object.hasOwn(statuses,s.status) || typeof s.stale!=='boolean' || typeof s.dialog!=='string') throw Error('Unexpected plant API response.');
  for(const key of ['moisture_pct','light_pct','temperature_c']) {
    if(s[key]!==null&&!number(s[key]))throw Error('Invalid sensor value from the API.');
  }
  for(const key of ['moisture_pct','light_pct'])if(number(s[key])&&(s[key]<0||s[key]>100))throw Error('Sensor percentage is outside 0–100.');
  if(s.updated_at!==null&&!Number.isFinite(Date.parse(s.updated_at)))throw Error('Invalid sensor timestamp.');
}
// The body classes set here (night, dry, wet, hot, cold, stale) restyle the page.
function renderState(s) {
  validateState(s);
  const p=plant(), stamp=Date.parse(s.updated_at), age=Date.now()-stamp;
  const stale=s.stale||s.status==='offline'||!Number.isFinite(stamp)||age>10000||age < -10000;
  const m=s.moisture_pct,l=s.light_pct,t=s.temperature_c;
  for (const [key,v] of [['moisture',m],['light',l],['temp',t]]) {
    text(key,number(v)?v.toFixed(1):'—');$(key+'-meter').hidden=!number(v);
    if(number(v))$(key+'-meter').value=v;
  }
  const dark=number(l)&&number(p.light_min_pct)&&l<p.light_min_pct;
  const dry=number(m)&&number(p.moisture_min_pct)&&m<p.moisture_min_pct;
  const wet=number(m)&&number(p.moisture_max_pct)&&m>p.moisture_max_pct;
  const hot=number(t)&&number(p.temp_max_c)&&t>p.temp_max_c;
  const cold=number(t)&&number(p.temp_min_c)&&t<p.temp_min_c;
  for(const [name,on] of Object.entries({night:dark,dry,wet,hot,cold,stale}))document.body.classList.toggle(name,on);
  text('environment',stale?'☾ Awaiting fresh data':!number(l)?'Light unavailable':dark?'☾ Moonlit grove':'☀ Sunlit grove');
  text('water-state',!number(m)?'◉ Soil —':dry?'◉ Dry soil':wet?'◉ Wet soil':'◉ Soil in range');
  text('light-state',!number(l)?'☀ Light —':dark?'☾ Low light':'☀ Light in range');
  text('temp-state',!number(t)?'♨ Air —':hot?'♨ Warm air':cold?'❄ Cool air':'♨ Air in range');
  text('status-label',statuses[s.status]);$('status-label').dataset.status=s.status;
  text('plant-dialog',s.dialog);text('report-time',stale?'This report is stale or offline.':'Current report from the plant API');
  text('updated',Number.isFinite(stamp)?'Measured '+new Date(stamp).toLocaleTimeString()+(stale?' · STALE':''):'Measurement time unavailable');
  notify(stale?'SENSOR OFFLINE / STALE · Displayed values are not current.':'LIVE · Polling every 1.5 seconds · Shared sensors, plant-specific care ranges',stale);
  text('mode',stale?'◌ Stale readings':'● Live readings');
  talkButton();
  if(!stale) {
    const list=journals.get(selected)||[];
    if(!list.length||stamp>Date.parse(list.at(-1).updated_at)) {list.push({...s});if(list.length>100)list.shift();journals.set(selected,list);}
  }
  drawCharts();
}
// The journal: this tab's last 100 fresh reports for the plant, as SVG charts.
function drawCharts() {
  const list=journals.get(selected)||[];const ns='http://www.w3.org/2000/svg';$('charts').replaceChildren();
  for(const [key,label,color] of [['moisture_pct','Soil moisture · %','#79a69c'],['light_pct','Light · %','#cfad58'],['temperature_c','Temperature · °C','#c68c72']]) {
    const row=document.createElement('div');row.className='chart-row';
    const heading=document.createElement('div');heading.className='chart-label';heading.textContent=label;row.append(heading);
    const svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 500 76');svg.setAttribute('role','img');svg.setAttribute('aria-label',label+'; '+list.filter(r=>number(r[key])).length+' measurements');
    const values=list.map(r=>r[key]).filter(number);
    if(values.length){
      const min=key==='temperature_c'?Math.floor(Math.min(...values)-2):0,max=key==='temperature_c'?Math.ceil(Math.max(...values)+2):100;
      const first=Date.parse(list[0].updated_at),last=Date.parse(list.at(-1).updated_at),range=Math.max(1,last-first);
      for(const [v,y] of [[max,12],[min,55]]){const tx=document.createElementNS(ns,'text');tx.setAttribute('x','0');tx.setAttribute('y',y);tx.textContent=v;svg.append(tx);const line=document.createElementNS(ns,'line');for(const [k,v] of Object.entries({x1:32,x2:495,y1:y-3,y2:y-3}))line.setAttribute(k,v);svg.append(line);}
      // Break lines across missing sensors and connection gaps; never draw null as zero.
      let segment=[];let prev;
      const flush=()=>{if(!segment.length)return;const e=document.createElementNS(ns,segment.length===1?'circle':'polyline');if(segment.length===1){e.setAttribute('cx',segment[0][0]);e.setAttribute('cy',segment[0][1]);e.setAttribute('r','3');e.setAttribute('fill',color);}else{e.setAttribute('points',segment.map(p=>p.join(',')).join(' '));e.setAttribute('fill','none');e.setAttribute('stroke',color);e.setAttribute('stroke-width','2.5');}svg.append(e);segment=[];};
      for(const r of list){const ts=Date.parse(r.updated_at);if(!number(r[key])||(prev&&ts-prev>10000))flush();if(number(r[key]))segment.push([32+463*(ts-first)/range,52-43*(r[key]-min)/(max-min)]);prev=ts;}flush();
    }else{const empty=document.createElementNS(ns,'text');empty.setAttribute('x',32);empty.setAttribute('y',35);empty.textContent='Waiting for fresh measurements';svg.append(empty);}
    row.append(svg);$('charts').append(row);
  }
  text('history-status',list.length?`${list.length} samples this session · ${new Date(list[0].updated_at).toLocaleTimeString()} – ${new Date(list.at(-1).updated_at).toLocaleTimeString()}`:'No fresh samples yet. History starts when measurements arrive.');
}
// Refreshes the plant list every 30 s and the plant every 1.5 s. generation drops
// answers to older requests.
async function poll() {
  clearTimeout(timer);controller?.abort();controller=new AbortController();const signal=controller.signal,g=++generation;
  const timeout=setTimeout(()=>controller?.signal===signal&&controller.abort(),8000);
  try {
    if(!plants.length||Date.now()-listUpdated>30000) {
      const list=await api('/api/plants',signal);if(g!==generation)return;
      if(!Array.isArray(list)||list.some(p=>!p||typeof p.id!=='string'||!/^[A-Za-z0-9_-]{1,64}$/.test(p.id)||typeof p.name!=='string'))throw Error('Unexpected plant list from the API.');
      plants=list;listUpdated=Date.now();
      if(!plants.some(p=>p.id===selected)) {
        const requested=new URLSearchParams(location.search).get('plant');
        selected=plants.some(p=>p.id===requested)?requested:plants[0]?.id||'';reset();
      }
      renderRoster();
      if(!selected){text('plant-name','your plant');text('tagline','No plants have been registered yet.');$('qr-link').hidden=true;$('ranges').replaceChildren();drawCharts();notify('No plants registered. Add a plant through your teammate’s app.');return;}
      showPlant();
    }
    const id=selected,s=await api('/api/plant/'+encodeURIComponent(id),signal);
    if(g!==generation||id!==selected)return;
    renderState(s);
  }catch(e){if(g===generation){document.body.classList.add('stale');text('mode','◌ Disconnected');text('environment','Connection unavailable');text('report-time','Connection lost: the last report may be outdated.');notify(e.name==='AbortError'?'Request timed out. Retrying automatically.':e.message,true);}}
  finally{clearTimeout(timeout);if(g===generation)timer=setTimeout(poll,POLL_MS);}
}
$('refresh').onclick=()=>{listUpdated=0;poll();pollCaretaker();};
// A label holds dryad:<id> (old ones groot:<id>), a raw id, {"plant_id": ...} or
// a URL with ?plant=<id>.
function qrID(raw){let v=raw.trim();if(v.startsWith('dryad:')||v.startsWith('groot:'))v=v.slice(6);else if(v.startsWith('{'))v=JSON.parse(v).plant_id;else if(/^https?:/.test(v))v=new URL(v).searchParams.get('plant');if(typeof v!=='string'||!/^[A-Za-z0-9_-]{1,64}$/.test(v))throw Error('Use a plant ID, dryad:ID, or a URL containing ?plant=ID.');return v;}
function closeCamera(){scannerGeneration++;clearTimeout(cameraTimer);cameraStream?.getTracks().forEach(t=>t.stop());cameraStream=null;$('camera').srcObject=null;}
$('scanner').addEventListener('close',closeCamera);$('close-scan').onclick=()=>$('scanner').close();
$('qr-form').onsubmit=e=>{e.preventDefault();try{selectPlant(qrID($('qr-input').value));$('scanner').close();}catch(err){text('scan-status',err.message);}};
// Scans with BarcodeDetector when the browser has it, otherwise jsQR.
// scannerGeneration stops the loop of a closed scanner.
$('scan').onclick=async()=>{$('scanner').showModal();text('scan-status','Starting camera…');const sg=++scannerGeneration;
 try{if(!navigator.mediaDevices?.getUserMedia)throw Error('Camera requires localhost or HTTPS. You can enter the QR text below.');
  const stream=await navigator.mediaDevices.getUserMedia({video:{facingMode:{ideal:'environment'}},audio:false});if(sg!==scannerGeneration){stream.getTracks().forEach(t=>t.stop());return;}cameraStream=stream;$('camera').srcObject=stream;await $('camera').play();
  let detector;if('BarcodeDetector'in window){const formats=await BarcodeDetector.getSupportedFormats();if(formats.includes('qr_code'))detector=new BarcodeDetector({formats:['qr_code']});}
  if(!detector&&!window.jsQR)throw Error('QR decoder unavailable. Enter the plant ID below.');
  text('scan-status','Camera ready. Hold a plant label inside the frame.');const canvas=document.createElement('canvas'),ctx=canvas.getContext('2d',{willReadFrequently:true});
  async function scan(){if(sg!==scannerGeneration)return;try{let raw;if(detector){const codes=await detector.detect($('camera'));raw=codes[0]?.rawValue;}else{canvas.width=$('camera').videoWidth;canvas.height=$('camera').videoHeight;if(canvas.width){ctx.drawImage($('camera'),0,0);const im=ctx.getImageData(0,0,canvas.width,canvas.height);raw=jsQR(im.data,im.width,im.height)?.data;}}if(raw){selectPlant(qrID(raw));$('scanner').close();return;}}catch(e){text('scan-status',e.message);}if(sg===scannerGeneration)cameraTimer=setTimeout(scan,250);}scan();
 }catch(e){if(sg===scannerGeneration){closeCamera();text('scan-status',e.message);}}};

// Talking to the plant. The board answers recordings in turn; the page polls for
// its answer (giving up after a while), then shows it and plays the voice.
const TALK_MAX_MS=20000,ANSWER_WAIT_MS=90000;
let recorder,talkTimer,player,waiting=false;
const pause=ms=>new Promise(r=>setTimeout(r,ms));
const sentence=s=>s.charAt(0).toUpperCase()+s.slice(1);
function talkButton(){
  const recording=recorder?.state==='recording';
  $('talk').disabled=!recording&&(waiting||!selected);
  $('talk').textContent=recording?'■ Stop and send':'🎙 Start talking';
}
function bubble(who,words,mine){
  const b=document.createElement('p');b.className='bubble'+(mine?' user':'');
  const name=document.createElement('strong');name.textContent=who;b.append(name,words);
  $('messages').append(b);while($('messages').children.length>6)$('messages').firstChild.remove();
  return b;
}
// Phones only play sound started by a tap, and the answer comes seconds later,
// so the tap that sends the recording unlocks the player with a moment of silence.
function unlockPlayer(){
  const n=800,wav=new DataView(new ArrayBuffer(44+n*2)),ascii=(at,s)=>[...s].forEach((c,i)=>wav.setUint8(at+i,c.charCodeAt(0)));
  ascii(0,'RIFF');wav.setUint32(4,36+n*2,true);ascii(8,'WAVEfmt ');wav.setUint32(16,16,true);wav.setUint16(20,1,true);wav.setUint16(22,1,true);
  wav.setUint32(24,8000,true);wav.setUint32(28,16000,true);wav.setUint16(32,2,true);wav.setUint16(34,16,true);ascii(36,'data');wav.setUint32(40,n*2,true);
  player=new Audio(URL.createObjectURL(new Blob([wav],{type:'audio/wav'})));player.play().catch(()=>{});
}
async function waitForAnswer(job,name){
  const until=Date.now()+ANSWER_WAIT_MS;
  while(Date.now()<until){
    const s=await api('/api/talk/jobs/'+job);
    if(s.status==='done')return s;
    if(s.status==='failed')throw Error(s.error);
    text('talk-status',s.status==='queued'&&s.position?`Waiting in line: ${s.position} ahead of you…`:'Listening and thinking…');
    await pause(1000);
  }
  throw Error(name+' took too long to answer. Try again.');
}
async function sendRecording(blob,id,voice){
  waiting=true;talkButton();text('talk-status','Sending…');
  const name=plants.find(p=>p.id===id)?.name||'Your plant';
  try{
    const r=await fetch('/api/talk/'+encodeURIComponent(id),{method:'POST',body:blob,headers:{'Content-Type':blob.type||'application/octet-stream'}});
    let data;try{data=await r.json();}catch{throw Error('The server returned an unreadable response.');}
    if(!r.ok)throw Error(data.error||`Request failed (${r.status})`);
    const s=await waitForAnswer(data.job,name);
    bubble('You',s.heard,true);const b=bubble(name,s.answer);
    if(!s.voice){text('talk-status',name+' couldn’t speak this time, but answered above.');return;}
    voice.src='/api/talk/jobs/'+data.job+'/voice';voice.controls=true;voice.style.cssText='display:block;max-width:100%;margin-top:8px';b.append(voice);
    voice.onended=()=>text('talk-status','Your turn: tap to talk.');
    text('talk-status',name+' is talking…');
    await voice.play().catch(()=>text('talk-status','Tap ▶ to hear '+name+'.'));
  }catch(e){text('talk-status',sentence(e.message));}
  finally{waiting=false;talkButton();}
}
$('talk').onclick=async()=>{
  if(recorder?.state==='recording'){unlockPlayer();recorder.stop();return;}
  try{
    if(!navigator.mediaDevices?.getUserMedia||!window.MediaRecorder)throw Error('Recording needs HTTPS (open the site on its domain) and a browser that can record audio.');
    const stream=await navigator.mediaDevices.getUserMedia({audio:true}),id=selected,chunks=[];
    recorder=new MediaRecorder(stream);
    recorder.ondataavailable=e=>{if(e.data.size)chunks.push(e.data);};
    recorder.onstop=()=>{
      clearTimeout(talkTimer);stream.getTracks().forEach(t=>t.stop());talkButton();
      const blob=new Blob(chunks,{type:recorder.mimeType||'audio/webm'}),voice=player||new Audio();player=null;
      if(blob.size)sendRecording(blob,id,voice);else text('talk-status','Nothing was recorded. Try again.');
    };
    recorder.start();talkTimer=setTimeout(()=>recorder.state==='recording'&&recorder.stop(),TALK_MAX_MS);
    talkButton();text('talk-status','Recording… tap again when you’re done (20 seconds at most).');
  }catch(e){text('talk-status',e.name==='NotAllowedError'?'Microphone permission was denied.':e.message);}
};

// Caretaker wallet. Watering a thirsty plant back into range earns it 0.01 test
// SOL on devnet; the rewards list refreshes every 10 seconds.
const CARETAKER_POLL_MS=10000;
let caretakerTimer,walletNote=null; // walletNote: the last save's result, shown until the next save
const shortWallet=w=>w.length>12?w.slice(0,4)+'…'+w.slice(-4):w;
const plantName=id=>plants.find(p=>p.id===id)?.name||id||'the default ranges';
function walletStatus(message,error=false){text('wallet-status',message);$('wallet-status').classList.toggle('wallet-error',error);}
function renderCaretaker(c){
  if(!c||typeof c!=='object'||typeof c.wallet!=='string'||!Array.isArray(c.payouts))throw Error('Unexpected caretaker response from the API.');
  const mine=c.payouts.filter(p=>p.wallet===c.wallet),earned=mine.reduce((sum,p)=>sum+(number(p.sol)?p.sol:0),0);
  $('current-caretaker').hidden=!c.wallet;
  text('current-caretaker',!c.wallet?'':`Current caretaker: ${shortWallet(c.wallet)} for ${plantName(c.plant_id)}`+(mine.length?` · earned ${+earned.toFixed(4)} test SOL from ${mine.length} watering${mine.length===1?'':'s'}`:''));
  $('payouts').replaceChildren();
  for(const p of c.payouts.slice(0,10)){
    const li=document.createElement('li'),what=document.createElement('strong'),when=document.createElement('span'),at=Date.parse(p.at);
    what.textContent=`${number(p.sol)?p.sol:'?'} test SOL to ${shortWallet(String(p.wallet))}`;
    when.textContent=Number.isFinite(at)?new Date(at).toLocaleString():'';li.append(what,when);
    // Only ever link to the Solana Explorer.
    if(typeof p.explorer_url==='string'&&p.explorer_url.startsWith('https://explorer.solana.com/tx/')){
      const a=document.createElement('a');a.href=p.explorer_url;a.target='_blank';a.rel='noopener';a.textContent='View on Solana Explorer ↗';li.append(a);
    }
    $('payouts').append(li);
  }
  if(!c.payouts.length){const li=document.createElement('li');li.textContent='No rewards yet. Water a thirsty plant back into its range to earn one.';$('payouts').append(li);}
}
async function pollCaretaker(){
  clearTimeout(caretakerTimer);
  try{
    const c=await api('/api/caretaker');renderCaretaker(c);
    if(walletNote&&!walletNote.error&&!c.wallet)walletNote=null; // the server restarted and forgot the wallet
    if(walletNote)walletStatus(walletNote.message,walletNote.error);
    else walletStatus(c.wallet?'Rewards are on.':'Rewards are on. Save a wallet to start earning.');
  }catch(e){walletStatus(sentence(e.message),true);}
  finally{caretakerTimer=setTimeout(pollCaretaker,CARETAKER_POLL_MS);}
}
$('wallet-form').onsubmit=async e=>{
  e.preventDefault();
  const wallet=$('wallet-address').value.trim(),id=selected;
  if(!id)walletNote={message:'Choose a plant first.',error:true};
  else if(!/^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(wallet))walletNote={message:'That doesn’t look like a Solana address. Paste the public address from your wallet app.',error:true};
  else{
    $('save-wallet').disabled=true;walletStatus('Saving…');
    try{
      const r=await fetch('/api/caretaker',{method:'POST',cache:'no-store',body:JSON.stringify({wallet,plant_id:id}),
        headers:{'Content-Type':'application/json','X-Dryad-Request':'caretaker'}});
      let data;try{data=await r.json();}catch{throw Error('The server returned an unreadable response.');}
      if(!r.ok)throw Error(data.error||`Request failed (${r.status})`);
      renderCaretaker(data);
      walletNote={message:`Saved. Water ${plantName(id)} when it’s thirsty to earn 0.01 test SOL.`,error:false};
    }catch(err){walletNote={message:sentence(err.message),error:true};}
    finally{$('save-wallet').disabled=false;}
  }
  walletStatus(walletNote.message,walletNote.error);
};

window.addEventListener('pagehide',()=>{closeCamera();controller?.abort();clearTimeout(timer);clearTimeout(caretakerTimer);if(recorder?.state==='recording')recorder.stop();});
poll();pollCaretaker();
