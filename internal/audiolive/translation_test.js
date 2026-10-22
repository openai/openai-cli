'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

// These stand-ins exercise browser control flow without permissions or external requests.
function page(options = {}) {
  const elements = new Map(), calls = [], contexts = [], nodes = [], blobs = new Map(), downloads = [], timers = [];
  const tracks = [{kind:'audio',enabled:true,stopped:false,stop(){this.stopped=true;},addEventListener(){}}];
  if (options.noTabAudio) tracks[0].kind = 'video';
  const stream = {getTracks:()=>tracks,getAudioTracks:()=>tracks.filter(track=>track.kind==='audio')};
  let eventController, frame;
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
    constructor(config){this.output=!config;this.sampleRate=config?.sampleRate||48000;this.currentTime=0;this.state='running';this.destination={};this.audioWorklet={addModule:async()=>{}};contexts.push(this);}
    resume(){return Promise.resolve();}
    close(){this.state='closed';return this.output && options.closeOutput ? options.closeOutput : Promise.resolve();}
    createGain(){return {gain:{value:1},connect(){}};}
    createMediaStreamSource(){return {connect(){},disconnect(){}};}
    createBuffer(channels,frames,rate){if(options.bufferFail)throw new Error('Unsupported sample rate');const values=Array.from({length:channels},()=>new Float32Array(frames));return {duration:frames/rate,getChannelData:index=>values[index]};}
    createBufferSource(){return {connect(){},start(){if(options.playFail)throw new Error('Playback failed');}};}
    setSinkId(){return Promise.resolve();}
  }
  class AudioWorkletNode {
    constructor(){if(options.nodeFail)throw new Error('Capture setup failed.');nodes.push(this);this.port={postMessage:value=>{if(value==='flush' && !options.holdFlush)queueMicrotask(()=>{options.flush?.(this);this.port.onmessage({data:'flushed'});});}};}
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
      if(url==='/start')return options.failStart ? new Response('Your API key was not accepted.',{status:401}) : new Response(JSON.stringify({id:'synthetic-session'}));
      if(url.startsWith('/events')){const eventStream=new ReadableStream({start(controller){eventController=controller;}});const controller=eventController;config.signal.addEventListener('abort',()=>{try{controller.error(new DOMException('aborted','AbortError'));}catch(_){}},{once:true});return new Response(eventStream);}
      if(url.startsWith('/input') && options.input)return options.input(url,config);
      if(url.startsWith('/stop')){emit({type:'session.closed'});}
      return new Response(null,{status:204});
    },
    URL:{createObjectURL(blob){if(options.recordingFail)throw new Error('Could not prepare the recording.');const id='blob:synthetic-'+blobs.size;blobs.set(id,blob);return id;},revokeObjectURL(){}},
    getComputedStyle:()=>({getPropertyValue:()=> '#888'}),requestAnimationFrame:callback=>{frame=callback;},
    setTimeout:(callback,delay)=>{const timer={callback,delay,active:true};timers.push(timer);return timer;},clearTimeout(timer){timer.active=false;},addEventListener(name,callback){if(name==='pagehide')this.pagehide=callback;},
    atob:value=>Buffer.from(value,'base64').toString('binary'),queueMicrotask,
  };
  context.window=context;
  function emit(event){eventController.enqueue(new TextEncoder().encode(JSON.stringify(event)+'\n'));if(event.type==='session.closed')eventController.close();}
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'translation.js'),'utf8'),context);
  return {elements,calls,contexts,nodes,tracks,downloads,emit,timers,frame:()=>frame(),close:()=>context.pagehide?.()};
}
const settle = () => new Promise(resolve=>setImmediate(resolve));

function heldInputs(options) {
  const pending=[];
  options.input=(url,config)=>new Promise((resolve,reject)=>{
    const abort=()=>reject(new DOMException('aborted','AbortError'));
    if(!options.ignoreInputAbort)config.signal.addEventListener('abort',abort,{once:true});
    pending.push({url,config,resolve:()=>{config.signal.removeEventListener('abort',abort);resolve(new Response(null,{status:204}));},
      reject:()=>{config.signal.removeEventListener('abort',abort);reject(new Error('Late upload failure'));}});
  });
  return pending;
}

