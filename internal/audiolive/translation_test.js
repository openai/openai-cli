'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

// These stand-ins exercise browser control flow without permissions or external requests.
function page(options = {}) {
  const elements = new Map(), calls = [], contexts = [], nodes = [], blobs = new Map(), downloads = [], timers = [];
  class Track extends EventTarget {
    constructor(){super();this.kind=options.noTabAudio?'video':'audio';this.enabled=true;this.stopped=false;this.readyState=options.alreadyEnded?'ended':'live';}
    stop(){this.stopped=true;this.readyState='ended';}
    end(){this.readyState='ended';this.dispatchEvent(new Event('ended'));}
  }
  const tracks = [new Track()];
  const capture = () => {
    if(tracks[0].stopped)tracks[0]=new Track();
    const captured=[...tracks];
    return {getTracks:()=>captured,getAudioTracks:()=>captured.filter(track=>track.kind==='audio')};
  };
  let eventController, frame, sessionNumber = 0;
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
    constructor(config){this.output=!config;this.sampleRate=config?.sampleRate||48000;this.currentTime=0;this.state='running';this.destination={};this.audioWorklet={addModule:()=>options.worklet||Promise.resolve()};contexts.push(this);}
    resume(){return this.output && options.resumeOutput ? options.resumeOutput : Promise.resolve();}
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
    navigator:{mediaDevices:{enumerateDevices:async()=>[],getUserMedia:async()=>{calls.push({kind:'microphone'});if(options.permission)throw new DOMException('denied','NotAllowedError');return options.capture?options.capture:capture();},getDisplayMedia:async()=>{calls.push({kind:'tab'});return options.capture?options.capture:capture();}}},
    fetch:async(url,config)=>{
      calls.push({url,config});
      if(url==='/start') {
        const id='synthetic-session-'+(++sessionNumber);
        if(options.start)return options.start(id,config);
        return options.failStart ? new Response('Your API key was not accepted.',{status:401}) : new Response(JSON.stringify({id}),{headers:{'X-OpenAI-Audio-Session':id}});
      }
      if(url.startsWith('/events')){const eventStream=new ReadableStream({start(controller){eventController=controller;}});const controller=eventController;config.signal.addEventListener('abort',()=>{try{controller.error(new DOMException('aborted','AbortError'));}catch(_){}},{once:true});return new Response(eventStream);}
      if(url.startsWith('/input') && options.input)return options.input(url,config);
      if(url.startsWith('/stop')){if(options.stop)return options.stop(url,config);if(eventController && !url.endsWith('&abort=1'))emit({type:'session.closed'});}
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

function startResponse(id, json) {
  const response=new Response(JSON.stringify({id}),{headers:{'X-OpenAI-Audio-Session':id}});
  if(json)response.json=json;
  return response;
}

test('start cancellation waits for scoped cleanup while its JSON body is pending',{timeout:2000},async()=>{
  let releaseJSON,releaseStop,bodyRead=false,active,firstID;
  const options={start(id){active=id;firstID=id;return startResponse(id,()=>{bodyRead=true;return new Promise(resolve=>{releaseJSON=resolve;});});},
    stop(url){assert.equal(url,'/stop?id='+firstID+'&abort=1');return new Promise(resolve=>{releaseStop=()=>{active=null;resolve(new Response(null,{status:204}));};});}};
  const p=page(options),opening=p.elements.get('start').onclick();await settle();
  assert.equal(bodyRead,true);assert.equal(p.elements.get('status').textContent,'Connecting');
  const canceling=p.elements.get('start').onclick();await settle();
  assert.equal(p.elements.get('start').disabled,true);assert.equal(p.tracks[0].stopped,true);
  assert.equal(p.calls.find(call=>call.url==='/start').config.signal.aborted,true);
  assert.equal(typeof releaseStop,'function','Cancel must send a scoped stop before the JSON body resolves');
  await p.elements.get('start').onclick();assert.equal(p.calls.filter(call=>call.url==='/start').length,1);
  releaseStop();await canceling;assert.equal(active,null);assert.equal(p.elements.get('status').textContent,'Ready');
  options.start=id=>{assert.equal(active,null);active=id;return startResponse(id);};
  options.stop=url=>{if(url==='/stop?id='+active+'&abort=1')active=null;return new Response(null,{status:204});};
  await p.elements.get('start').onclick();const replacement=active;
  assert.notEqual(replacement,firstID);assert.equal(p.elements.get('status').textContent,'Translating');
  releaseJSON({id:firstID});await opening;await settle();
  assert.equal(active,replacement);assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.tracks[0].stopped,false);
  assert.equal(p.elements.get('error').textContent,'');p.close();
});

for(const kind of ['malformed','rejected','missing-id','different-id','null']) {
  test(`start cancellation releases a ${kind} JSON response before retry`,{timeout:2000},async()=>{
    let releaseStop,active,firstID;
    const options={start(id){active=id;firstID=id;return startResponse(id,async()=>{
      if(kind==='malformed')return JSON.parse('{');
      if(kind==='rejected')throw new Error('Synthetic body read failed');
      return kind==='null'?null:kind==='missing-id'?{}:{id:'wrong-session'};
    });},stop(url){assert.equal(url,'/stop?id='+firstID+'&abort=1');return new Promise(resolve=>{releaseStop=()=>{active=null;resolve(new Response(null,{status:204}));};});}};
    const p=page(options),opening=p.elements.get('start').onclick();await settle();
    assert.equal(typeof releaseStop,'function','failed JSON must still release the known session');
    assert.equal(p.elements.get('start').disabled,true);assert.equal(p.tracks[0].stopped,true);
    releaseStop();await opening;assert.equal(active,null);
    assert.match(p.elements.get('error').textContent,/could not finish connecting.*Start a new session/);
    assert.equal(p.elements.get('start').disabled,false);
    options.start=id=>{assert.equal(active,null);active=id;return startResponse(id);};options.stop=null;
    await p.elements.get('start').onclick();assert.equal(p.elements.get('status').textContent,'Translating');p.close();
  });
}

test('start cancellation before headers blocks retry and releases a late identity without reading its body',{timeout:2000},async()=>{
  let releaseHeaders,oldID,bodyRead=false;
  const options={start(id){oldID=id;return new Promise(resolve=>{releaseHeaders=()=>resolve(startResponse(id,()=>{bodyRead=true;return new Promise(()=>{});}));});},
    stop:()=>new Response(null,{status:204})};
  const p=page(options),opening=p.elements.get('start').onclick();await settle();
  await p.elements.get('start').onclick();assert.equal(p.elements.get('status').textContent,'Stopped');
  assert.equal(p.calls.find(call=>call.url==='/start').config.signal.aborted,true);
  assert.equal(p.elements.get('start').disabled,true);assert.match(p.elements.get('error').textContent,/Could not confirm.*Reopen live translation/);
  await p.elements.get('start').onclick();assert.equal(p.calls.filter(call=>call.url==='/start').length,1);
  releaseHeaders();await opening;await settle();
  assert.equal(bodyRead,false);assert.equal(p.elements.get('status').textContent,'Stopped');assert.equal(p.elements.get('start').disabled,true);
  assert.equal(p.calls.filter(call=>call.url==='/stop?id='+oldID+'&abort=1').length,1);p.close();
});

test('start cancellation preserves previous output when a canceled JSON read fails after retry',{timeout:2000},async()=>{
  const options={},p=page(options);await p.elements.get('start').onclick();
  const pcm=Buffer.alloc(48000);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});
  p.emit({type:'session.output_transcript.delta',delta:'Previous translation.'});await settle();
  await p.elements.get('start').onclick();await settle();p.elements.get('waveform').onclick({clientX:250});
  let rejectJSON;
  options.start=id=>startResponse(id,()=>new Promise((_,reject)=>{rejectJSON=reject;}));
  options.stop=()=>new Response(null,{status:204});
  const opening=p.elements.get('start').onclick();await settle();await p.elements.get('start').onclick();
  assert.equal(p.elements.get('translated').textContent,'Previous translation.');assert.equal(p.elements.get('player').currentTime,.5);
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);
  options.start=null;await p.elements.get('start').onclick();
  rejectJSON(new Error('Late body failure'));await opening;
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.elements.get('error').textContent,'');p.close();
});

