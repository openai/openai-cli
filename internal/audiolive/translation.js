'use strict';
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, '', '/');
  const $ = id => document.getElementById(id);
  const controls = ['source','language','microphone','noise'];
  // Bound pending local capture to five seconds of 24 kHz PCM16 in 200 ms chunks.
  const maxPendingInputChunks = 25, maxPendingInputBytes = 24000 * 2 * 5;
  let generation = 0, current = null, state = 'ready';
  let chunks = [], peaks = [], segments = [], outputText = [], originalText = [];
  let sampleCount = 0, nextStart = 0, outputContext = null, outputGain = null, recordingURL = '';
  let complete = false, outputRate = 24000, outputChannels = 1;
  let peakFrames = 0, peakValue = 0, framesPerPeak = 240, playedSegment = 0;

  const status = value => { $('status').textContent = value; };
  const showError = message => { $('error').textContent = message; $('error').hidden = !message; };
  const time = value => `${String(Math.floor(value / 60)).padStart(2,'0')}:${String(Math.floor(value % 60)).padStart(2,'0')}.${String(Math.floor(value * 1000) % 1000).padStart(3,'0')}`;
  const endpoint = (route, id) => route + (id ? '?id=' + encodeURIComponent(id) : '');
  async function request(route, options = {}, received) {
    const response = await fetch(route, {...options, cache:'no-store', headers:{'X-OpenAI-Audio':token,...options.headers}});
    received?.(response);
    if (!response.ok) throw new Error(await response.text());
    return response;
  }
  function setState(value) {
    state = value;
    const busy = value === 'starting' || value === 'canceling' || value === 'running' || value === 'finishing';
    for (const id of controls) $(id).disabled = busy;
    $('start').textContent = value === 'starting' ? 'Cancel' : value === 'running' ? 'Stop session' : value === 'finishing' ? 'Stop now' : 'Start session';
    $('start').disabled = !token || value === 'canceling' || value === 'disconnected';
    $('mute').hidden = value !== 'running';
    $('clear').disabled = busy || value === 'disconnected';
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
    $('microphone').disabled = tab || state === 'starting' || state === 'canceling' || state === 'running' || state === 'finishing';
  }
  async function stopOutput() {
    const context = outputContext;
    outputContext = null; outputGain = null; segments = []; playedSegment = 0;
    if (context && context.state !== 'closed') await context.close();
  }
  function resetRecording(revoke = true) {
    outputRate = 24000; outputChannels = 1; peakFrames = 0; peakValue = 0; framesPerPeak = 240;
    chunks = []; peaks = []; outputText = []; originalText = []; sampleCount = 0; nextStart = 0; complete = false;
    $('original').replaceChildren(); $('translated').replaceChildren(); $('usage').textContent = '';
    $('download').disabled = true; $('transcript').disabled = true; $('playback').disabled = true;
    $('player').pause(); $('player').removeAttribute('src'); $('player').load(); $('player').hidden = true;
    if (revoke && recordingURL) URL.revokeObjectURL(recordingURL);
    recordingURL = '';
  }
  function retainRecording() {
    if (!sampleCount && !outputText.length && !originalText.length) return null;
    return {chunks,peaks,outputText,originalText,sampleCount,outputRate,outputChannels,
      peakFrames,peakValue,framesPerPeak,complete,recordingURL,position:playbackPosition(),usage:$('usage').textContent};
  }
  function replaceRecording(run) {
    if (run.hasOutput) return;
    run.hasOutput = true;
    if (run.previous?.recordingURL) URL.revokeObjectURL(run.previous.recordingURL);
    run.previous = null;
  }
  function restoreRecording(run) {
    if (!run.replaced || run.hasOutput || !run.previous) return false;
    const old = run.previous;
    ({chunks,peaks,outputText,originalText,sampleCount,outputRate,outputChannels,
      peakFrames,peakValue,framesPerPeak,complete,recordingURL} = old);
    $('original').replaceChildren(document.createTextNode(originalText.join('')));
    $('translated').replaceChildren(document.createTextNode(outputText.join('')));
    $('usage').textContent = old.usage;
    if (recordingURL) $('player').src = recordingURL;
    $('player').currentTime = old.position;
    $('download').disabled = !sampleCount; $('playback').disabled = !sampleCount;
    $('transcript').disabled = !(outputText.length || originalText.length);
    run.previous = null;
    return !!(sampleCount || outputText.length || originalText.length);
  }
  function releaseCapture(run) {
    for (const track of run.stream?.getTracks() || []) track.stop();
    run.node?.disconnect();
    run.input?.disconnect();
    if (run.context && run.context.state !== 'closed') run.context.close().catch(() => {});
    if (run.output && run.output !== outputContext && run.output.state !== 'closed') run.output.close().catch(() => {});
  }
  async function start() {
    showError('');
    if (!navigator.mediaDevices || !window.AudioWorkletNode) { showError('This browser cannot capture live audio. Try Chrome or Edge.'); return; }
    const run = {generation:++generation, controller:new AbortController(), stream:null, queue:Promise.resolve(), pendingInputs:0, pendingInputBytes:0, sequence:0, id:'', failed:false};
    run.previous = retainRecording();
    $('player').pause();
    current = run;
    setState('starting'); status('Choose your audio source');
    try {
      await stopOutput();
      if (current !== run) return;
      const tab = $('source').value === 'tab';
      if (tab) {
        if (!navigator.mediaDevices.getDisplayMedia) throw new Error('This browser cannot share tab audio. Try Chrome or Edge.');
        run.stream = await navigator.mediaDevices.getDisplayMedia({video:true,audio:true});
      } else {
        run.stream = await navigator.mediaDevices.getUserMedia({audio:{deviceId:$('microphone').value ? {exact:$('microphone').value} : undefined,channelCount:1,echoCancellation:true,noiseSuppression:$('noise').checked}});
      }
      if (current !== run) { releaseCapture(run); return; }
      if (!run.stream.getAudioTracks().length) throw new Error('No audio was shared. Choose a browser tab and enable Share tab audio.');
      const ended = () => {
        if (current !== run || run.failed) return;
        if (state === 'starting') showError('Audio capture ended before translation started. Share audio or reconnect your microphone, then start again.');
        if (state === 'starting' || state === 'running') stop().catch(error => fail(run,error));
      };
      for (const track of run.stream.getTracks()) track.addEventListener('ended',ended,{once:true});
      if (run.stream.getTracks().some(track => track.readyState === 'ended')) { ended(); return; }
      devices().catch(() => {});
      run.context = new AudioContext({sampleRate:24000});
      if (run.context.sampleRate !== 24000) throw new Error('This browser cannot capture the required audio format. Try Chrome or Edge.');
      await run.context.audioWorklet.addModule('/capture-worklet.js');
      await run.context.resume();
      if (current !== run) { releaseCapture(run); return; }
      const playback = new AudioContext(); run.output = playback;
      const gain = playback.createGain(); gain.connect(playback.destination);
      gain.gain.value = $('listen').checked ? 1 : 0;
      if ($('output').value && playback.setSinkId) await playback.setSinkId($('output').value);
      await playback.resume();
      if (current !== run) { releaseCapture(run); return; }
      status('Connecting');
      run.startSent = true;
      const response = await request('/start',{method:'POST',signal:run.controller.signal,headers:{'Content-Type':'application/json'},body:JSON.stringify({language:$('language').value,noise_reduction:$('noise').checked})},response => { run.startRejected = !response.ok; });
      // Acquire the identity before awaiting the response body so Cancel can release it.
      run.id = response.headers.get('X-OpenAI-Audio-Session');
      if (!run.id) {
        run.cleanupUnconfirmed = true;
        throw new Error('The local translation page could not finish connecting. Reopen live translation from the CLI.');
      }
      if (current !== run) { releaseCapture(run); request(endpoint('/stop',run.id) + '&abort=1',{method:'POST'}).catch(() => {}); return; }
      let accepted;
      try { accepted = await response.json(); } catch (_) { throw new Error('The local translation page could not finish connecting. Start a new session.'); }
      if (accepted?.id !== run.id) throw new Error('The local translation page could not finish connecting. Start a new session.');
      if (current !== run) { releaseCapture(run); request(endpoint('/stop',run.id) + '&abort=1',{method:'POST'}).catch(() => {}); return; }
      if (run.stream.getTracks().some(track => track.readyState === 'ended')) { ended(); return; }
      resetRecording(false); run.replaced = true;
      outputContext = playback; outputGain = gain;
      run.input = run.context.createMediaStreamSource(run.stream);
      run.node = new AudioWorkletNode(run.context,'translation-capture');
      run.node.port.onmessage = ({data}) => {
        if (data === 'flushed') { run.flushed?.(); return; }
        if (current !== run || run.failed || run.closed || run.controller.signal.aborted) return;
        if (run.pendingInputs >= maxPendingInputChunks || run.pendingInputBytes + data.byteLength > maxPendingInputBytes) {
          fail(run,new Error('Audio capture stopped because the connection cannot keep up. Some input was not sent. Received audio remains available. Check the connection. Start a new session.'));
          return;
        }
        run.pendingInputs++; run.pendingInputBytes += data.byteLength;
        run.queue = run.queue.then(() => { if (!run.closed && !run.failed) return request(endpoint('/input',run.id) + '&sequence=' + run.sequence++,{method:'POST',signal:run.controller.signal,body:data}); })
          .catch(error => { if (!run.closed) return fail(run,error); })
          .finally(() => { run.pendingInputs--; run.pendingInputBytes -= data.byteLength; });
      };
      run.input.connect(run.node);
      // A zero-gain sink keeps capture processing active without monitoring source audio.
      run.sink = run.context.createGain(); run.sink.gain.value = 0;
      run.node.connect(run.sink); run.sink.connect(run.context.destination);
      $('mute').textContent = 'Mute input';
      setState('running'); status('Translating');
      readEvents(run).catch(error => { if (!run.closed || error.name !== 'AbortError') return fail(run,error); });
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
          await receive(run,event);
        }
      }
      if (start < value.length) pending.push(value.slice(start));
    }
    if (current === run && !run.closed && !complete && !run.failed) await fail(run,new Error('The connection ended before translation finished. Received audio remains available.'));
  }
  async function receive(run,event) {
    if (current !== run || run.failed) return;
    if (event.type === 'session.input_transcript.delta' && event.delta) { originalText.push(event.delta); $('original').append(document.createTextNode(event.delta)); $('transcript').disabled = false; }
    if (event.type === 'session.output_transcript.delta' && event.delta) { replaceRecording(run); outputText.push(event.delta); $('translated').append(document.createTextNode(event.delta)); $('transcript').disabled = false; }
    if (event.type === 'session.output_audio.delta') playChunk(run,event);
    if (event.tokens) $('usage').textContent = event.tokens.toLocaleString() + ' tokens';
    if (event.type === 'error') fail(run,new Error(event.message));
    if (event.type === 'session.closed') {
      run.closed = true; releaseCapture(run); run.controller.abort();
      setState('canceling'); status('Finishing translation');
      try { await cancelSession(run); }
      catch (error) { run.cleanupUnconfirmed = true; await fail(run,error); return; }
      if (current !== run) return;
      if (!run.hasOutput) stopOutput().catch(() => {});
      const restored = restoreRecording(run);
      if (!restored) { complete = true; prepareRecording(); }
      setState('ready'); updateSource(); status(restored ? 'No translated output. Previous recording kept.' : run.hasOutput ? 'Finished' : 'No translated output.');
      run.controller.abort();
    }
  }
  function playChunk(run,event) {
    const rate = event.sample_rate || 24000, channels = event.channels || 1;
    if ((event.format && event.format !== 'pcm16') || !Number.isInteger(rate) || rate <= 0 || !Number.isInteger(channels) || channels < 1) throw new Error('The service returned an unsupported audio format. Received audio remains available.');
    if (sampleCount && (rate !== outputRate || channels !== outputChannels)) throw new Error('The audio format changed during translation. Received audio remains available.');
    const bytes = Uint8Array.from(atob(event.delta), c => c.charCodeAt(0));
    if (!bytes.length) return;
    if (bytes.length % (2 * channels)) throw new Error('The service returned incomplete audio. Received audio remains available.');
    const view = new DataView(bytes.buffer), frames = bytes.length / (2 * channels);
    let buffer;
    try { buffer = outputContext.createBuffer(channels,frames,rate); }
    catch (_) { throw new Error('The browser cannot decode this audio format. Start again with another browser.'); }
    replaceRecording(run);
    outputRate = rate; outputChannels = channels;
    if (!sampleCount) framesPerPeak = Math.max(1,Math.round(rate / 100));
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
    const offset = sampleCount / outputRate; sampleCount += frames;
    try {
      const source = outputContext.createBufferSource(); source.buffer = buffer; source.connect(outputGain);
      const start = Math.max(outputContext.currentTime + .025,nextStart);
      source.start(start);
      segments.push({start,duration:buffer.duration,offset}); nextStart = start + buffer.duration;
    } catch (_) { throw new Error('Audio was received, but playback failed. Save the recording or choose another output device.'); }
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
    $('player').src = recordingURL; $('download').disabled = false; $('playback').disabled = false;
  }
  async function waitForCapture(run,pending,milliseconds,message) {
    let timer, interrupted;
    try {
      await Promise.race([pending,new Promise((_,reject) => {
        interrupted = () => reject(new DOMException('aborted','AbortError'));
        if (run.controller.signal.aborted) { interrupted(); return; }
        run.controller.signal.addEventListener('abort',interrupted,{once:true});
        timer = setTimeout(() => reject(new Error(message)),milliseconds);
      })]);
    } finally {
      if (timer !== undefined) clearTimeout(timer);
      run.controller.signal.removeEventListener('abort',interrupted);
    }
  }
  async function stop() {
    const run = current;
    if (!run) return;
    if (state === 'starting') {
      setState('canceling'); status('Canceling connection');
      current = null; generation++; run.controller.abort(); releaseCapture(run); restoreRecording(run);
      try { await cancelSession(run); }
      catch (error) { showError(error.message); setState('disconnected'); updateSource(); status('Stopped'); return; }
      await stopOutput();
      setState('ready'); updateSource(); status('Ready'); return;
    }
    if (state !== 'running') return;
    setState('finishing'); status('Finishing translation');
    try {
      await waitForCapture(run,new Promise(resolve => {
        run.flushed = resolve;
        run.node.port.postMessage('flush');
      }),500,'Audio capture did not finish sending. The final input may be incomplete. Received audio remains available.');
      releaseCapture(run);
      await waitForCapture(run,run.queue,2000,'Audio upload did not finish. The final input may be incomplete. Received audio remains available. Check the connection. Start a new session.');
      if (current === run && !run.failed && !run.closed) await request(endpoint('/stop',run.id),{method:'POST',signal:run.controller.signal});
    } catch (error) { if (!run.closed) await fail(run,error); }
    finally { run.flushed = null; }
  }
  async function cancelSession(run) {
    const message = 'Could not confirm that the connection stopped. Reopen live translation from the CLI.';
    if (!run.id) {
      if (run.startSent && !run.startRejected) throw new Error(message);
      return;
    }
    const controller = new AbortController();
    try {
      await waitForCapture({controller},request(endpoint('/stop',run.id) + '&abort=1',{method:'POST',signal:controller.signal}),2000,'');
    } catch (_) {
      throw new Error(message);
    } finally { controller.abort(); }
  }
  async function fail(run,error) {
    if (current !== run || run.failed) return;
    const starting = state === 'starting';
    run.failed = true; releaseCapture(run);
    run.controller.abort();
    let cleanupFailed = !!run.cleanupUnconfirmed;
    setState('canceling'); status('Stopping translation');
    if (!cleanupFailed) {
      try { await cancelSession(run); }
      catch (cleanupError) { error = cleanupError; cleanupFailed = true; }
    }
    if (current !== run) return;
    // Local audio cleanup may still be pending; Cancel can dismiss it once the session is released.
    if (starting && !cleanupFailed) setState('starting');
    if (!run.hasOutput) {
      try { await stopOutput(); }
      catch (_) {
        error = new Error('Could not finish closing audio. Received audio and captions remain available. Reopen live translation from the CLI.');
        cleanupFailed = true;
      }
    }
    if (current !== run) return;
    if (!restoreRecording(run)) { try { prepareRecording(); } catch (_) {} }
    showError(error.message || 'Translation stopped. Start a new session.'); status('Stopped'); setState(cleanupFailed ? 'disconnected' : 'ready'); updateSource();
  }
  function save(blob,extension) {
    const name = outputText.join('').normalize('NFKD').replace(/[\u0300-\u036f]/g,'').toLowerCase().replace(/[^\p{L}\p{N}]+/gu,'-').replace(/^-|-$/g,'').split('-').slice(0,8).join('-') || 'translation';
    const filename = Array.from(name).slice(0,55).join('');
    const link = document.createElement('a'), url = URL.createObjectURL(blob);
    link.href = url; link.download = 'translation-' + filename + '.' + extension; link.click(); setTimeout(() => URL.revokeObjectURL(url),1000);
  }
  function playbackPosition() {
    if (!outputContext) return $('player').currentTime || 0;
    while (playedSegment+1 < segments.length && outputContext.currentTime >= segments[playedSegment+1].start) playedSegment++;
    const segment = segments[playedSegment];
    return segment ? segment.offset + Math.max(0,Math.min(segment.duration,outputContext.currentTime-segment.start)) : 0;
  }
  async function togglePlayback() {
    if (!recordingURL) return;
    if (outputContext && outputContext.currentTime < nextStart) {
      const position = playbackPosition(); $('player').currentTime = position; await stopOutput(); return;
    }
    if (!$('player').paused) { $('player').pause(); return; }
    if ($('player').currentTime >= sampleCount/outputRate - .001) $('player').currentTime = 0;
    try { await $('player').play(); } catch (_) { showError('Press Play again to allow audio playback.'); }
  }
  function draw() {
    const canvas = $('waveform'), ratio = devicePixelRatio || 1, width = canvas.clientWidth;
    if (canvas.width !== Math.round(width*ratio)) canvas.width = Math.round(width*ratio);
    if (canvas.height !== 120*ratio) canvas.height = 120*ratio;
    const context = canvas.getContext('2d'); context.setTransform(ratio,0,0,ratio,0,0); context.clearRect(0,0,width,120);
    const position = playbackPosition();
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
    canvas.tabIndex = recordingURL ? 0 : -1;
    const playing = outputContext ? outputContext.currentTime < nextStart : !$('player').paused;
    $('playback').textContent = playing ? 'Pause' : duration && position >= duration - .001 ? 'Replay' : 'Play';
    canvas.setAttribute('aria-valuenow',position.toFixed(2)); canvas.setAttribute('aria-valuemax',duration.toFixed(2));
    requestAnimationFrame(draw);
  }
  $('start').onclick = () => state === 'canceling' || state === 'disconnected' ? undefined : state === 'finishing' ? fail(current,new Error('Translation stopped before completion. Received audio remains available.')) : state === 'starting' || state === 'running' ? stop() : start();
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
    if (!recordingURL) return;
    $('player').pause(); stopOutput().catch(() => {});
    const box = $('waveform').getBoundingClientRect();
    $('player').currentTime = Math.max(0,Math.min(sampleCount/outputRate,(event.clientX-box.left)/box.width*sampleCount/outputRate));
  };
  $('waveform').onkeydown = event => {
    if (!recordingURL) return;
    let position = playbackPosition();
    if (event.key === 'ArrowLeft') position -= .5;
    else if (event.key === 'ArrowRight') position += .5;
    else if (event.key === 'Home') position = 0;
    else if (event.key === 'End') position = sampleCount/outputRate;
    else if (event.key === ' ') { event.preventDefault(); togglePlayback(); return; }
    else return;
    event.preventDefault(); $('player').pause(); stopOutput().catch(() => {}); $('player').currentTime = Math.max(0,Math.min(sampleCount/outputRate,position));
  };
  $('player').onplay = () => stopOutput().catch(() => {});
  $('playback').onclick = togglePlayback;
  $('download').onclick = () => { try { save(wavBlob(),'wav'); } catch (error) { showError(error.message); } };
  $('transcript').onclick = () => save(new Blob(['Original\n',originalText.join(''),'\n\nTranslation\n',outputText.join('')],{type:'text/plain;charset=utf-8'}),'txt');
  $('clear').onclick = async () => {
    if (state === 'running' || state === 'starting' || state === 'canceling' || state === 'finishing' || state === 'disconnected') return;
    const clearing = ++generation; current = null;
    try { await stopOutput(); }
    catch (_) { if (generation === clearing) showError('Could not finish clearing audio. Try Clear again.'); return; }
    if (generation !== clearing) return;
    resetRecording(); showError(''); status('Ready');
  };
  window.addEventListener('pagehide',() => { const run = current; current = null; if (run) { run.controller.abort(); releaseCapture(run); if (state === 'starting' && run.id) request(endpoint('/stop',run.id) + '&abort=1',{method:'POST',keepalive:true}).catch(() => {}); } stopOutput().catch(() => {}); });
  if (!token) showError('Open live translation from the CLI to connect this page.');
  setState('ready'); devices().catch(() => {}); draw();
})();
