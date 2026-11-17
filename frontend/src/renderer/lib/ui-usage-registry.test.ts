import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { sanitizeRendererProperties } from "./telemetry";
import {
	CONTROL_STAT_KEYS,
	ERROR_KINDS,
	ERROR_PRESENTATIONS,
	sanitizeUiUsageProperties,
	SURFACE_OPENED_BY,
	SURFACE_STAT_KEYS,
	SWITCH_SOURCES,
	UI_CONTROLS,
	UI_FEATURES,
	UI_USAGE_EVENTS,
	USAGE_SUMMARY_COUNTERS,
} from "./ui-usage-registry";
import { settingsSurface, surfaceOf, UI_SURFACES } from "./ui-surfaces";

const WINDOW_ID = "123e4567-e89b-42d3-a456-426614174000";

describe("surfaces", () => {
	it("finds the innermost known surface of an event target", () => {
		document.body.innerHTML = `
			<div data-telemetry-surface="sidebar">
				<div data-telemetry-surface="not_a_surface"><span id="inner">x</span></div>
				<div data-telemetry-surface="cmdk"><span id="deep">y</span></div>
			</div>`;
		expect(surfaceOf(document.getElementById("deep"))).toBe("cmdk");
		// An unknown value is not a surface, and the walk stops at the nearest attribute.
		expect(surfaceOf(document.getElementById("inner"))).toBeUndefined();
		expect(surfaceOf(document.getElementById("deep")!.firstChild)).toBe("cmdk");
		expect(surfaceOf(null)).toBeUndefined();
	});

	it("maps settings pages to settings surfaces", () => {
		expect(settingsSurface("harness")).toBe("settings_agents");
		expect(settingsSurface("browserProfiles")).toBe("settings_browser");
		expect(settingsSurface("remoteHosts")).toBe("settings_remote_hosts");
		expect(settingsSurface("some-new-page")).toBe("settings_general");
	});
});