test('start cancellation on page close sends a scoped keepalive stop while JSON is pending',{timeout:2000},async()=>{
  let resolveJSON,id;
  const options={start(value){id=value;return startResponse(id,()=>new Promise(resolve=>{resolveJSON=resolve;}));},stop:()=>new Response(null,{status:204})};
  const p=page(options),opening=p.elements.get('start').onclick();await settle();p.close();
  const stopped=p.calls.find(call=>call.url==='/stop?id='+id+'&abort=1');
  assert.ok(stopped);assert.equal(stopped.config.keepalive,true);assert.equal(p.tracks[0].stopped,true);
  resolveJSON({id});await opening;
});

for(const failure of ['timeout','rejected']) {
  test(`start cancellation reports unconfirmed ${failure} cleanup without enabling retry`,{timeout:2000},async()=>{
    let resolveJSON,cleanupSignal,id;
    const options={start(value){id=value;return startResponse(id,()=>new Promise(resolve=>{resolveJSON=resolve;}));},
      stop(_,config){cleanupSignal=config.signal;return failure==='timeout'?new Promise(()=>{}):Promise.reject(new Error('Synthetic stop failure'));}};
    const p=page(options),opening=p.elements.get('start').onclick();await settle();
    const canceling=p.elements.get('start').onclick();await settle();
    if(failure==='timeout') {
      const timer=p.timers.find(timer=>timer.active && timer.delay===2000);assert.ok(timer,'startup cleanup needs a deadline');timer.callback();
    }
    await canceling;assert.equal(cleanupSignal.aborted,true);
    assert.equal(p.elements.get('status').textContent,'Stopped');assert.equal(p.elements.get('start').disabled,true);
    assert.match(p.elements.get('error').textContent,/Could not confirm.*Reopen live translation from the CLI/);
    const guidance=p.elements.get('error').textContent;
    assert.equal(p.elements.get('clear').disabled,true);await p.elements.get('clear').onclick();
    assert.equal(p.elements.get('error').textContent,guidance);assert.equal(p.elements.get('status').textContent,'Stopped');
    assert.equal(p.timers.some(timer=>timer.active),false);
    await p.elements.get('start').onclick();assert.equal(p.calls.filter(call=>call.url==='/start').length,1);
    resolveJSON({id});await opening;p.close();
  });
}

