const MAX_REPLAY_BYTES = 64 * 1024;
const decode = (data: string) => Uint8Array.from(atob(data), (character) => character.charCodeAt(0));

/** Subscribe before loading the snapshot, then trim queued events at its byte cursor. */
export class TriageTerminalStream {
	private cursor: number | undefined;
	private replay = new Uint8Array();
	private queue: Array<{ bytes: Uint8Array; sequence: number }> = [];
	private queuedBytes = 0;
	private writer?: (bytes: Uint8Array) => void;

	attach(write: (bytes: Uint8Array) => void): void {
		this.writer = write;
		this.flush();
	}

	snapshot(data: string, sequence: number): void {
		this.replay = decode(data);
		this.cursor = sequence;
		this.flush();
	}

	push(data: string, sequence: number): void {
		const bytes = decode(data);
		this.queue.push({ bytes, sequence });
		this.queuedBytes += bytes.length;
		while (this.queuedBytes > MAX_REPLAY_BYTES) {
			const first = this.queue[0];
			const excess = Math.min(first.bytes.length, this.queuedBytes - MAX_REPLAY_BYTES);
			first.bytes = first.bytes.slice(excess);
			this.queuedBytes -= excess;
			if (!first.bytes.length) this.queue.shift();
		}
		this.flush();
	}

	private flush(): void {
		if (!this.writer || this.cursor === undefined) return;
		if (this.replay.length) this.writer(this.replay);
		this.replay = new Uint8Array();
		for (const chunk of this.queue.splice(0).sort((a, b) => a.sequence - b.sequence)) {
			if (chunk.sequence <= this.cursor) continue;
			const overlap = Math.max(0, this.cursor - (chunk.sequence - chunk.bytes.length));
			this.writer(chunk.bytes.slice(overlap));
			this.cursor = chunk.sequence;
		}
		this.queuedBytes = 0;
	}
}
