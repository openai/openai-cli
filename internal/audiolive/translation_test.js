'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

// These stand-ins exercise browser control flow without permissions or external requests.
function page(options = {}) {
  const elements = new Map(), calls = [], contexts = [], nodes = [], blobs = new Map(), downloads = [];
  const tracks = [{kind:'audio',enabled:true,stopped:false,stop(){this.stopped=true;},addEventListener(){}}];
  if (options.noTabAudio) tracks[0].kind = 'video';
  const stream = {getTracks:()=>tracks,getAudioTracks:()=>tracks.filter(track=>track.kind==='audio')};
  let eventController, frame;
  const eventStream = new ReadableStream({start(controller){eventController=controller;}});
  class Element {
    constructor(id) { this.id=id;this.value='';this.checked=true;this.hidden=false;this.disabled=false;this.options=[];this.children=[];this.currentTime=0;this.paused=true;this.clientWidth=500; }
    get textContent(){return this.children.map(child=>child.textContent).join('');}
    set textContent(value){this.children=[{textContent:String(value)}];}
    append(child){this.children.push(child);}
    replaceChildren(...children){this.children=children;this.options=children;}
    add(child){this.options.push(child);}
    pause(){this.paused=true;}
    play(){this.paused=false;this.onplay?.();return Promise.resolve();}
    load(){}
    removeAttribute(name){delete this[name];}
    setAttribute(name,value){this[name]=value;}
    getBoundingClientRect(){return {left:0,width:500};}
    getContext(){return new Proxy({},{get:(_,key)=>key==='setTransform'||key==='clearRect'||key==='beginPath'||key==='roundRect'||key==='fill'||key==='fillRect'?()=>{}:undefined});}
    setSinkId(){return Promise.resolve();}
    click(){downloads.push({name:this.download,blob:blobs.get(this.href)});}
  }
  for(const id of ['status','time','source','language','microphone','output','listen','noise','start','mute','clear','original','translated','usage','download','transcript','playback','player','error','waveform','tab-hint']) elements.set(id,new Element(id));
  elements.get('source').value=options.tab?'tab':'microphone'; elements.get('language').value='en';
  elements.get('noise').checked=false;elements.get('player').hidden=true;
  class AudioContext {
    constructor(config){this.sampleRate=config?.sampleRate||48000;this.currentTime=0;this.state='running';this.destination={};this.audioWorklet={addModule:async()=>{}};contexts.push(this);}
    resume(){return Promise.resolve();}
    close(){this.state='closed';return Promise.resolve();}
    createGain(){return {gain:{value:1},connect(){}};}
    createMediaStreamSource(){return {connect(){},disconnect(){}};}
    createBuffer(channels,frames,rate){const values=Array.from({length:channels},()=>new Float32Array(frames));return {duration:frames/rate,getChannelData:index=>values[index]};}
    createBufferSource(){return {connect(){},start(){}};}
    setSinkId(){return Promise.resolve();}
  }
  class AudioWorkletNode {
    constructor(){nodes.push(this);this.port={postMessage:value=>{if(value==='flush')queueMicrotask(()=>this.port.onmessage({data:'flushed'}));}};}
    connect(){} disconnect(){}
  }
  const context = {
    console,AbortController,DOMException,TextDecoderStream,ReadableStream,Blob,Response,DataView,ArrayBuffer,Int16Array,Float32Array,Uint8Array,
    AudioContext,AudioWorkletNode,devicePixelRatio:1,Option:class{constructor(text,value){this.text=text;this.value=value;}},
    location:{hash:'#synthetic-local-test-token'},history:{replaceState(){context.location.hash='';}},
    document:{getElementById:id=>elements.get(id),documentElement:{},createTextNode:text=>({textContent:text}),createElement:()=>new Element('link')},
    navigator:{mediaDevices:{enumerateDevices:async()=>[],getUserMedia:async()=>{calls.push({kind:'microphone'});if(options.permission)throw new DOMException('denied','NotAllowedError');return options.capture?options.capture:stream;},getDisplayMedia:async()=>{calls.push({kind:'tab'});return stream;}}},
    fetch:async(url,config)=>{
      calls.push({url,config});
      if(url==='/start')return new Response(JSON.stringify({id:'synthetic-session'}));
      if(url.startsWith('/events')){config.signal.addEventListener('abort',()=>{try{eventController.error(new DOMException('aborted','AbortError'));}catch(_){}},{once:true});return new Response(eventStream);}
      if(url.startsWith('/stop')){emit({type:'session.closed'});eventController.close();}
      return new Response(null,{status:204});
    },
    URL:{createObjectURL(blob){const id='blob:synthetic-'+blobs.size;blobs.set(id,blob);return id;},revokeObjectURL(){}},
    getComputedStyle:()=>({getPropertyValue:()=> '#888'}),requestAnimationFrame:callback=>{frame=callback;},
    setTimeout:()=>1,clearTimeout(){},addEventListener(name,callback){if(name==='pagehide')this.pagehide=callback;},
    atob:value=>Buffer.from(value,'base64').toString('binary'),queueMicrotask,
  };
  context.window=context;
  function emit(event){eventController.enqueue(new TextEncoder().encode(JSON.stringify(event)+'\n'));}
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'translation.js'),'utf8'),context);
  return {elements,calls,contexts,nodes,tracks,downloads,emit,frame:()=>frame(),close:()=>context.pagehide?.()};
}
const settle = () => new Promise(resolve=>setImmediate(resolve));