test('start cancellation with a missing identity preserves reopen guidance and blocks retry',async()=>{
  const p=page({start:()=>new Response(JSON.stringify({id:'unknown-session'}))});
  await p.elements.get('start').onclick();
  const guidance=p.elements.get('error').textContent;
  assert.match(guidance,/Reopen live translation from the CLI/);
  assert.equal(p.elements.get('status').textContent,'Stopped');
  assert.equal(p.elements.get('start').disabled,true);assert.equal(p.elements.get('clear').disabled,true);
  await p.elements.get('clear').onclick();await p.elements.get('start').onclick();
  assert.equal(p.elements.get('error').textContent,guidance);assert.equal(p.elements.get('status').textContent,'Stopped');
  assert.equal(p.calls.filter(call=>call.url==='/start').length,1);
  assert.equal(p.calls.filter(call=>call.url?.startsWith('/stop')).length,0);p.close();
});

test('start cancellation still permits retry when the server rejects an unpublished start',async()=>{
  const options={failStart:true},p=page(options);await p.elements.get('start').onclick();
  assert.match(p.elements.get('error').textContent,/API key was not accepted/);
  assert.equal(p.elements.get('start').disabled,false);assert.equal(p.elements.get('clear').disabled,false);
  options.failStart=false;await p.elements.get('start').onclick();
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.elements.get('error').textContent,'');p.close();
});

