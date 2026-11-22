import { beforeEach, describe, expect, it, vi } from "vitest";
import { PerformanceAccumulator, discardPerformanceWindow, percentile } from "./performance-telemetry";

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

	it("splits a long frame into its phases and names the cause, entry point and bundle that took longest", async () => {
		const accumulator = create();
		accumulator.activity();
		// A 120 ms frame: tasks until 80, frame callbacks until 100, then style, layout and paint.
		accumulator.recordLongFrame({
			startTime: 0,
			duration: 120,
			blockingDuration: 70,
			renderStart: 80,
			styleAndLayoutStart: 100,
			scripts: [
				{ invokerType: "event-listener", invoker: "BUTTON#send.onclick", sourceURL: "app://renderer/assets/SessionView-BHqaR3Jk.js", startTime: 5, duration: 40, forcedStyleAndLayoutDuration: 10 },
				{ invokerType: "user-callback", invoker: "Window.requestAnimationFrame", sourceURL: "app://renderer/assets/index-AbCd_f12.js", startTime: 82, duration: 12 },
			],
		});
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({
			measurement_schema_version: 3,
			long_frame_count: 1,
			long_frame_blocking_ms: 70,
			long_frame_ms: 120,
			long_frame_max_ms: 120,
			long_frame_no_script_count: 0,
			long_frame_unlisted_ms: 40,
			long_frame_event_listener_ms: 30,
			long_frame_forced_layout_ms: 10,
			long_frame_user_callback_ms: 12,
			long_frame_frame_callbacks_ms: 8,
			long_frame_rendering_ms: 20,
			long_frame_top_cause: "unlisted",
			long_frame_top_invoker: "BUTTON.onclick",
			long_frame_top_bundle: "SessionView",
		});
	});

	it("names rendering when style, layout and paint outweigh the rest", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.recordLongFrame({ startTime: 0, duration: 60, renderStart: 2, styleAndLayoutStart: 5 });
		accumulator.recordLongFrame({ startTime: 100, duration: 70, renderStart: 104, styleAndLayoutStart: 110 });
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ long_frame_count: 2, long_frame_no_script_count: 2, long_frame_max_ms: 70, long_frame_rendering_ms: 115, long_frame_top_cause: "rendering" });
		expect(flushed()).not.toHaveProperty("long_frame_top_invoker");
	});

	it("keeps URLs and element details out of entry point and bundle names", async () => {
		const accumulator = create();
		accumulator.activity();
		accumulator.recordLongFrame({
			startTime: 0,
			duration: 100,
			scripts: [
				{ invokerType: "classic-script", invoker: "https://example.com/app.js", sourceURL: "https://example.com/app.js", duration: 50 },
				{ invokerType: "event-listener", invoker: "IMG[src=https://example.com/a.png].onload", sourceURL: "blob:app://renderer/1234", duration: 20 },
				{ invokerType: "a-future-type", duration: 25 },
			],
		});
		now += 10_000;
		await accumulator.flush();

		expect(flushed()).toMatchObject({ long_frame_classic_script_ms: 50, long_frame_event_listener_ms: 20, long_frame_unlisted_ms: 30, long_frame_top_invoker: "IMG.onload" });
		expect(flushed()).not.toHaveProperty("long_frame_top_bundle");
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

	it("drops a window saved by an earlier format", async () => {
		const first = create();
		first.activity();
		first.recordOpen({ isSwitch: true, cached: true, chatLoaded: false, visibleMs: 20, usableMs: 100 });
		now += 60_000;
		first.pause();
		const saved = JSON.parse(localStorage.getItem("ao.telemetry.performance.v1") ?? "{}");
		localStorage.setItem("ao.telemetry.performance.v1", JSON.stringify({ ...saved, version: 2 }));

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