test('microphone permission failures explain recovery without opening an API session',async()=>{
  const p=page({permission:true});await p.elements.get('start').onclick();
  assert.match(p.elements.get('error').textContent,/Allow microphone or tab audio access/);
  assert.equal(p.calls.filter(call=>call.url==='/start').length,0);p.close();
});

test('late capture permission cannot start a canceled session',async()=>{
  let release;
  const capture=new Promise(resolve=>{release=resolve;});const p=page({capture});
  const opening=p.elements.get('start').onclick();await settle();
  await p.elements.get('start').onclick();
  release({getTracks:()=>p.tracks,getAudioTracks:()=>p.tracks});await opening;
  assert.equal(p.calls.filter(call=>call.url==='/start').length,0);
  assert.equal(p.tracks[0].stopped,true);p.close();
});

test('tab capture requires a shared audio track',async()=>{
  const p=page({tab:true,noTabAudio:true});await p.elements.get('start').onclick();
  assert.match(p.elements.get('error').textContent,/enable Share tab audio/);
  assert.equal(p.calls.filter(call=>call.url==='/start').length,0);
  assert.equal(p.tracks[0].stopped,true);p.close();
});

test('tab capture sends complete PCM once and retains final translated output',async()=>{
  const p=page({tab:true});await p.elements.get('start').onclick();
  assert.equal(p.calls.some(call=>call.kind==='tab'),true);
  assert.equal(p.calls.some(call=>call.kind==='microphone'),false);
  const pcm=new Int16Array([1,-1,32767,-32768]);p.nodes[0].port.onmessage({data:pcm.buffer});await settle();
  const inputs=p.calls.filter(call=>call.url?.startsWith('/input'));
  assert.equal(inputs.length,1);assert.equal(inputs[0].url.includes('sequence=0'),true);
  assert.deepEqual(Buffer.from(inputs[0].config.body),Buffer.from(pcm.buffer));
  p.emit({type:'session.output_audio.delta',delta:Buffer.from(pcm.buffer).toString('base64'),sample_rate:48000,channels:2,format:'pcm16'});
  p.emit({type:'session.output_transcript.delta',delta:'Hello <script>literal</script>.'});await settle();
  await p.elements.get('start').onclick();await settle();
  assert.equal(p.elements.get('status').textContent,'Finished');
  assert.equal(p.elements.get('translated').textContent,'Hello <script>literal</script>.');
  assert.equal(p.tracks[0].stopped,true);
  p.elements.get('download').onclick();
  const saved=p.downloads[0],data=Buffer.from(await saved.blob.arrayBuffer());
  assert.equal(data.toString('ascii',0,4),'RIFF');assert.equal(data.readUInt16LE(22),2);
  assert.equal(data.readUInt32LE(24),48000);assert.equal(data.readUInt32LE(28),192000);
  assert.equal(data.readUInt32LE(40),pcm.byteLength);assert.deepEqual(data.subarray(44),Buffer.from(pcm.buffer));
  assert.match(saved.name,/^translation-hello-script-literal-script\.wav$/);p.close();
});

test('waveform seeking preserves audio and uses the actual duration',async()=>{
  const p=page();await p.elements.get('start').onclick();
  const pcm=Buffer.alloc(48000);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64'),sample_rate:24000,channels:1,format:'pcm16'});
  await settle();await p.elements.get('start').onclick();await settle();
  p.elements.get('waveform').onclick({clientX:250});
  assert.equal(p.elements.get('player').currentTime,.5);
  p.elements.get('waveform').onkeydown({key:'Home',preventDefault(){}});
  assert.equal(p.elements.get('player').currentTime,0);
  p.elements.get('waveform').onkeydown({key:'End',preventDefault(){}});
  assert.equal(p.elements.get('player').currentTime,1);
  p.elements.get('download').onclick();assert.equal((await p.downloads[0].blob.arrayBuffer()).byteLength,48044);p.close();
});

test('capture worklet downmixes, clips, and flushes the final partial block',()=>{
  let Processor;const sent=[];
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'capture-worklet.js'),'utf8'),{
    AudioWorkletProcessor:class{constructor(){this.port={postMessage:value=>sent.push(value)};}},
    Int16Array,Math,registerProcessor:(_,value)=>{Processor=value;},
  });
  const capture=new Processor();capture.process([[new Float32Array([2,-2,.5]),new Float32Array([2,-2,-.5])]]);
  capture.port.onmessage({data:'flush'});
  assert.deepEqual([...new Int16Array(sent[0])],[32767,-32768,0]);assert.equal(sent[1],'flushed');
  assert.equal(capture.process([[]]),false);
});