for(const outcome of ['resolved','rejected']) {
test(`clearing an old recording cannot alter its replacement after ${outcome} cleanup`,async()=>{
  let release;
  const options={closeOutput:new Promise((resolve,reject)=>{release=()=>outcome==='resolved'?resolve():reject(new Error('Synthetic close failure'));})},p=page(options);
  await p.elements.get('start').onclick();
  p.emit({type:'session.output_audio.delta',delta:Buffer.from(new Int16Array([1,-1]).buffer).toString('base64')});
  p.emit({type:'session.closed'});await settle();
  const clearing=p.elements.get('clear').onclick();await settle();
  assert.equal(p.elements.get('start').disabled,false);
  options.closeOutput=null;await p.elements.get('start').onclick();
  const replacement=Buffer.from(new Int16Array([7,-7]).buffer);
  p.emit({type:'session.output_audio.delta',delta:replacement.toString('base64')});
  p.emit({type:'session.output_transcript.delta',delta:'Replacement translation.'});
  p.emit({type:'session.closed'});await settle();
  release();await clearing;
  assert.equal(p.elements.get('status').textContent,'Finished');
  assert.equal(p.elements.get('error').textContent,'');
  assert.equal(p.elements.get('translated').textContent,'Replacement translation.');
  assert.equal(p.elements.get('download').disabled,false);assert.equal(p.elements.get('transcript').disabled,false);
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),replacement);p.close();
});
}

test('clearing failure retains the recording and permits another Clear',async()=>{
  let rejectClose;
  const options={closeOutput:new Promise((_,reject)=>{rejectClose=reject;})},p=page(options);
  await p.elements.get('start').onclick();
  const pcm=Buffer.from(new Int16Array([6,-6]).buffer);
  p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});
  p.emit({type:'session.closed'});await settle();
  const clearing=p.elements.get('clear').onclick();await settle();rejectClose(new Error('Synthetic close failure'));await clearing;
  assert.match(p.elements.get('error').textContent,/Try Clear again/);
  assert.equal(p.elements.get('clear').disabled,false);assert.equal(p.elements.get('start').disabled,false);
  p.elements.get('download').onclick();assert.deepEqual(Buffer.from(await p.downloads[0].blob.arrayBuffer()).subarray(44),pcm);
  await p.elements.get('clear').onclick();
  assert.equal(p.elements.get('status').textContent,'Ready');assert.equal(p.elements.get('error').textContent,'');
  assert.equal(p.elements.get('download').disabled,true);p.close();
});

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
  await stopping;
  assert.deepEqual(p.calls.filter(call=>call.url?.startsWith('/stop')).map(call=>call.url),['/stop?id=synthetic-session-1','/stop?id=synthetic-session-1&abort=1']);
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

for (const ending of ['error','session.closed']) {
  test(`original-only captions survive first-session ${ending}`,async()=>{
    const p=page();await p.elements.get('start').onclick();
    p.emit({type:'session.input_transcript.delta',delta:'Original <script>literal</script>.'});await settle();
    p.emit({type:ending,message:'Synthetic translation failure.'});await settle();
    assert.equal(p.elements.get('original').textContent,'Original <script>literal</script>.');
    assert.equal(p.elements.get('translated').textContent,'');
    assert.equal(p.elements.get('transcript').disabled,false);
    assert.equal(p.elements.get('download').disabled,true);assert.equal(p.elements.get('playback').disabled,true);
    p.elements.get('transcript').onclick();
    assert.equal(await p.downloads[0].blob.text(),'Original\nOriginal <script>literal</script>.\n\nTranslation\n');
    assert.equal(p.tracks[0].stopped,true);
    assert.equal(p.elements.get('status').textContent,ending==='error'?'Stopped':'No translated output.');
    if(ending==='error')assert.equal(p.elements.get('error').textContent,'Synthetic translation failure.');
    await p.elements.get('clear').onclick();
    assert.equal(p.elements.get('original').textContent,'');assert.equal(p.elements.get('transcript').disabled,true);p.close();
  });
}

