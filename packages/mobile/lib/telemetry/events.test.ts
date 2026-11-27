import { describe, expect, it } from "vitest";
import { MOBILE_ALLOWLIST, MOBILE_EVENTS } from "./events";
import { sanitizeMobileProperties } from "./sanitize";

describe("mobile event names", () => {
	it("uses the mobile.app.* names with an allowlist entry each", () => {
		expect(MOBILE_EVENTS).toEqual({
			active: "mobile.app.active",
			paired: "mobile.app.paired",
			connected: "mobile.app.connected",
			onboardingStarted: "mobile.app.onboarding_started",
			onboardingCompleted: "mobile.app.onboarding_completed",
			notificationOpened: "mobile.app.notification_opened",
			featureUsed: "mobile.app.feature_used",
			surfaceViewed: "mobile.surfaces.viewed",
			usageSummary: "mobile.ui.usage_summary",
			errorShown: "mobile.ui.error_shown",
			launchTimed: "mobile.launches.timed",
		});
		for (const name of Object.values(MOBILE_EVENTS)) expect(MOBILE_ALLOWLIST[name]).toBeDefined();
		expect(Object.keys(MOBILE_ALLOWLIST).some((name) => name.startsWith("ao."))).toBe(false);
	});

	// PushManager sends review and cloud too; the sanitizer used to strip them.
	it.each(["session", "review", "prs", "cloud"])("keeps the %s notification target", (target) => {
		expect(sanitizeMobileProperties(MOBILE_EVENTS.notificationOpened, { target, cold_start: true }))
			.toEqual({ target, cold_start: true });
	});

	// Everything the tracker can put in a summary survives the sanitizer.
	it("keeps every property of a usage summary", async () => {
		const { createUiUsageTracker, UI_WINDOW_MS } = await import("./uiUsage");
		let now = 0;
		let summary: Record<string, unknown> | undefined;
		const tracker = createUiUsageTracker({
			now: () => now,
			capture: (event, props) => { if (event === MOBILE_EVENTS.usageSummary) summary = props; return true; },
			save: () => {},
			clear: () => {},
		});
		tracker.setRoute("board");
		now += 5_000;
		tracker.setRoute("session");
		tracker.setVariant("session", "session_tui");
		tracker.sessionOpened("h|s1");
		tracker.errorShown("toast", "action_failed");
		now += UI_WINDOW_MS;
		tracker.setForeground(false);
		expect(summary).toBeDefined();
		expect(sanitizeMobileProperties(MOBILE_EVENTS.usageSummary, summary)).toEqual(summary);
	});

	it("drops a surface outside the closed list", () => {
		expect(sanitizeMobileProperties(MOBILE_EVENTS.surfaceViewed, { surface: "/session/acme-1" })).toEqual({});
	});
});
