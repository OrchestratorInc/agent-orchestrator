// Capture 50 ms mono blocks. AudioContext performs the input resampling to 16 kHz.
class DictationProcessor extends AudioWorkletProcessor {
	constructor() {
		super();
		this.block = new Float32Array(800);
		this.offset = 0;
		this.stopped = false;
		this.port.onmessage = () => {
			this.stopped = true;
			if (this.offset) this.port.postMessage(this.block.slice(0, this.offset));
			this.port.postMessage("flushed");
		};
	}
	process(inputs) {
		if (this.stopped) return false;
		const samples = inputs[0]?.[0];
		if (!samples) return true;
		for (const sample of samples) {
			this.block[this.offset++] = sample;
			if (this.offset === this.block.length) {
				this.port.postMessage(this.block, [this.block.buffer]);
				this.block = new Float32Array(800);
				this.offset = 0;
			}
		}
		return true;
	}
}
registerProcessor("ao-dictation", DictationProcessor);