for (const track of ['input','output']) {
  test(`caption export accepts a nonempty ${track} track during capture`,async()=>{
    const p=page();await p.elements.get('start').onclick();
    p.emit({type:`session.${track}_transcript.delta`,delta:'Synthetic captions.'});await settle();
    assert.equal(p.elements.get('transcript').disabled,false);
    p.elements.get('transcript').onclick();
    assert.equal(await p.downloads[0].blob.text(),track==='input'?'Original\nSynthetic captions.\n\nTranslation\n':'Original\n\n\nTranslation\nSynthetic captions.');
    assert.equal(p.elements.get('status').textContent,'Translating');p.close();
  });
}

test('empty caption deltas neither enable export nor replace retained captions',async()=>{
  const p=page();await p.elements.get('start').onclick();
  for(const track of ['input','output'])p.emit({type:`session.${track}_transcript.delta`,delta:''});
  await settle();assert.equal(p.elements.get('transcript').disabled,true);
  p.emit({type:'session.closed'});await settle();
  await p.elements.get('start').onclick();
  p.emit({type:'session.input_transcript.delta',delta:'Retained original.'});
  p.emit({type:'session.closed'});await settle();
  await p.elements.get('start').onclick();
  for(const track of ['input','output'])p.emit({type:`session.${track}_transcript.delta`,delta:''});
  p.emit({type:'session.closed'});await settle();
  assert.equal(p.elements.get('original').textContent,'Retained original.');
  assert.equal(p.elements.get('transcript').disabled,false);p.close();
});

for (const previous of ['original-only','recording']) for (const ending of ['error','session.closed']) {
  test(`caption-only replacement preserves prior ${previous} after ${ending}`,async()=>{
    const p=page();await p.elements.get('start').onclick();
    p.emit({type:'session.input_transcript.delta',delta:'Previous original.'});
    const pcm=Buffer.from(new Int16Array([123,-123]).buffer);
    if(previous==='recording') {
      p.emit({type:'session.output_audio.delta',delta:pcm.toString('base64')});
      p.emit({type:'session.output_transcript.delta',delta:'Previous translation.'});
    }
    p.emit({type:'session.closed'});await settle();
    await p.elements.get('start').onclick();
    p.emit({type:'session.input_transcript.delta',delta:'Replacement original.'});
    p.emit({type:'session.output_transcript.delta',delta:''});
    p.emit({type:ending,message:'Synthetic replacement failure.'});await settle();
    assert.equal(p.elements.get('original').textContent,'Previous original.');
    assert.equal(p.elements.get('translated').textContent,previous==='recording'?'Previous translation.':'');
    assert.equal(p.elements.get('transcript').disabled,false);
    p.elements.get('transcript').onclick();
    assert.equal(await p.downloads[0].blob.text(),'Original\nPrevious original.\n\nTranslation\n'+(previous==='recording'?'Previous translation.':''));
    if(previous==='recording') {
      p.elements.get('download').onclick();
      assert.deepEqual(Buffer.from(await p.downloads[1].blob.arrayBuffer()).subarray(44),pcm);
    } else assert.equal(p.elements.get('download').disabled,true);
    if(ending==='session.closed')assert.match(p.elements.get('status').textContent,/Previous recording kept/);
    p.close();
  });
}

