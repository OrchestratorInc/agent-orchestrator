// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
	bellUnreadCount,
	dockBounceType,
	loudNotificationsEnabled,
	needsHumanFeedback,
	osToastChimeControllable,
	shouldReplaceBounce,
	shouldSignalAttention,
	shouldToast,
	toastSilent,
	type NotificationType,
} from "./notification-signals";

// The signal set spans two vocabularies: AO backend types and the cloud
// control plane's names for the same event classes.
const SIGNAL_TYPES: string[] = ["needs_input", "review_changes_requested", "review_feedback", "agent_failed"];
const SILENT_TYPES: NotificationType[] = [
	"ready_to_merge",
	"pr_merged",
	"pr_closed_unmerged",
	"review_completed",
];

describe("needsHumanFeedback", () => {
	it("classifies the human-feedback types as signals", () => {
		for (const type of SIGNAL_TYPES) {
			expect(needsHumanFeedback(type)).toBe(true);
		}
	});

	it("keeps routine lifecycle types quiet", () => {
		for (const type of SILENT_TYPES) {
			expect(needsHumanFeedback(type)).toBe(false);
		}
	});

	it("defaults unknown and missing types to quiet", () => {
		expect(needsHumanFeedback("some_future_type")).toBe(false);
		expect(needsHumanFeedback(undefined)).toBe(false);
	});
});

describe("loudNotificationsEnabled", () => {
	it.each(["1", "true", " 1 ", "TRUE"])("opts back into the upstream behavior for %s", (value) => {
		expect(loudNotificationsEnabled(value)).toBe(true);
	});

	it.each([undefined, "", "0", "false", "loud", "yes"])("stays quiet for %s", (value) => {
		expect(loudNotificationsEnabled(value)).toBe(false);
	});
});

describe("shouldToast", () => {
	it("toasts only the human-feedback types", () => {
		for (const type of SIGNAL_TYPES) {
			expect(shouldToast({ title: `${type} title`, type }, true)).toBe(true);
		}
	});

	it("does not toast routine types, even with a title", () => {
		for (const type of SILENT_TYPES) {
			expect(shouldToast({ title: `${type} title`, type }, true)).toBe(false);
		}
	});

	it("in loud mode toasts every backend type, like upstream", () => {
		for (const type of [...SIGNAL_TYPES, ...SILENT_TYPES]) {
			expect(shouldToast({ title: `${type} title`, type }, true, true)).toBe(true);
		}
	});

	it("does not toast without a title or when notifications are unsupported", () => {
		expect(shouldToast({ title: "", type: "needs_input" }, true)).toBe(false);
		expect(shouldToast({ type: "needs_input" }, true)).toBe(false);
		expect(shouldToast({ title: "needs input", type: "needs_input" }, false)).toBe(false);
	});
});

describe("shouldSignalAttention", () => {
	it("in quiet mode signals for every human-feedback type", () => {
		for (const type of ["needs_input", "review_changes_requested"] as const) {
			expect(shouldSignalAttention(type, false)).toBe(true);
		}
		expect(shouldSignalAttention("ready_to_merge", false)).toBe(false);
	});

	it("in loud mode restores the upstream actionable set", () => {
		expect(shouldSignalAttention("needs_input", true)).toBe(true);
		expect(shouldSignalAttention("ready_to_merge", true)).toBe(true);
		expect(shouldSignalAttention("review_changes_requested", true)).toBe(false);
		expect(shouldSignalAttention("pr_merged", true)).toBe(false);
		expect(shouldSignalAttention(undefined, true)).toBe(false);
	});
});

describe("bellUnreadCount", () => {
	it("counts every unread row in loud mode, like upstream", () => {
		expect(
			bellUnreadCount(
				[
					{ type: "needs_input", status: "unread" },
					{ type: "ready_to_merge", status: "unread" },
					{ type: "pr_merged", status: "unread" },
					{ type: "needs_input", status: "read" },
				],
				true,
			),
		).toBe(3);
	});

	it("is quiet by default", () => {
		expect(
			bellUnreadCount([{ type: "ready_to_merge", status: "unread" }], false),
		).toBe(0);
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

	it("bounces once for the other signal types", () => {
		for (const type of SIGNAL_TYPES.filter((t) => t !== "needs_input")) {
			expect(dockBounceType(type)).toBe("informational");
		}
	});
});
