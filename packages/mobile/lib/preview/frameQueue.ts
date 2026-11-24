// The browser stream's frames on their way to the screen: one decoding, and
// at most the newest one waiting behind it. A frame that arrives while another
// decodes replaces the one waiting, so a fast page on a slow phone drops frames
// rather than queueing them, and memory stays bounded.
export class FrameQueue<T> {
	private decoding = false;
	private waiting: { frame: T } | null = null;
	private droppedFrames = 0;

	constructor(private readonly show: (frame: T) => void) {}

	push(frame: T): void {
		if (!this.decoding) {
			this.decoding = true;
			this.show(frame);
			return;
		}
		if (this.waiting) this.droppedFrames++;
		this.waiting = { frame };
	}

	/** The frame shown last has decoded: show the newest one waiting, if any. */
	decoded(): void {
		const next = this.waiting;
		this.waiting = null;
		if (!next) {
			this.decoding = false;
			return;
		}
		this.show(next.frame);
	}

	/** A new stream: whatever was decoding or waiting belongs to the old one. */
	reset(): void {
		this.decoding = false;
		this.waiting = null;
	}

	pending(): number {
		return this.waiting ? 1 : 0;
	}

	dropped(): number {
		return this.droppedFrames;
	}
}
