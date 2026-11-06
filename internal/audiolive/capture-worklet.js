class TranslationCapture extends AudioWorkletProcessor {
  constructor() {
    super();
    this.samples = new Int16Array(4800);
    this.offset = 0;
    this.stopped = false;
    this.port.onmessage = ({data}) => {
      if (data === 'flush') {
        if (this.offset) this.port.postMessage(this.samples.slice(0, this.offset).buffer);
        this.offset = 0;
        this.port.postMessage('flushed');
        this.stopped = true;
      }
    };
  }
  process(inputs) {
    if (this.stopped) return false;
    const channels = inputs[0];
    const length = channels[0]?.length || 128;
    for (let i = 0; i < length; i++) {
      let value = 0;
      for (const channel of channels) value += channel[i] || 0;
      value = Math.max(-1, Math.min(1, value / Math.max(1, channels.length)));
      this.samples[this.offset++] = Math.round(value * (value < 0 ? 32768 : 32767));
      if (this.offset === this.samples.length) {
        this.port.postMessage(this.samples.buffer, [this.samples.buffer]);
        this.samples = new Int16Array(4800);
        this.offset = 0;
      }
    }
    return true;
  }
}
registerProcessor('translation-capture', TranslationCapture);