test('late canceled setup cannot restore original captions over a newer session',{timeout:2000},async()=>{
  const options={},p=page(options);await p.elements.get('start').onclick();
  p.emit({type:'session.input_transcript.delta',delta:'Previous original.'});
  p.emit({type:'session.closed'});await settle();
  let rejectJSON;
  options.start=id=>startResponse(id,()=>new Promise((_,reject)=>{rejectJSON=reject;}));
  options.stop=()=>new Response(null,{status:204});
  const opening=p.elements.get('start').onclick();await settle();await p.elements.get('start').onclick();
  assert.equal(p.elements.get('original').textContent,'Previous original.');
  options.start=null;await p.elements.get('start').onclick();
  p.emit({type:'session.input_transcript.delta',delta:'New original.'});await settle();
  rejectJSON(new Error('Late body failure'));await opening;
  assert.equal(p.elements.get('original').textContent,'New original.');
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.elements.get('error').textContent,'');
  assert.equal(p.elements.get('transcript').disabled,false);p.close();
});

for (const ending of ['error','session.closed']) {
  test(`lifecycle cleanup waits for release after ${ending} before retry`,async()=>{
    let active,releaseStop;
    const options={start(id){if(active)return new Response('Still active',{status:409});active=id;return startResponse(id);},
      stop(url){assert.ok(url.startsWith('/stop?id='+active));return new Promise(resolve=>{releaseStop=()=>{active=null;resolve(new Response(null,{status:204}));};});}};
    const p=page(options);await p.elements.get('start').onclick();
    p.emit({type:'session.input_transcript.delta',delta:'Retained captions.'});
    p.emit({type:ending,message:'Synthetic connection failure.'});await settle();
    assert.equal(typeof releaseStop,'function');assert.equal(p.elements.get('start').disabled,true);
    await p.elements.get('start').onclick();assert.equal(p.calls.filter(call=>call.url==='/start').length,1);
    releaseStop();await settle();
    assert.equal(p.elements.get('start').disabled,false);assert.equal(p.elements.get('transcript').disabled,false);
    p.elements.get('transcript').onclick();assert.match(await p.downloads[0].blob.text(),/Retained captions\./);
    await p.elements.get('start').onclick();assert.equal(p.elements.get('status').textContent,'Translating');
    assert.equal(p.elements.get('error').textContent,'');p.close();
  });
}

for (const outcome of ['timeout','rejected']) {
  test(`lifecycle cleanup ${outcome} retains captions and prevents an unsafe retry`,async()=>{
    const p=page({stop:()=>outcome==='timeout'?new Promise(()=>{}):Promise.reject(new Error('Synthetic stop failure'))});
    await p.elements.get('start').onclick();
    p.emit({type:'session.input_transcript.delta',delta:'Received before failure.'});
    p.emit({type:'error',message:'Synthetic failure.'});await settle();
    if(outcome==='timeout') {const timer=p.timers.find(timer=>timer.active&&timer.delay===2000);assert.ok(timer);timer.callback();await settle();}
    assert.equal(p.elements.get('status').textContent,'Stopped');assert.equal(p.elements.get('start').disabled,true);
    assert.match(p.elements.get('error').textContent,/Could not confirm.*Reopen live translation/);
    assert.equal(p.elements.get('transcript').disabled,false);assert.equal(p.elements.get('original').textContent,'Received before failure.');
    await p.elements.get('clear').onclick();await p.elements.get('start').onclick();
    assert.equal(p.calls.filter(call=>call.url==='/start').length,1);assert.equal(p.elements.get('original').textContent,'Received before failure.');
    assert.equal(p.timers.some(timer=>timer.active),false);p.close();
  });
}

test('lifecycle cleanup handles rejected output close without losing captions',async()=>{
  let rejectClose;const options={},p=page(options);await p.elements.get('start').onclick();
  options.closeOutput=new Promise((_,reject)=>{rejectClose=reject;});
  p.emit({type:'session.input_transcript.delta',delta:'Keep original captions.'});p.emit({type:'error',message:'Synthetic failure.'});await settle();
  rejectClose(new Error('Synthetic close failure'));await settle();
  assert.equal(p.elements.get('status').textContent,'Stopped');assert.equal(p.elements.get('start').disabled,true);
  assert.match(p.elements.get('error').textContent,/Could not finish closing audio.*Reopen live translation/);
  assert.equal(p.elements.get('original').textContent,'Keep original captions.');assert.equal(p.elements.get('transcript').disabled,false);p.close();
});

