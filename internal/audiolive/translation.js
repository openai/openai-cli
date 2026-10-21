'use strict';
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, '', '/');
  const $ = id => document.getElementById(id);
  const controls = ['source','language','microphone','noise'];
  let generation = 0, current = null, state = 'ready';
  let chunks = [], peaks = [], segments = [], outputText = [], originalText = [];
  let sampleCount = 0, nextStart = 0, outputContext = null, outputGain = null, recordingURL = '';
  let complete = false, outputRate = 24000, outputChannels = 1;
  let peakFrames = 0, peakValue = 0, framesPerPeak = 240, playedSegment = 0;

  const status = value => { $('status').textContent = value; };
  const showError = message => { $('error').textContent = message; $('error').hidden = !message; };
  const time = value => `${String(Math.floor(value / 60)).padStart(2,'0')}:${String(Math.floor(value % 60)).padStart(2,'0')}`;
  const endpoint = (route, id) => route + (id ? '?id=' + encodeURIComponent(id) : '');
  async function request(route, options = {}) {
    const response = await fetch(route, {...options, cache:'no-store', headers:{'X-OpenAI-Audio':token,...options.headers}});
    if (!response.ok) throw new Error(await response.text());
    return response;
  }
  function setState(value) {
    state = value;
    const busy = value === 'starting' || value === 'running' || value === 'finishing';
    for (const id of controls) $(id).disabled = busy;
    $('start').textContent = value === 'starting' ? 'Cancel' : value === 'running' ? 'Stop session' : value === 'finishing' ? 'Finishing…' : 'Start session';
    $('start').disabled = value === 'finishing' || !token;
    $('mute').hidden = value !== 'running';
    $('clear').disabled = busy;
  }
  async function devices() {
    if (!navigator.mediaDevices?.enumerateDevices) return;
    const devices = await navigator.mediaDevices.enumerateDevices();
    for (const [id, kind] of [['microphone','audioinput'],['output','audiooutput']]) {
      const select = $(id), previous = select.value;
      select.replaceChildren(new Option('System default',''));
      for (const device of devices.filter(d => d.kind === kind && d.deviceId && d.deviceId !== 'default')) {
        select.add(new Option(device.label || (kind === 'audioinput' ? 'Microphone' : 'Output device'), device.deviceId));
      }
      if ([...select.options].some(option => option.value === previous)) select.value = previous;
    }
    if (!('setSinkId' in AudioContext.prototype)) $('output').disabled = true;
  }
  function updateSource() {
    const tab = $('source').value === 'tab';
    $('tab-hint').hidden = !tab;
    $('microphone').disabled = tab || state === 'starting' || state === 'running' || state === 'finishing';
  }
  async function stopOutput() {
    const context = outputContext;
    outputContext = null; outputGain = null; segments = []; playedSegment = 0;
    if (context && context.state !== 'closed') await context.close();
  }
  function resetRecording() {
    outputRate = 24000; outputChannels = 1; peakFrames = 0; peakValue = 0; framesPerPeak = 240;
    chunks = []; peaks = []; outputText = []; originalText = []; sampleCount = 0; nextStart = 0; complete = false;
    $('original').replaceChildren(); $('translated').replaceChildren(); $('usage').textContent = '';
    $('download').disabled = true; $('transcript').disabled = true;
    $('player').pause(); $('player').removeAttribute('src'); $('player').load(); $('player').hidden = true;
    if (recordingURL) URL.revokeObjectURL(recordingURL);
    recordingURL = '';
  }
  function releaseCapture(run) {
    for (const track of run.stream?.getTracks() || []) track.stop();
    run.node?.disconnect();
    run.input?.disconnect();
    if (run.context && run.context.state !== 'closed') run.context.close().catch(() => {});
  }
  async function start() {
    showError('');
    if (!navigator.mediaDevices || !window.AudioWorkletNode) { showError('This browser cannot capture live audio. Try Chrome or Edge.'); return; }
    const run = {generation:++generation, controller:new AbortController(), stream:null, queue:Promise.resolve(), sequence:0, id:'', failed:false};
    current = run;
    setState('starting'); status('Choose your audio source');
    try {
      await stopOutput();
      if (current !== run) return;
      resetRecording();
      const tab = $('source').value === 'tab';
      if (tab) {
        if (!navigator.mediaDevices.getDisplayMedia) throw new Error('This browser cannot share tab audio. Try Chrome or Edge.');
        run.stream = await navigator.mediaDevices.getDisplayMedia({video:true,audio:true});
      } else {
        run.stream = await navigator.mediaDevices.getUserMedia({audio:{deviceId:$('microphone').value ? {exact:$('microphone').value} : undefined,channelCount:1,echoCancellation:true,noiseSuppression:$('noise').checked}});
      }
      if (current !== run) { releaseCapture(run); return; }
      if (!run.stream.getAudioTracks().length) throw new Error('No audio was shared. Choose a browser tab and enable Share tab audio.');
      devices().catch(() => {});
      run.context = new AudioContext({sampleRate:24000});
      if (run.context.sampleRate !== 24000) throw new Error('This browser cannot capture the required audio format. Try Chrome or Edge.');
      await run.context.audioWorklet.addModule('/capture-worklet.js');
      await run.context.resume();
      if (current !== run) { releaseCapture(run); return; }
      outputContext = new AudioContext();
      outputGain = outputContext.createGain(); outputGain.connect(outputContext.destination);
      outputGain.gain.value = $('listen').checked ? 1 : 0;
      if ($('output').value && outputContext.setSinkId) await outputContext.setSinkId($('output').value);
      await outputContext.resume();
      if (current !== run) { releaseCapture(run); return; }
      status('Connecting');
      const response = await request('/start',{method:'POST',signal:run.controller.signal,headers:{'Content-Type':'application/json'},body:JSON.stringify({language:$('language').value,noise_reduction:$('noise').checked})});
      run.id = (await response.json()).id;
      if (current !== run) { releaseCapture(run); request(endpoint('/stop',run.id),{method:'POST'}).catch(() => {}); return; }
      run.input = run.context.createMediaStreamSource(run.stream);
      run.node = new AudioWorkletNode(run.context,'translation-capture');
      run.node.port.onmessage = ({data}) => {
        if (data === 'flushed') { run.flushed?.(); return; }
        if (current !== run || run.failed || run.closed) return;
        run.queue = run.queue.then(() => { if (!run.closed && !run.failed) return request(endpoint('/input',run.id) + '&sequence=' + run.sequence++,{method:'POST',signal:run.controller.signal,body:data}); }).catch(error => { if (!run.closed) return fail(run,error); });
      };
      run.input.connect(run.node);
      // A zero-gain sink keeps capture processing active without monitoring source audio.
      run.sink = run.context.createGain(); run.sink.gain.value = 0;
      run.node.connect(run.sink); run.sink.connect(run.context.destination);
      for (const track of run.stream.getTracks()) track.addEventListener('ended',() => { if (current === run && state === 'running') stop(); },{once:true});
      $('mute').textContent = 'Mute input';
      setState('running'); status('Translating');
      readEvents(run).catch(error => fail(run,error));
    } catch (error) {
      if (current !== run) { releaseCapture(run); return; }
      let message = error.message;
      if (error.name === 'NotAllowedError') message = 'Audio access was not granted. Allow microphone or tab audio access, then start again.';
      if (error.name === 'NotFoundError') message = 'No microphone is available. Connect a microphone or choose Browser tab.';
      await fail(run,new Error(message));
    }
  }
  async function readEvents(run) {
    const response = await request(endpoint('/events',run.id),{signal:run.controller.signal});
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
    let pending = [];
    while (current === run) {
      const {value,done} = await reader.read();
      if (done) break;
      let start = 0, newline;
      while ((newline = value.indexOf('\n',start)) >= 0) {
        pending.push(value.slice(start,newline));
        const line = pending.join(''); pending = []; start = newline + 1;
        if (line) {
          let event;
          try { event = JSON.parse(line); } catch (_) { throw new Error('The service returned an unreadable translation update. Start a new session.'); }
          receive(run,event);
        }
      }
      if (start < value.length) pending.push(value.slice(start));
    }
    if (current === run && !complete && !run.failed) await fail(run,new Error('The connection ended before translation finished. Received audio remains available.'));
  }
  function receive(run,event) {
    if (current !== run || run.failed) return;
    if (event.type === 'session.input_transcript.delta') { originalText.push(event.delta); $('original').append(document.createTextNode(event.delta)); }
    if (event.type === 'session.output_transcript.delta') { outputText.push(event.delta); $('translated').append(document.createTextNode(event.delta)); $('transcript').disabled = false; }
    if (event.type === 'session.output_audio.delta') playChunk(event);
    if (event.tokens) $('usage').textContent = event.tokens.toLocaleString() + ' tokens';
    if (event.type === 'error') fail(run,new Error(event.message));
    if (event.type === 'session.closed') {
      complete = true; run.closed = true; releaseCapture(run); prepareRecording();
      setState('ready'); updateSource(); status('Finished');
    }
  }
  function playChunk(event) {
    const rate = event.sample_rate || 24000, channels = event.channels || 1;
    if ((event.format && event.format !== 'pcm16') || !Number.isInteger(rate) || rate <= 0 || !Number.isInteger(channels) || channels < 1) throw new Error('The service returned an unsupported audio format. Received audio remains available.');
    if (sampleCount && (rate !== outputRate || channels !== outputChannels)) throw new Error('The audio format changed during translation. Received audio remains available.');
    const bytes = Uint8Array.from(atob(event.delta), c => c.charCodeAt(0));
    if (!bytes.length) return;
    if (bytes.length % (2 * channels)) throw new Error('The service returned incomplete audio. Received audio remains available.');
    outputRate = rate; outputChannels = channels;
    if (!sampleCount) framesPerPeak = Math.max(1,Math.round(rate / 100));
    const view = new DataView(bytes.buffer), frames = bytes.length / (2 * channels);
    const buffer = outputContext.createBuffer(channels,frames,rate);
    const samples = Array.from({length:channels},(_,i) => buffer.getChannelData(i));
    for (let i = 0; i < frames; i++) {
      for (let channel = 0; channel < channels; channel++) {
        const value = view.getInt16((i * channels + channel) * 2,true) / 32768;
        samples[channel][i] = value; peakValue = Math.max(peakValue,Math.abs(value));
      }
      if (++peakFrames >= framesPerPeak) {
        peaks.push(peakValue); peakValue = 0; peakFrames = 0;
        if (peaks.length >= 4096) {
          const merged = [];
          for (let j = 0; j < peaks.length; j += 2) merged.push(Math.max(peaks[j],peaks[j+1] || 0));
          peaks = merged; framesPerPeak *= 2;
        }
      }
    }
    chunks.push(bytes);
    const source = outputContext.createBufferSource(); source.buffer = buffer; source.connect(outputGain);
    nextStart = Math.max(outputContext.currentTime + .025,nextStart);
    segments.push({start:nextStart,duration:buffer.duration,offset:sampleCount / outputRate});
    source.start(nextStart); nextStart += buffer.duration; sampleCount += frames;
  }

  function wavBlob() {
    const bytes = sampleCount * 2 * outputChannels;
    if (bytes > 0xffffffff - 36) throw new Error('This recording exceeds the WAV format size. Save shorter recordings.');
    const header = new ArrayBuffer(44), view = new DataView(header);
    const text = (at,value) => [...value].forEach((c,i) => view.setUint8(at+i,c.charCodeAt(0)));
    text(0,'RIFF'); view.setUint32(4,36+bytes,true); text(8,'WAVE'); text(12,'fmt '); view.setUint32(16,16,true); view.setUint16(20,1,true); view.setUint16(22,outputChannels,true); view.setUint32(24,outputRate,true); view.setUint32(28,outputRate*2*outputChannels,true); view.setUint16(32,2*outputChannels,true); view.setUint16(34,16,true); text(36,'data'); view.setUint32(40,bytes,true);
    return new Blob([header,...chunks],{type:'audio/wav'});
  }
  function prepareRecording() {
    if (!sampleCount) return;
    if (recordingURL) URL.revokeObjectURL(recordingURL);
    recordingURL = URL.createObjectURL(wavBlob());
    $('player').src = recordingURL; $('player').hidden = false; $('download').disabled = false;
  }
  async function stop() {
    const run = current;
    if (!run) return;
    if (state === 'starting') {
      current = null; generation++; run.controller.abort(); releaseCapture(run); await stopOutput();
      setState('ready'); updateSource(); status('Ready'); return;
    }
    if (state !== 'running') return;
    setState('finishing'); status('Finishing translation');
    try {
      await new Promise(resolve => { run.flushed = resolve; run.node.port.postMessage('flush'); setTimeout(resolve,500); });
      releaseCapture(run); await run.queue;
      if (!run.failed) await request(endpoint('/stop',run.id),{method:'POST',signal:run.controller.signal});
    } catch (error) { await fail(run,error); }
  }
  async function fail(run,error) {
    if (current !== run || run.failed) return;
    run.failed = true; releaseCapture(run);
    if (run.id) request(endpoint('/stop',run.id),{method:'POST'}).catch(() => {});
    run.controller.abort();
    try { prepareRecording(); } catch (_) {}
    showError(error.message || 'Translation stopped. Start a new session.'); status('Stopped'); setState('ready'); updateSource();
  }
  function save(blob,extension) {
    const name = outputText.join('').normalize('NFKD').replace(/[\u0300-\u036f]/g,'').toLowerCase().replace(/[^\p{L}\p{N}]+/gu,'-').replace(/^-|-$/g,'').split('-').slice(0,8).join('-') || 'translation';
    const filename = Array.from(name).slice(0,55).join('');
    const link = document.createElement('a'), url = URL.createObjectURL(blob);
    link.href = url; link.download = 'translation-' + filename + '.' + extension; link.click(); setTimeout(() => URL.revokeObjectURL(url),1000);
  }
  function draw() {
    const canvas = $('waveform'), ratio = devicePixelRatio || 1, width = canvas.clientWidth;
    if (canvas.width !== Math.round(width*ratio)) canvas.width = Math.round(width*ratio);
    if (canvas.height !== 120*ratio) canvas.height = 120*ratio;
    const context = canvas.getContext('2d'); context.setTransform(ratio,0,0,ratio,0,0); context.clearRect(0,0,width,120);
    let position = $('player').currentTime || 0;
    if (outputContext && $('player').paused) {
      while (playedSegment+1 < segments.length && outputContext.currentTime >= segments[playedSegment+1].start) playedSegment++;
      const segment = segments[playedSegment];
      if (segment) position = segment.offset + Math.max(0,Math.min(segment.duration,outputContext.currentTime-segment.start));
    }
    const totalPeaks = peaks.length + (peakFrames ? 1 : 0);
    const duration = sampleCount / outputRate, bars = Math.max(1,Math.floor(width / 5));
    const style = getComputedStyle(document.documentElement);
    for (let bar = 0; bar < bars && totalPeaks; bar++) {
      const first = Math.floor(bar * totalPeaks / bars), last = Math.max(first+1,Math.ceil((bar+1)*totalPeaks/bars));
      let peak = 0;
      for (let i = first; i < last; i++) peak = Math.max(peak,i === peaks.length ? peakValue : peaks[i] || 0);
      const height = Math.max(2,Math.sqrt(peak)*100);
      context.fillStyle = style.getPropertyValue(bar / bars <= position / duration ? '--accent' : '--muted');
      context.beginPath(); context.roundRect(bar*5,60-height/2,2,height,1); context.fill();
    }
    if (duration) { context.fillStyle = style.getPropertyValue('--accent'); context.fillRect(Math.min(width-1,width*position/duration),8,1.5,104); }
    $('time').textContent = time(position) + ' / ' + time(duration);
    canvas.tabIndex = $('player').hidden ? -1 : 0;
    canvas.setAttribute('aria-valuenow',position.toFixed(2)); canvas.setAttribute('aria-valuemax',duration.toFixed(2));
    requestAnimationFrame(draw);
  }
  $('start').onclick = () => state === 'starting' || state === 'running' ? stop() : start();
  $('source').onchange = updateSource;
  $('listen').onchange = () => { if (outputGain) outputGain.gain.value = $('listen').checked ? 1 : 0; $('player').muted = !$('listen').checked; };
  $('output').onchange = async () => {
    try {
      if (outputContext?.setSinkId) await outputContext.setSinkId($('output').value);
      if ($('player').setSinkId) await $('player').setSinkId($('output').value);
    } catch (_) { showError('Could not use this output device. Choose System default or another device.'); }
  };
  $('mute').onclick = () => {
    const tracks = current?.stream?.getAudioTracks() || [], enabled = tracks.some(track => track.enabled);
    for (const track of tracks) track.enabled = !enabled;
    $('mute').textContent = enabled ? 'Unmute input' : 'Mute input';
  };
  $('waveform').onclick = event => {
    if ($('player').hidden) return;
    const box = $('waveform').getBoundingClientRect();
    $('player').currentTime = Math.max(0,Math.min(sampleCount/outputRate,(event.clientX-box.left)/box.width*sampleCount/outputRate));
  };
  $('waveform').onkeydown = event => {
    if ($('player').hidden) return;
    let position = $('player').currentTime;
    if (event.key === 'ArrowLeft') position -= .5;
    else if (event.key === 'ArrowRight') position += .5;
    else if (event.key === 'Home') position = 0;
    else if (event.key === 'End') position = sampleCount/outputRate;
    else if (event.key === ' ') { event.preventDefault(); $('player').paused ? $('player').play().catch(() => showError('Press Play to allow audio playback.')) : $('player').pause(); return; }
    else return;
    event.preventDefault(); $('player').currentTime = Math.max(0,Math.min(sampleCount/outputRate,position));
  };
  $('player').onplay = () => stopOutput();
  $('download').onclick = () => { try { save(wavBlob(),'wav'); } catch (error) { showError(error.message); } };
  $('transcript').onclick = () => save(new Blob(['Original\n',originalText.join(''),'\n\nTranslation\n',outputText.join('')],{type:'text/plain;charset=utf-8'}),'txt');
  $('clear').onclick = async () => { if (state === 'running' || state === 'starting' || state === 'finishing') return; current = null; await stopOutput(); resetRecording(); showError(''); status('Ready'); };
  window.addEventListener('pagehide',() => { if (current) { current.controller.abort(); releaseCapture(current); } stopOutput().catch(() => {}); });
  if (!token) showError('Open live translation from the CLI to connect this page.');
  setState('ready'); devices().catch(() => {}); draw();
})();
