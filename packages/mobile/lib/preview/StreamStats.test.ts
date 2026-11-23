import { describe, expect, it } from "vitest";
import { StreamStats } from "./StreamStats";

describe("StreamStats", () => {
	it("reports nothing before the stream connects", () => {
		const stats = new StreamStats();
		expect(stats.snapshot(1_000)).toEqual({ firstFrameMs: null, fps: 0, kbps: 0, decodeMs: null, inputToFrameMs: null, frames: 0 });
	});

	it("times the first frame from connecting", () => {
		const stats = new StreamStats();
		stats.connecting(100);
		stats.frame(1_340, 10_000, 4);
		stats.frame(1_400, 10_000, 4);
		expect(stats.snapshot(1_400).firstFrameMs).toBe(1_240);
	});

	it("restarts the first-frame clock on a reconnect", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		stats.frame(500, 1, 1);
		stats.connecting(2_000);
		stats.frame(2_300, 1, 1);
		expect(stats.snapshot(2_300).firstFrameMs).toBe(300);
	});

	it("counts frames per second and kilobytes per second over the last second", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		// An old frame, outside the window.
		stats.frame(100, 50_000, 2);
		for (let i = 0; i < 20; i++) stats.frame(2_000 + i * 50, 5_120, 2);
		const s = stats.snapshot(3_000);
		expect(s.fps).toBe(20);
		expect(s.kbps).toBe(100);
		expect(s.frames).toBe(21);
	});

	it("reports the median decode time of recent frames", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		[3, 9, 5].forEach((ms, i) => stats.frame(10 + i, 1, ms));
		expect(stats.snapshot(20).decodeMs).toBe(5);
	});

	it("measures input to the next frame, once per input", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		stats.input(1_000);
		stats.input(1_020); // a second input before a frame: the first one times it
		stats.frame(1_120, 1, 1);
		stats.frame(1_200, 1, 1); // no input pending: not a latency sample
		stats.input(2_000);
		stats.frame(2_080, 1, 1);
		expect(stats.snapshot(2_100).inputToFrameMs).toBe(100);
	});

	it("keeps a bounded history", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		for (let i = 0; i < 10_000; i++) stats.frame(i, 1, 1);
		expect(stats.retained()).toBeLessThanOrEqual(256);
	});

	it("formats one HUD line", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		stats.input(900);
		for (let i = 0; i < 10; i++) stats.frame(1_000 + i * 100, 2_048, 3);
		expect(stats.line(1_950)).toBe("first 1000ms · 10fps · 20KB/s · decode 3ms · input 100ms");
	});
});
