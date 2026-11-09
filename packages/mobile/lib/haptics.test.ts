import { beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	platform: { OS: "ios", Version: 0 as number | string },
	impact: vi.fn(async (_style: string) => {}),
	selection: vi.fn(async () => {}),
	notification: vi.fn(async (_type: string) => {}),
	android: vi.fn(async (_type: string) => {}),
}));
vi.mock("react-native", () => ({ Platform: fixtures.platform }));
vi.mock("expo-haptics", async () => {
	// The enums are plain data; only the calls that reach native are faked.
	const types = await import("expo-haptics/src/Haptics.types");
	return {
		...types,
		impactAsync: fixtures.impact,
		selectionAsync: fixtures.selection,
		notificationAsync: fixtures.notification,
		performAndroidHapticsAsync: fixtures.android,
	};
});

import { haptics } from "./haptics";

type Intent = keyof typeof haptics;

beforeEach(() => {
	fixtures.platform.OS = "ios";
	fixtures.platform.Version = "18.0";
	for (const fn of [fixtures.impact, fixtures.selection, fixtures.notification, fixtures.android]) fn.mockClear();
});

describe("on iOS", () => {
	it.each<[Intent, "impact" | "selection" | "notification", string | undefined]>([
		["tap", "impact", "light"],
		["select", "selection", undefined],
		["detent", "selection", undefined],
		["armed", "impact", "rigid"],
		["press", "impact", "medium"],
		["success", "notification", "success"],
		["warning", "notification", "warning"],
		["error", "notification", "error"],
	])("plays %s through the Taptic Engine generators", (intent, channel, style) => {
		haptics[intent]();
		const call = fixtures[channel];
		expect(call).toHaveBeenCalledTimes(1);
		if (style) expect(call).toHaveBeenCalledWith(style);
		expect(fixtures.android).not.toHaveBeenCalled();
	});
});

describe("on Android", () => {
	beforeEach(() => {
		fixtures.platform.OS = "android";
		fixtures.platform.Version = 35;
	});

	it.each<[Intent, string]>([
		["tap", "virtual-key"],
		["select", "segment-tick"],
		["detent", "segment-tick"],
		["armed", "long-press"],
		["press", "long-press"],
		["success", "confirm"],
		["warning", "long-press"],
		["error", "reject"],
	])("plays %s as the system's own %s feedback, never the Vibrator", (intent, constant) => {
		haptics[intent]();
		expect(fixtures.android).toHaveBeenCalledExactlyOnceWith(constant);
		expect(fixtures.impact).not.toHaveBeenCalled();
		expect(fixtures.selection).not.toHaveBeenCalled();
		expect(fixtures.notification).not.toHaveBeenCalled();
	});

	// A constant newer than the device is rejected outright, which would leave
	// the press silent; each falls back to one every API level has.
	it.each<[number, Intent, string]>([
		[33, "select", "clock-tick"],
		[33, "detent", "clock-tick"],
		[34, "select", "segment-tick"],
		[29, "success", "virtual-key"],
		[29, "error", "long-press"],
		[30, "success", "confirm"],
		[30, "error", "reject"],
	])("on API %i plays %s as %s", (version, intent, constant) => {
		fixtures.platform.Version = version;
		haptics[intent]();
		expect(fixtures.android).toHaveBeenCalledExactlyOnceWith(constant);
	});
});

it("does nothing on the web", () => {
	fixtures.platform.OS = "web";
	for (const intent of Object.keys(haptics) as Intent[]) haptics[intent]();
	for (const fn of [fixtures.impact, fixtures.selection, fixtures.notification, fixtures.android]) expect(fn).not.toHaveBeenCalled();
});

it("never lets a failed haptic reach the press handler", async () => {
	fixtures.platform.OS = "android";
	fixtures.platform.Version = 35;
	fixtures.android.mockRejectedValueOnce(new Error("no haptic engine"));
	expect(() => haptics.tap()).not.toThrow();
	await Promise.resolve();
});
