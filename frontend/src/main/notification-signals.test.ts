// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import {
	alreadyLooking,
	createTaggedToasts,
	dockBounceType,
	osToastChimeControllable,
	shouldReplaceBounce,
	shouldSignalAttention,
	shouldToast,
	toastSilent,
	type NotificationType,
} from "./notification-signals";

const ALL_TYPES: NotificationType[] = ["needs_input", "ready_to_merge", "pr_merged", "pr_closed_unmerged"];

describe("shouldToast", () => {
	it("fires a toast for every backend notification type", () => {
		for (const type of ALL_TYPES) {
			expect(shouldToast({ title: `${type} title` }, true)).toBe(true);
		}
	});

	it("does not toast without a title or when notifications are unsupported", () => {
		expect(shouldToast({ title: "" }, true)).toBe(false);
		expect(shouldToast({}, true)).toBe(false);
		expect(shouldToast({ title: "needs input" }, false)).toBe(false);
	});
});

describe("alreadyLooking", () => {
	it("skips visual signals while the window has focus, or the prompt is on screen", () => {
		expect(alreadyLooking(true, {})).toBe(true);
		expect(alreadyLooking(false, { watched: true })).toBe(true);
		expect(alreadyLooking(false, {})).toBe(false);
	});

	it("alerts while focused for a cloud mention or aside reply about a session not on screen", () => {
		expect(alreadyLooking(true, { watched: false, alertWhenFocused: true })).toBe(false);
		expect(alreadyLooking(false, { alertWhenFocused: true })).toBe(false);
		// The session on screen stays quiet even then.
		expect(alreadyLooking(true, { watched: true, alertWhenFocused: true })).toBe(true);
	});
});

describe("shouldSignalAttention", () => {
	it("flashes the taskbar for the actionable types", () => {
		expect(shouldSignalAttention("needs_input")).toBe(true);
		expect(shouldSignalAttention("ready_to_merge")).toBe(true);
	});

	it("does not flash the taskbar for informational PR outcomes", () => {
		expect(shouldSignalAttention("pr_merged")).toBe(false);
		expect(shouldSignalAttention("pr_closed_unmerged")).toBe(false);
	});

	it("does not signal for unknown or missing types", () => {
		expect(shouldSignalAttention("some_future_type")).toBe(false);
		expect(shouldSignalAttention(undefined)).toBe(false);
	});
});

describe("shouldReplaceBounce", () => {
	it("replaces no bounce or a pending informational bounce", () => {
		expect(shouldReplaceBounce(null)).toBe(true);
		expect(shouldReplaceBounce({ critical: false })).toBe(true);
	});

	it("never replaces a pending critical bounce, so a blocked agent stays loud", () => {
		expect(shouldReplaceBounce({ critical: true })).toBe(false);
	});
});

describe("osToastChimeControllable", () => {
	// Platform contract: Electron's mac and win presenters read `silent`;
	// the Linux libnotify presenter does not (no suppress-sound hint).
	it.each(["darwin", "win32"] as const)("is controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(true);
	});

	it.each(["linux", "freebsd", "openbsd"] as const)("is not controllable on %s", (platform) => {
		expect(osToastChimeControllable(platform)).toBe(false);
	});
});

describe("toastSilent", () => {
	const HONOURING: NodeJS.Platform[] = ["darwin", "win32"];

	it.each(HONOURING)("on %s mutes the toast whenever sound notifications are off", (platform) => {
		expect(toastSilent(platform, false, false)).toBe(true);
		expect(toastSilent(platform, false, true)).toBe(true);
	});

	it.each(HONOURING)("on %s mutes the toast when AO plays its own sound", (platform) => {
		expect(toastSilent(platform, true, true)).toBe(true);
	});

	it.each(HONOURING)("on %s keeps the native chime for an informational toast when sound is on", (platform) => {
		expect(toastSilent(platform, true, false)).toBe(false);
	});

	// On Linux the value would be ignored, so no combination may claim a mute.
	it.each([
		[false, false],
		[false, true],
		[true, false],
		[true, true],
	])("on linux never claims control (enabled=%s, playsSound=%s)", (enabled, playsSound) => {
		expect(toastSilent("linux", enabled, playsSound)).toBeUndefined();
	});
});

describe("dockBounceType", () => {
	it("bounces critically for a blocked agent waiting on the user", () => {
		expect(dockBounceType("needs_input")).toBe("critical");
	});

	it("bounces once for the other backend types", () => {
		for (const type of ALL_TYPES.filter((t) => t !== "needs_input")) {
			expect(dockBounceType(type)).toBe("informational");
		}
	});

	it("bounces once for unknown or missing types", () => {
		expect(dockBounceType("some_future_type")).toBe("informational");
		expect(dockBounceType(undefined)).toBe("informational");
	});
});

describe("createTaggedToasts", () => {
	const toast = () => ({ close: vi.fn() });

	it("replaces the toast showing under the same tag, so a session shows its newest mention", () => {
		const toasts = createTaggedToasts<ReturnType<typeof toast>>();
		const first = toast();
		const second = toast();
		toasts.show("cloud-session:s1", first);
		toasts.show("cloud-session:s1", second);
		expect(first.close).toHaveBeenCalledTimes(1);
		expect(second.close).not.toHaveBeenCalled();
		expect(toasts.size).toBe(1);
	});

	it("keeps toasts with other tags, or none, side by side", () => {
		const toasts = createTaggedToasts<ReturnType<typeof toast>>();
		const a = toast();
		const b = toast();
		toasts.show("cloud-session:s1", a);
		toasts.show("cloud-session:s2", b);
		toasts.show(undefined, toast());
		expect(a.close).not.toHaveBeenCalled();
		expect(b.close).not.toHaveBeenCalled();
		expect(toasts.size).toBe(2);
	});

	it("forgets a toast once it closes, so a later one does not close it again", () => {
		const toasts = createTaggedToasts<ReturnType<typeof toast>>();
		const first = toast();
		toasts.show("cloud-session:s1", first);
		toasts.forget("cloud-session:s1", first);
		toasts.show("cloud-session:s1", toast());
		expect(first.close).not.toHaveBeenCalled();
	});

	it("drops the oldest tag past its bound", () => {
		const toasts = createTaggedToasts<ReturnType<typeof toast>>(2);
		toasts.show("a", toast());
		toasts.show("b", toast());
		toasts.show("c", toast());
		expect(toasts.size).toBe(2);
	});
});
