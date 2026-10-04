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
  text('plant-name',plant().name);text('tagline','A woodland spirit watching over '+plant().name+'.');
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
function validateState(s) {
  if (!s || typeof s!=='object' || !Object.hasOwn(statuses,s.status) || typeof s.stale!=='boolean' || typeof s.dialog!=='string') throw Error('Unexpected plant API response.');
  for(const key of ['moisture_pct','light_pct','temperature_c']) {
    if(s[key]!==null&&!number(s[key]))throw Error('Invalid sensor value from the API.');
  }
  for(const key of ['moisture_pct','light_pct'])if(number(s[key])&&(s[key]<0||s[key]>100))throw Error('Sensor percentage is outside 0–100.');
  if(s.updated_at!==null&&!Number.isFinite(Date.parse(s.updated_at)))throw Error('Invalid sensor timestamp.');
}
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
  if(!stale) {
    const list=journals.get(selected)||[];
    if(!list.length||stamp>Date.parse(list.at(-1).updated_at)) {list.push({...s});if(list.length>100)list.shift();journals.set(selected,list);}
  }
  drawCharts();
}
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
$('refresh').onclick=()=>{listUpdated=0;poll();};
function qrID(raw){let v=raw.trim();if(v.startsWith('dryad:')||v.startsWith('groot:'))v=v.slice(6);else if(v.startsWith('{'))v=JSON.parse(v).plant_id;else if(/^https?:/.test(v))v=new URL(v).searchParams.get('plant');if(typeof v!=='string'||!/^[A-Za-z0-9_-]{1,64}$/.test(v))throw Error('Use a plant ID, dryad:ID, or a URL containing ?plant=ID.');return v;}
function closeCamera(){scannerGeneration++;clearTimeout(cameraTimer);cameraStream?.getTracks().forEach(t=>t.stop());cameraStream=null;$('camera').srcObject=null;}
$('scanner').addEventListener('close',closeCamera);$('close-scan').onclick=()=>$('scanner').close();
$('qr-form').onsubmit=e=>{e.preventDefault();try{selectPlant(qrID($('qr-input').value));$('scanner').close();}catch(err){text('scan-status',err.message);}};
$('scan').onclick=async()=>{$('scanner').showModal();text('scan-status','Starting camera…');const sg=++scannerGeneration;
 try{if(!navigator.mediaDevices?.getUserMedia)throw Error('Camera requires localhost or HTTPS. You can enter the QR text below.');
  const stream=await navigator.mediaDevices.getUserMedia({video:{facingMode:{ideal:'environment'}},audio:false});if(sg!==scannerGeneration){stream.getTracks().forEach(t=>t.stop());return;}cameraStream=stream;$('camera').srcObject=stream;await $('camera').play();
  let detector;if('BarcodeDetector'in window){const formats=await BarcodeDetector.getSupportedFormats();if(formats.includes('qr_code'))detector=new BarcodeDetector({formats:['qr_code']});}
  if(!detector&&!window.jsQR)throw Error('QR decoder unavailable. Enter the plant ID below.');
  text('scan-status','Camera ready. Hold a plant label inside the frame.');const canvas=document.createElement('canvas'),ctx=canvas.getContext('2d',{willReadFrequently:true});
  async function scan(){if(sg!==scannerGeneration)return;try{let raw;if(detector){const codes=await detector.detect($('camera'));raw=codes[0]?.rawValue;}else{canvas.width=$('camera').videoWidth;canvas.height=$('camera').videoHeight;if(canvas.width){ctx.drawImage($('camera'),0,0);const im=ctx.getImageData(0,0,canvas.width,canvas.height);raw=jsQR(im.data,im.width,im.height)?.data;}}if(raw){selectPlant(qrID(raw));$('scanner').close();return;}}catch(e){text('scan-status',e.message);}if(sg===scannerGeneration)cameraTimer=setTimeout(scan,250);}scan();
 }catch(e){if(sg===scannerGeneration){closeCamera();text('scan-status',e.message);}}};

window.addEventListener('pagehide',()=>{closeCamera();controller?.abort();clearTimeout(timer);});
poll();