describe("sanitizeUiUsageProperties", () => {
	it("keeps only known surfaces and opened_by values on surface views", () => {
		expect(sanitizeUiUsageProperties("ao.surfaces.viewed", { surface: "board", opened_by: "cmdk", path: "/Users/me" })).toEqual({
			surface: "board",
			opened_by: "cmdk",
		});
		expect(sanitizeUiUsageProperties("ao.surfaces.viewed", { surface: "Board", opened_by: "mouse" })).toEqual({});
	});

	it("keeps only known features", () => {
		expect(sanitizeUiUsageProperties("ao.features.first_used", { feature: "steer" })).toEqual({ feature: "steer" });
		expect(sanitizeUiUsageProperties("ao.features.first_used", { feature: "secret-project" })).toEqual({});
	});

	it("keeps error codes but never messages", () => {
		expect(
			sanitizeUiUsageProperties("ao.ui.error_shown", {
				surface: "chat",
				presentation: "inline",
				error_kind: "chat_turn_failed",
				error_code: "AUTH_REQUIRED",
				message: "token expired for me@example.com",
			}),
		).toEqual({ surface: "chat", presentation: "inline", error_kind: "chat_turn_failed", error_code: "AUTH_REQUIRED" });
		expect(sanitizeUiUsageProperties("ao.ui.error_shown", { error_code: "auth required: /Users/me/repo" })).toEqual({});
		expect(sanitizeUiUsageProperties("ao.ui.error_shown", { error_code: "auth_required" })).toEqual({});
		expect(sanitizeUiUsageProperties("ao.ui.error_shown", { error_kind: "toast_with_text", presentation: "modal" })).toEqual({});
	});

	it("keeps window fields and positive whole-number counters on the summary", () => {
		const safe = sanitizeUiUsageProperties("ao.ui.usage_summary", {
			measurement_schema_version: 1,
			window_id: WINDOW_ID,
			window_duration_seconds: 0,
			active_seconds: 12.6,
			flush_reason: "startup",
			send_count: 3,
			steer_count: 0,
			click_count: -1,
			queue_count: Number.NaN,
			cmdk_open_count: Number.POSITIVE_INFINITY,
			shortcut_count: "4",
			draft_text: "hello",
			unknown_count: 9,
		});
		expect(safe).toEqual({
			measurement_schema_version: 1,
			window_id: WINDOW_ID,
			window_duration_seconds: 0,
			active_seconds: 13,
			flush_reason: "startup",
			send_count: 3,
		});
	});

	it("drops a malformed window id and flush reason", () => {
		expect(sanitizeUiUsageProperties("ao.ui.usage_summary", { window_id: "session-abc", flush_reason: "manual" })).toEqual({});
	});

	it("keeps known surface rows once, with only their counts", () => {
		const safe = sanitizeUiUsageProperties("ao.ui.usage_summary", {
			surfaces: [
				{ surface: "sidebar", visible_seconds: 30, dead_click_count: 2, name: "My project", region_click_counts: [1, 0, 0, 0, 0, 0, 0, 0, 1] },
				{ surface: "sidebar", visible_seconds: 999 },
				{ surface: "secret_panel", visible_seconds: 5 },
				{ surface: "board", visible_seconds: 0, click_count: 40 },
				{ surface: "chat", open_count: 1, dead_click_count: -2 },
				{ surface: "composer", x: 120, y: 40 },
				"not an object",
			],
		});
		expect(safe.surfaces).toEqual([
			{ surface: "sidebar", visible_seconds: 30, dead_click_count: 2 },
			{ surface: "chat", open_count: 1 },
		]);
	});

	it("keeps known control rows once and drops empty lists", () => {
		const safe = sanitizeUiUsageProperties("ao.ui.usage_summary", {
			controls: [
				{ control_id: "composer.send", click_count: 4, keyboard_count: 1, label: "Send" },
				{ control_id: "composer.send", click_count: 100 },
				{ control_id: "button.my-secret", click_count: 1 },
				{ control_id: "tab.close" },
			],
		});
		expect(safe.controls).toEqual([{ control_id: "composer.send", click_count: 4, keyboard_count: 1 }]);
		expect(sanitizeUiUsageProperties("ao.ui.usage_summary", { surfaces: [], controls: [{ control_id: "nope" }] })).toEqual({});
	});

	it("is what the renderer client applies to these events", async () => {
		await expect(sanitizeRendererProperties("ao.ui.rage_clicked", { surface: "sidebar", x: 10, y: 20 })).resolves.toEqual({ surface: "sidebar" });
		await expect(sanitizeRendererProperties("ao.surfaces.viewed", { surface: "cmdk", opened_by: "shortcut", url: "https://x" })).resolves.toEqual({
			surface: "cmdk",
			opened_by: "shortcut",
		});
	});
});

describe("docs/telemetry.md", () => {
	const docs = readFileSync(resolve(__dirname, "../../../../docs/telemetry.md"), "utf8");
	const start = docs.indexOf("## UI usage");
	const section = docs.slice(start, docs.indexOf("\n## ", start + 1));
	const documented = (value: string) => section.includes(`\`${value}\``);

	it("has a UI usage section", () => {
		expect(section.length).toBeGreaterThan(100);
	});

	it("documents every event, surface, control, feature and enum value", () => {
		const values = [
			...UI_USAGE_EVENTS,
			...UI_SURFACES,
			...UI_CONTROLS,
			...UI_FEATURES,
			...SURFACE_OPENED_BY,
			...SWITCH_SOURCES,
			...ERROR_KINDS,
			...ERROR_PRESENTATIONS,
		];
		expect(values.filter((value) => !documented(value))).toEqual([]);
	});

	it("documents every summary property", () => {
		const keys = [
			"measurement_schema_version",
			"window_id",
			"window_duration_seconds",
			"active_seconds",
			"flush_reason",
			"surfaces",
			"controls",
			"control_id",
			...USAGE_SUMMARY_COUNTERS.filter((key) => !key.startsWith("switch_from_")),
			...SURFACE_STAT_KEYS,
			...CONTROL_STAT_KEYS,
		];
		expect(keys.filter((key) => !documented(key))).toEqual([]);
		// The per-source switch counters are documented by their pattern.
		expect(documented("switch_from_<source>_count")).toBe(true);
	});

	it("lists the events under Event names", () => {
		const eventNames = docs.slice(docs.indexOf("## Event names"));
		expect(UI_USAGE_EVENTS.filter((event) => !eventNames.includes(`\`${event}\``))).toEqual([]);
	});
});
