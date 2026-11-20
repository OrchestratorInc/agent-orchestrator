import { describe, expect, it } from "vitest";
import { memoryBucket, openSessionsBucket, sanitizePerformanceProperties, uptimeBucket } from "./performance-telemetry-properties";
import { sanitizeRendererProperties } from "./telemetry";

describe("sanitizePerformanceProperties", () => {
	it("keeps the launch fields, rounded and capped", () => {
		expect(sanitizePerformanceProperties("ao.launches.timed", {
			launch_kind: "update",
			to_visible_ms: 812.6,
			to_usable_ms: 9_999_999,
			has_cache: true,
			path: "/Users/someone/project",
		})).toEqual({ launch_kind: "update", to_visible_ms: 813, to_usable_ms: 600_000 });
	});

	it("sends no timings without a launch kind", () => {
		expect(sanitizePerformanceProperties("ao.launches.timed", { launch_kind: "reload", to_usable_ms: 900 })).toEqual({});
		expect(sanitizePerformanceProperties("ao.launches.timed")).toEqual({});
	});

	it("drops negative, non-finite and non-numeric timings", () => {
		expect(sanitizePerformanceProperties("ao.launches.timed", { launch_kind: "cold", to_visible_ms: -4, to_usable_ms: Number.NaN })).toEqual({ launch_kind: "cold" });
	});

	it("keeps only the documented summary properties and closed values", () => {
		const safe = sanitizePerformanceProperties("ao.usage.ui_performance", {
			measurement_schema_version: 2,
			flush_reason: "interval",
			window_seconds: 2_700,
			active_seconds: 1_200.4,
			switch_cached_lt150_count: 12,
			switch_uncached_ge1000_count: "3",
			interaction_p98_ms: 216,
			slowest_area: "composer",
			long_frame_top_cause: "event-listener",
			open_sessions_bucket: "4-10",
			renderer_mb_bucket: "lt512",
			app_mb_bucket: "lt1024",
			app_cpu_avg_pct: 2.345,
			uptime_hours_bucket: "lt24",
			session_id: "ses_123",
			script_url: "file:///app.js",
		});
		expect(safe).toEqual({
			measurement_schema_version: 2,
			flush_reason: "interval",
			window_seconds: 2_700,
			active_seconds: 1_200,
			switch_cached_lt150_count: 12,
			interaction_p98_ms: 216,
			long_frame_top_cause: "event-listener",
			open_sessions_bucket: "4-10",
			renderer_mb_bucket: "lt512",
			app_mb_bucket: "lt1024",
			app_cpu_avg_pct: 2.3,
			uptime_hours_bucket: "lt24",
		});
	});

	it("drops values outside the closed lists", () => {
		expect(sanitizePerformanceProperties("ao.usage.ui_performance", {
			measurement_schema_version: 1,
			flush_reason: "quit",
			slowest_area: "button.send",
			long_frame_top_cause: "renderSidebar",
			open_sessions_bucket: "5",
			renderer_mb_bucket: "700",
			uptime_hours_bucket: 3,
		})).toEqual({});
	});

	it("is what the renderer client applies to both events", async () => {
		expect(await sanitizeRendererProperties("ao.launches.timed", { launch_kind: "warm", to_usable_ms: 420, extra: 1 })).toEqual({ launch_kind: "warm", to_usable_ms: 420 });
		expect(await sanitizeRendererProperties("ao.usage.ui_performance", { long_frame_top_cause: "user-callback", element_text: "Send" })).toEqual({ long_frame_top_cause: "user-callback" });
		expect(await sanitizeRendererProperties("ao.usage.ui_performance", { long_frame_top_cause: "rendering" })).toEqual({ long_frame_top_cause: "rendering" });
		// ao.app.loaded is sent at init with no properties, as before.
		expect(await sanitizeRendererProperties("ao.app.loaded", { launch_kind: "warm", to_usable_ms: 420 })).toEqual({});
	});
});

describe("performance buckets", () => {
	it("buckets open sessions", () => {
		expect([0, 1, 3, 4, 10, 11, 30, 31].map(openSessionsBucket)).toEqual(["0", "1-3", "1-3", "4-10", "4-10", "11-30", "11-30", "31+"]);
	});

	it("buckets memory in MB", () => {
		expect([100, 256, 511, 1_023, 2_047, 2_048].map(memoryBucket)).toEqual(["lt256", "lt512", "lt512", "lt1024", "lt2048", "ge2048"]);
	});

	it("buckets uptime in hours", () => {
		expect([0.5, 1, 7.9, 23, 71, 72].map(uptimeBucket)).toEqual(["lt1", "lt8", "lt8", "lt24", "lt72", "ge72"]);
	});
});
