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
		stats.received(1_300, 10_000);
		stats.shown(1_340, 4);
		stats.shown(1_400, 4);
		expect(stats.snapshot(1_400).firstFrameMs).toBe(1_240);
	});

	it("restarts the first-frame clock on a reconnect", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		stats.shown(500, 1);
		stats.connecting(2_000);
		stats.shown(2_300, 1);
		expect(stats.snapshot(2_300).firstFrameMs).toBe(300);
	});

	it("counts frames shown per second and kilobytes received per second over the last second", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		// An old frame, outside the window.
		stats.received(100, 50_000);
		stats.shown(100, 2);
		for (let i = 0; i < 20; i++) {
			stats.received(2_000 + i * 50, 5_120);
			stats.shown(2_000 + i * 50, 2);
		}
		const s = stats.snapshot(3_000);
		expect(s.fps).toBe(20);
		expect(s.kbps).toBe(100);
		expect(s.frames).toBe(21);
	});

	it("counts the bytes of frames replaced before they were shown", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		// Two frames arrive before the image shows either: both cost bandwidth.
		stats.received(1_000, 10_240);
		stats.received(1_010, 10_240);
		stats.shown(1_050, 30);
		const s = stats.snapshot(1_100);
		expect(s.kbps).toBe(20);
		expect(s.fps).toBe(1);
		expect(s.decodeMs).toBe(30);
	});

	it("counts every frame in the last second, however many there are", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		// 120 frames of 1 KB within one second.
		for (let i = 0; i < 120; i++) {
			stats.received(1_000 + i * 8, 1_024);
			stats.shown(1_000 + i * 8, 1);
		}
		const s = stats.snapshot(1_999);
		expect(s.kbps).toBe(120);
		expect(s.fps).toBe(120);
	});

	it("reports the median decode time of recent frames", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		[3, 9, 5].forEach((ms, i) => stats.shown(10 + i, ms));
		expect(stats.snapshot(20).decodeMs).toBe(5);
	});

	it("measures input to the next frame, once per input", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		stats.input(1_000);
		stats.input(1_020); // a second input before a frame: the first one times it
		stats.shown(1_120, 1);
		stats.shown(1_200, 1); // no input pending: not a latency sample
		stats.input(2_000);
		stats.shown(2_080, 1);
		expect(stats.snapshot(2_100).inputToFrameMs).toBe(100);
	});

	it("keeps a bounded history", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		for (let i = 0; i < 10_000; i++) {
			stats.received(i, 1);
			stats.shown(i, 1);
		}
		expect(stats.retained()).toBeLessThanOrEqual(256);
	});

	it("formats one HUD line", () => {
		const stats = new StreamStats();
		stats.connecting(0);
		stats.input(900);
		for (let i = 0; i < 10; i++) {
			stats.received(1_000 + i * 100, 2_048);
			stats.shown(1_000 + i * 100, 3);
		}
		expect(stats.line(1_950)).toBe("first 1000ms · 10fps · 20KB/s · decode 3ms · input 100ms");
	});
});