for (const bytes of [9600,2]) {
  test(`capture upload backlog interrupts at25 pending chunks: ${bytes} bytes`,async()=>{
    const options={},pending=heldInputs(options),p=page(options);await p.elements.get('start').onclick();
    for(let i=0;i<25;i++)p.nodes[0].port.onmessage({data:new ArrayBuffer(bytes)});
    await settle();assert.equal(pending.length,1);assert.equal(p.elements.get('error').textContent,'');
    p.nodes[0].port.onmessage({data:new ArrayBuffer(bytes)});await settle();
    assert.match(p.elements.get('error').textContent,/cannot keep up.*Start a new session/);
    assert.equal(p.elements.get('status').textContent,'Stopped');assert.equal(p.tracks[0].stopped,true);
    assert.equal(pending[0].config.signal.aborted,true);
    for(let i=0;i<100;i++)p.nodes[0].port.onmessage({data:new ArrayBuffer(bytes)});
    await settle();assert.equal(pending.length,1);p.close();
  });
}

test('capture upload bounds pending bytes without a total-session limit',async()=>{
  const options={},pending=heldInputs(options),p=page(options);await p.elements.get('start').onclick();
  for(let i=0;i<30;i++){
    p.nodes[0].port.onmessage({data:new ArrayBuffer(9600)});await settle();
    assert.equal(pending.length,i+1);pending[i].resolve();await settle();
  }
  assert.equal(p.elements.get('status').textContent,'Translating');
  p.nodes[0].port.onmessage({data:new ArrayBuffer(240000)});await settle();
  assert.equal(pending.length,31);assert.equal(p.elements.get('error').textContent,'');
  p.nodes[0].port.onmessage({data:new ArrayBuffer(2)});await settle();
  assert.match(p.elements.get('error').textContent,/cannot keep up/);
  assert.equal(pending[30].config.signal.aborted,true);p.close();
});

test('capture upload drains in order including the final partial block before Stop',async()=>{
  const options={},pending=heldInputs(options),p=page(options);await p.elements.get('start').onclick();
  const buffers=[new Int16Array([1,-1]).buffer,new Int16Array([2,-2]).buffer,new Int16Array([3]).buffer];
  for(const data of buffers.slice(0,2))p.nodes[0].port.onmessage({data});
  options.flush=node=>node.port.onmessage({data:buffers[2]});
  const stopping=p.elements.get('start').onclick();await settle();
  assert.equal(p.tracks[0].stopped,true);assert.equal(pending.length,1);
  for(let i=0;i<buffers.length;i++){
    assert.equal(p.calls.filter(call=>call.url?.startsWith('/stop')).length,0);
    assert.equal(pending.length,i+1);assert.ok(pending[i].url.endsWith('sequence='+i));
    assert.deepEqual(Buffer.from(pending[i].config.body),Buffer.from(buffers[i]));
    pending[i].resolve();await settle();
  }
  await stopping;assert.equal(p.calls.filter(call=>call.url?.startsWith('/stop')).length,1);
  assert.equal(p.timers.some(timer=>timer.active),false);p.close();
});

test('capture upload Stop interrupts a stalled drain and keeps received audio',async()=>{
  const options={},pending=heldInputs(options),p=page(options);await p.elements.get('start').onclick();
  const pcm=Buffer.from(new Int16Array([12,-12]).buffer);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});
  p.emit({type:'session.output_transcript.delta',delta:'Received translation.'});await settle();
  for(let i=0;i<4;i++)p.nodes[0].port.onmessage({data:new ArrayBuffer(9600)});
  const stopping=p.elements.get('start').onclick();await settle();
  assert.equal(p.elements.get('status').textContent,'Finishing translation');
  assert.equal(p.tracks[0].stopped,true);assert.equal(pending.length,1);
  const timer=p.timers.find(timer=>timer.active && timer.delay===2000);assert.ok(timer,'Stop needs a bounded upload drain');
  timer.callback();await stopping;await settle();
  assert.equal(pending[0].config.signal.aborted,true);assert.equal(pending.length,1);
  assert.match(p.elements.get('error').textContent,/final input may be incomplete/);
  assert.equal(p.elements.get('status').textContent,'Stopped');assert.equal(timer.active,false);
  assert.equal(p.elements.get('translated').textContent,'Received translation.');
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);p.close();
});

test('capture upload interruption restores prior output and stale failure cannot affect retry',async()=>{
  const options={},p=page(options);await p.elements.get('start').onclick();
  const pcm=Buffer.alloc(48000);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});
  p.emit({type:'session.output_transcript.delta',delta:'Previous translation.'});await settle();
  await p.elements.get('start').onclick();await settle();p.elements.get('waveform').onclick({clientX:250});
  options.ignoreInputAbort=true;const pending=heldInputs(options);
  await p.elements.get('start').onclick();
  for(let i=0;i<25;i++)p.nodes[1].port.onmessage({data:new ArrayBuffer(9600)});
  await settle();p.nodes[1].port.onmessage({data:new ArrayBuffer(9600)});
  await settle();assert.equal(p.elements.get('status').textContent,'Stopped');
  assert.equal(p.elements.get('translated').textContent,'Previous translation.');
  assert.equal(p.elements.get('player').currentTime,.5);
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);
  options.input=null;await p.elements.get('start').onclick();
  pending[0].reject();await settle();
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.elements.get('error').textContent,'');
  p.nodes[2].port.onmessage({data:new Int16Array([7]).buffer});await settle();
  const inputs=p.calls.filter(call=>call.url?.startsWith('/input'));
  assert.equal(inputs.length,2);assert.ok(inputs[1].url.endsWith('sequence=0'));
  assert.equal(p.timers.some(timer=>timer.active && (timer.delay===500 || timer.delay===2000)),false);p.close();
});