for (const phase of ['worklet','output-resume','headers','body']) {
  test(`capture ending during ${phase} cancels setup and releases any late session`,{timeout:2000},async()=>{
    let release,id;
    const pending=new Promise(resolve=>{release=resolve;});
    const options={stop:()=>new Response(null,{status:204})};
    if(phase==='worklet')options.worklet=pending;
    if(phase==='output-resume')options.resumeOutput=pending;
    if(phase==='headers')options.start=value=>{id=value;return pending;};
    if(phase==='body')options.start=value=>{id=value;return startResponse(id,()=>pending);};
    const p=page(options),opening=p.elements.get('start').onclick();await settle();
    p.tracks[0].end();await settle();
    assert.equal(p.elements.get('status').textContent,phase==='headers'?'Stopped':'Ready');
    assert.match(p.elements.get('error').textContent,phase==='headers'?/Could not confirm.*Reopen live translation/:/Audio capture ended before translation started/);
    assert.equal(p.elements.get('start').disabled,phase==='headers');
    assert.equal(p.tracks[0].stopped,true);
    release(phase==='headers'?startResponse(id):phase==='body'?{id}:undefined);await opening;await settle();
    assert.equal(p.calls.filter(call=>call.url?.startsWith('/events')).length,0);
    assert.equal(p.calls.filter(call=>call.url==='/start').length,id?1:0);
    if(id)assert.ok(p.calls.some(call=>call.url==='/stop?id='+id+'&abort=1'));
    assert.equal(p.elements.get('status').textContent,phase==='headers'?'Stopped':'Ready');p.close();
  });
}

test('capture already ended at acquisition cannot open a session',async()=>{
  const p=page({alreadyEnded:true});await p.elements.get('start').onclick();await settle();
  assert.equal(p.calls.filter(call=>call.url==='/start').length,0);
  assert.match(p.elements.get('error').textContent,/Audio capture ended/);assert.equal(p.elements.get('status').textContent,'Ready');p.close();
});

test('capture ending while running drains normally and old track events cannot stop a replacement',async()=>{
  const p=page();await p.elements.get('start').onclick();const old=p.tracks[0];
  p.emit({type:'session.output_transcript.delta',delta:'Keep translated captions.'});await settle();
  old.end();await settle();
  assert.ok(p.calls.some(call=>call.url==='/stop?id=synthetic-session-1'));
  assert.equal(p.elements.get('translated').textContent,'Keep translated captions.');assert.equal(p.elements.get('status').textContent,'Finished');
  await p.elements.get('start').onclick();old.end();await settle();
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.tracks[0].stopped,false);p.close();
});

test('lifecycle completion restoring interrupted captions does not report a false early EOF',async()=>{
  const p=page();await p.elements.get('start').onclick();
  p.emit({type:'session.output_transcript.delta',delta:'Partial translated captions.'});
  p.emit({type:'error',message:'Synthetic interruption.'});await settle();
  await p.elements.get('start').onclick();p.emit({type:'session.closed'});await settle();
  assert.match(p.elements.get('status').textContent,/Previous recording kept/);
  assert.equal(p.elements.get('error').textContent,'');assert.equal(p.elements.get('translated').textContent,'Partial translated captions.');p.close();
});

test('late ended event from a released track cannot stop replacement capture',async()=>{
  const p=page();await p.elements.get('start').onclick();const old=p.tracks[0];
  await p.elements.get('start').onclick();await settle();
  await p.elements.get('start').onclick();const stops=p.calls.filter(call=>call.url?.startsWith('/stop')).length;
  old.end();await settle();
  assert.equal(p.calls.filter(call=>call.url?.startsWith('/stop')).length,stops);
  assert.equal(p.elements.get('status').textContent,'Translating');assert.equal(p.tracks[0].stopped,false);p.close();
});
