import { describe, expect, it } from "vitest";
import { FrameQueue } from "./frameQueue";

describe("FrameQueue", () => {
	it("shows the first frame at once", () => {
		const shown: string[] = [];
		const queue = new FrameQueue<string>((frame) => shown.push(frame));
		queue.push("a");
		expect(shown).toEqual(["a"]);
	});

	it("frame queue holds latest only", () => {
		const shown: string[] = [];
		const queue = new FrameQueue<string>((frame) => shown.push(frame));
		queue.push("a");
		// a is decoding: b and c arrive, only c is kept.
		queue.push("b");
		queue.push("c");
		expect(shown).toEqual(["a"]);
		expect(queue.pending()).toBe(1);
		queue.decoded();
		expect(shown).toEqual(["a", "c"]);
		expect(queue.dropped()).toBe(1);
	});

	it("goes idle when nothing is pending, and shows the next frame at once", () => {
		const shown: string[] = [];
		const queue = new FrameQueue<string>((frame) => shown.push(frame));
		queue.push("a");
		queue.decoded();
		expect(queue.pending()).toBe(0);
		queue.push("b");
		expect(shown).toEqual(["a", "b"]);
	});

	it("never holds more than one frame waiting, however fast frames come", () => {
		const queue = new FrameQueue<number>(() => undefined);
		for (let i = 0; i < 1_000; i++) queue.push(i);
		expect(queue.pending()).toBe(1);
		expect(queue.dropped()).toBe(998);
	});

	it("starts over after a reset, so a new stream's first frame shows at once", () => {
		const shown: string[] = [];
		const queue = new FrameQueue<string>((frame) => shown.push(frame));
		queue.push("a");
		queue.push("b");
		queue.reset();
		expect(queue.pending()).toBe(0);
		queue.push("c");
		expect(shown).toEqual(["a", "c"]);
	});
});