test('capture upload Stop now clears its drain timer before another session',async()=>{
  const options={},pending=heldInputs(options),p=page(options);await p.elements.get('start').onclick();
  p.nodes[0].port.onmessage({data:new ArrayBuffer(9600)});
  const stopping=p.elements.get('start').onclick();await settle();
  const timer=p.timers.find(timer=>timer.active && timer.delay===2000);assert.ok(timer);
  await p.elements.get('start').onclick();await stopping;await settle();
  assert.equal(timer.active,false);assert.equal(pending[0].config.signal.aborted,true);
  options.input=null;await p.elements.get('start').onclick();
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.elements.get('error').textContent,'');p.close();
});

test('capture upload repeated retries reset the pending budget and sequence',async()=>{
  const options={},pending=heldInputs(options),p=page(options);
  for(let round=0;round<3;round++){
    await p.elements.get('start').onclick();
    for(let i=0;i<25;i++)p.nodes[round].port.onmessage({data:new ArrayBuffer(9600)});
    await settle();assert.equal(pending.length,round+1);assert.ok(pending[round].url.endsWith('sequence=0'));
    p.nodes[round].port.onmessage({data:new ArrayBuffer(9600)});await settle();
    assert.equal(pending[round].config.signal.aborted,true);assert.equal(p.elements.get('status').textContent,'Stopped');
  }
  assert.equal(new Set(pending.map(input=>input.config.signal)).size,3);
  options.input=null;await p.elements.get('start').onclick();
  p.nodes[3].port.onmessage({data:new Int16Array([9]).buffer});await settle();
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.elements.get('error').textContent,'');
  await p.elements.get('start').onclick();await settle();
  assert.equal(p.timers.some(timer=>timer.active),false);p.close();
});

test('capture upload Stop now clears a pending flush and ignores its late acknowledgement',async()=>{
  const options={holdFlush:true},p=page(options);await p.elements.get('start').onclick();
  const stopping=p.elements.get('start').onclick();await settle();
  const timer=p.timers.find(timer=>timer.active && timer.delay===500);assert.ok(timer);
  await p.elements.get('start').onclick();await stopping;await settle();
  assert.equal(timer.active,false);assert.equal(p.elements.get('status').textContent,'Stopped');
  options.holdFlush=false;await p.elements.get('start').onclick();
  p.nodes[0].port.onmessage({data:'flushed'});await settle();
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.elements.get('error').textContent,'');p.close();
});

test('capture upload completion releases stalled acknowledgements without replacing Finished',async()=>{
  const options={ignoreInputAbort:true},pending=heldInputs(options),p=page(options);await p.elements.get('start').onclick();
  const pcm=Buffer.from(new Int16Array([33,-33]).buffer);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});await settle();
  p.nodes[0].port.onmessage({data:new ArrayBuffer(9600)});
  const stopping=p.elements.get('start').onclick();await settle();
  const timer=p.timers.find(timer=>timer.active && timer.delay===2000);assert.ok(timer);
  p.emit({type:'session.closed'});await settle();await stopping;
  assert.equal(timer.active,false);assert.equal(pending[0].config.signal.aborted,true);
  assert.equal(p.elements.get('status').textContent,'Finished');assert.equal(p.elements.get('error').textContent,'');
  pending[0].resolve();await settle();assert.equal(p.elements.get('status').textContent,'Finished');
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);p.close();
});

test('capture upload completion still reports recording preparation failures',async()=>{
  const options={},p=page(options);await p.elements.get('start').onclick();
  const pcm=Buffer.from(new Int16Array([44,-44]).buffer);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});await settle();
  options.recordingFail=true;p.emit({type:'session.closed'});await settle();
  assert.equal(p.elements.get('status').textContent,'Stopped');
  assert.match(p.elements.get('error').textContent,/Could not prepare the recording/);
  p.close();
});

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


