import { beforeEach, describe, expect, it, vi } from "vitest";
import { PerformanceAccumulator, discardPerformanceWindow, percentile, renderDuration } from "./performance-telemetry";

type Capture = (event: string, properties: Record<string, unknown>) => Promise<void>;

describe("PerformanceAccumulator", () => {
	let now = 0;
	let capture: ReturnType<typeof vi.fn<Capture>>;
	let resources: { rendererPeakMb: number; appPeakMb: number; cpuAvgPct: number } | null;
	let openSessions: number | undefined;
	const create = () => new PerformanceAccumulator(localStorage, () => now, capture, async () => resources, () => openSessions, () => 2);
	const flushed = () => capture.mock.calls.at(-1)?.[1] ?? {};

	beforeEach(() => {
		localStorage.clear();
		now = 1_000;
		resources = null;
		openSessions = 5;
		capture = vi.fn<Capture>().mockResolvedValue(undefined);
	});

	it("buckets switch usable times by cached and uncached, with a visible p75", async () => {
		const accumulator = create();
		accumulator.activity();
		for (const usableMs of [40, 149, 150, 900]) accumulator.recordOpen({ isSwitch: true, cached: true, chatLoaded: false, visibleMs: 20, usableMs });
		accumulator.recordOpen({ isSwitch: true, cached: false, chatLoaded: true, visibleMs: 80, usableMs: 1_200 });
		now += 10_000;
		await accumulator.flush();

		expect(capture).toHaveBeenCalledWith("ao.usage.ui_performance", expect.any(Object));
		expect(flushed()).toMatchObject({
			switch_cached_lt150_count: 2,
			switch_cached_lt500_count: 1,
			switch_cached_lt1000_count: 1,
			switch_cached_ge1000_count: 0,
			switch_uncached_ge1000_count: 1,
			switch_visible_p75_ms: 20,
			chat_open_count: 1,
			chat_open_p75_ms: 1_200,
			switch_dropped_count: 0,
		});
	});

	it("drops switches over 30 seconds and counts them", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.recordOpen({ isSwitch: true, cached: true, chatLoaded: false, visibleMs: 10, usableMs: 31_000 });
		accumulator.recordDroppedOpen(true);
		accumulator.recordDroppedOpen(false);
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ switch_dropped_count: 2, switch_cached_ge1000_count: 0 });
	});

	it("does not time an open from another page as a switch", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.recordOpen({ isSwitch: false, cached: false, chatLoaded: true, visibleMs: 50, usableMs: 300 });
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ switch_uncached_lt500_count: 0, chat_open_count: 1, chat_open_p75_ms: 300 });
		expect(flushed()).not.toHaveProperty("switch_visible_p75_ms", expect.any(Number));
	});

	it("counts each slow interaction once and reports the INP-style p98", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.addInteractionCount(120);
		// One interaction reports several entries (pointerdown, pointerup, click).
		accumulator.recordInteraction(7, 180);
		accumulator.recordInteraction(7, 260);
		accumulator.recordInteraction(7, 240);
		accumulator.recordInteraction(8, 640);
		accumulator.recordInteraction(9, 90);
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({
			interaction_count: 120,
			slow_interaction_count: 2,
			very_slow_interaction_count: 1,
			// 120 interactions skip the two longest: 640, 260, then 90.
			interaction_p98_ms: 90,
		});
	});

	it("keeps only the ten longest interactions", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.addInteractionCount(10);
		for (let id = 1; id <= 15; id++) accumulator.recordInteraction(id, id * 10);
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ interaction_p98_ms: 150 });
		const state = JSON.parse(localStorage.getItem("ao.telemetry.performance.v1") ?? "{}");
		expect(state.summary.longest).toEqual([]);
	});

	it("sums long frame blocking time and names the cause that blocked longest", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.recordLongFrame(40, [{ invokerType: "event-listener", duration: 70 }, { invokerType: "user-callback", duration: 20 }]);
		accumulator.recordLongFrame(30, [{ invokerType: "user-callback", duration: 60 }, { invokerType: "https://example.com/app.js", duration: 5 }]);
		accumulator.recordLongFrame(10, []);
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ long_frame_count: 3, long_frame_blocking_ms: 80, long_frame_top_cause: "user-callback" });
	});

	it("names rendering when style, layout and paint outweigh scripts", async () => {
		const accumulator = create();
		accumulator.activity();
		// Frames with no script long enough to be credited: rendering work only.
		accumulator.recordLongFrame(10, [], 55);
		accumulator.recordLongFrame(12, [], 58);
		accumulator.recordLongFrame(30, [{ invokerType: "event-listener", duration: 70 }], 8);
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ long_frame_count: 3, long_frame_top_cause: "rendering", measurement_schema_version: 2 });
	});

	it("puts a frame's uncredited time under other, so a sliver of rendering cannot win", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.recordLongFrame(50, [], 2, 100);
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ long_frame_top_cause: "other" });
	});

	it("never reports a window shorter than its active time after the clock is set back", async () => {
		const accumulator = create();
		accumulator.activity();
		now += 240_000;
		accumulator.pause();
		now = 500;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ active_seconds: 240, window_seconds: 240 });
	});

	it("drops a window saved by the version 1 format", async () => {
		const first = create();
		first.activity();
		first.recordOpen({ isSwitch: true, cached: true, chatLoaded: false, visibleMs: 20, usableMs: 100 });
		now += 60_000;
		first.pause();
		const saved = JSON.parse(localStorage.getItem("ao.telemetry.performance.v1") ?? "{}");
		localStorage.setItem("ao.telemetry.performance.v1", JSON.stringify({ ...saved, version: 1 }));

		now += 61 * 60_000;
		const second = create();
		expect(second.isFlushOverdue()).toBe(false);
		await second.flush();
		expect(capture).not.toHaveBeenCalled();
	});

	it("adds memory, CPU, open session and uptime buckets", async () => {
		resources = { rendererPeakMb: 700, appPeakMb: 2_100, cpuAvgPct: 3.4 };
		openSessions = 12;
		const accumulator = create();
		accumulator.activity();
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({
			renderer_mb_bucket: "lt1024",
			app_mb_bucket: "ge2048",
			app_cpu_avg_pct: 3.4,
			open_sessions_bucket: "11-30",
			uptime_hours_bucket: "lt8",
		});
	});

	it("counts active time only up to five idle minutes", async () => {
		const accumulator = create();
		accumulator.activity();
		now += 20 * 60_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ active_seconds: 300, window_seconds: 1_200, flush_reason: "interval" });
	});

	it("ends an active stretch at the idle limit when input returns after a gap", async () => {
		const accumulator = create();
		accumulator.activity();
		now += 5 * 60_000;
		accumulator.checkpoint();
		now += 2 * 60_000;
		accumulator.activity();
		now += 3 * 60_000;
		await accumulator.flush();

		// Minutes 0 to 5, then 7 to 10; the idle minutes 5 to 7 do not count.
		expect(flushed()).toMatchObject({ active_seconds: 480, window_seconds: 600 });
	});

	it("sends nothing for a window without use, and starts a new window", async () => {
		const accumulator = create();
		now += 10_000;
		await accumulator.flush();

		expect(capture).not.toHaveBeenCalled();
		const state = JSON.parse(localStorage.getItem("ao.telemetry.performance.v1") ?? "{}");
		expect(state.windowStartedAt).toBe(now);
	});

	it("keeps the window across a restart and flushes it on startup when overdue", async () => {
		const first = create();
		first.activity();
		first.recordOpen({ isSwitch: true, cached: true, chatLoaded: false, visibleMs: 20, usableMs: 100 });
		now += 60_000;
		first.pause();

		now += 61 * 60_000;
		const second = create();
		expect(second.isFlushOverdue()).toBe(true);
		await second.flush("startup");

		// The 61 minutes the app was closed are not part of the window.
		expect(flushed()).toMatchObject({ flush_reason: "startup", switch_cached_lt150_count: 1, active_seconds: 60, window_seconds: 60 });
	});

	it("sends nothing counted before an opt-out once the window is discarded", async () => {
		const before = create();
		before.activity();
		before.recordOpen({ isSwitch: true, cached: true, chatLoaded: false, visibleMs: 20, usableMs: 100 });
		now += 60_000;
		before.pause();
		discardPerformanceWindow(localStorage);

		const after = create();
		after.activity();
		now += 10_000;
		await after.flush();

		expect(flushed()).toMatchObject({ switch_cached_lt150_count: 0, active_seconds: 10 });
	});

	it("replaces malformed stored state", async () => {
		localStorage.setItem("ao.telemetry.performance.v1", JSON.stringify({ version: 1, windowStartedAt: 1, windowFlushAt: 2, summary: { longest: [{ ms: "/Users/someone" }] } }));
		const accumulator = create();
		expect(accumulator.isFlushOverdue()).toBe(false);
	});
});

describe("percentile", () => {
	it("uses the nearest rank", () => {
		expect(percentile([], 0.75)).toBeUndefined();
		expect(percentile([10], 0.75)).toBe(10);
		expect(percentile([40, 10, 30, 20], 0.75)).toBe(30);
	});
});

describe("renderDuration", () => {
	it("measures from style and layout to the end of the frame, not from requestAnimationFrame", () => {
		// A 120 ms frame: rendering starts at 40 with a requestAnimationFrame script, style and layout at 90.
		expect(renderDuration({ startTime: 0, duration: 120, styleAndLayoutStart: 90 })).toBe(30);
	});

	it("is 0 for a frame that did not render", () => {
		expect(renderDuration({ startTime: 0, duration: 80, styleAndLayoutStart: 0 })).toBe(0);
		expect(renderDuration({ startTime: 0, duration: 80 })).toBe(0);
	});
});