test('pausing queued playback retains its real position before replay',async()=>{
  const p=page();await p.elements.get('start').onclick();
  const pcm=Buffer.alloc(480000);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64'),sample_rate:24000,channels:1,format:'pcm16'});
  await settle();await p.elements.get('start').onclick();await settle();
  p.contexts[1].currentTime=2.025;
  await p.elements.get('playback').onclick();
  assert.ok(Math.abs(p.elements.get('player').currentTime-2)<.0001);
  assert.equal(p.contexts[1].state,'closed');assert.equal(p.elements.get('player').paused,true);
  await p.elements.get('playback').onclick();assert.equal(p.elements.get('player').paused,false);
  p.elements.get('waveform').onkeydown({key:'ArrowRight',preventDefault(){}});
  assert.ok(Math.abs(p.elements.get('player').currentTime-2.5)<.0001);
  assert.equal(p.elements.get('player').paused,true);p.close();
});

for (const failure of ['permission','failStart']) {
  test(`a failed replacement preserves the previous recording: ${failure}`,async()=>{
    const options={},p=page(options);await p.elements.get('start').onclick();
    const pcm=Buffer.from(new Int16Array([120,-120]).buffer);
    p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});
    p.emit({type:'session.output_transcript.delta',delta:'Keep this recording.'});await settle();
    await p.elements.get('start').onclick();await settle();
    options[failure]=true;await p.elements.get('start').onclick();
    assert.equal(p.elements.get('download').disabled,false);
    assert.equal(p.elements.get('transcript').disabled,false);
    assert.equal(p.elements.get('translated').textContent,'Keep this recording.');
    p.elements.get('download').onclick();
    assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);
    assert.notEqual(p.elements.get('error').textContent,'');p.close();
  });
}

test('missing worklet flush acknowledgement reports interrupted input and rejects late PCM',async()=>{
  const p=page({holdFlush:true});await p.elements.get('start').onclick();
  const stopping=p.elements.get('start').onclick();
  const timer=p.timers.find(timer=>timer.delay===500);assert.ok(timer);timer.callback();
  await stopping;await settle();
  assert.match(p.elements.get('error').textContent,/final input may be incomplete/);
  assert.equal(p.elements.get('status').textContent,'Stopped');
  p.nodes[0].port.onmessage({data:new Int16Array([1,2]).buffer});await settle();
  assert.equal(p.calls.filter(call=>call.url?.startsWith('/input')).length,0);
  assert.equal(p.tracks[0].stopped,true);p.close();
});

test('an old setup failure cannot reset a replacement session after delayed cleanup',async()=>{
  let release;
  const options={nodeFail:true,closeOutput:new Promise(resolve=>{release=resolve;})},p=page(options);
  const failed=p.elements.get('start').onclick();await settle();
  await p.elements.get('start').onclick();
  options.nodeFail=false;options.closeOutput=null;
  await p.elements.get('start').onclick();assert.equal(p.elements.get('status').textContent,'Translating');
  release();await failed;
  assert.equal(p.elements.get('status').textContent,'Translating');
  assert.equal(p.elements.get('start').textContent,'Stop session');p.close();
});

test('decoder rejection preserves the preceding completed recording',async()=>{
  const options={},p=page(options);await p.elements.get('start').onclick();
  const pcm=Buffer.from(new Int16Array([123,-123]).buffer);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});await settle();
  // Keep the stream open so a second session can use this fixture's event source.
  p.emit({type:'session.closed'});await settle();
  await p.elements.get('start').onclick();options.bufferFail=true;
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64'),sample_rate:1});await settle();
  assert.match(p.elements.get('error').textContent,/cannot decode/);
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);p.close();
});

test('playback scheduling failure retains received audio for saving',async()=>{
  const p=page({playFail:true});await p.elements.get('start').onclick();
  const pcm=Buffer.from(new Int16Array([321,-321]).buffer);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});await settle();
  assert.match(p.elements.get('error').textContent,/playback failed/);
  assert.equal(p.elements.get('download').disabled,false);
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);p.close();
});

test('an empty replacement closes its output context and restores the previous seek position',async()=>{
  const p=page();await p.elements.get('start').onclick();
  const pcm=Buffer.alloc(48000);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});await settle();
  await p.elements.get('start').onclick();await settle();
  p.elements.get('waveform').onclick({clientX:250});
  await p.elements.get('start').onclick();
  p.emit({type:'session.closed'});await settle();p.frame();
  assert.equal(p.contexts[3].state,'closed');
  assert.equal(p.elements.get('player').currentTime,.5);
  assert.equal(p.elements.get('time').textContent,'00:00.500 / 00:01.000');
  assert.match(p.elements.get('status').textContent,/Previous recording kept/);
  p.elements.get('download').onclick();assert.equal((await p.downloads[0].blob.arrayBuffer()).byteLength,48044);p.close();
});
