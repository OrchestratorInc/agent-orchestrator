import { describe, expect, it } from "vitest";
import { MOBILE_ALLOWLIST, MOBILE_EVENTS } from "./events";

describe("mobile event names", () => {
	it("uses the mobile.app.* names with an allowlist entry each", () => {
		expect(MOBILE_EVENTS).toEqual({
			active: "mobile.app.active",
			paired: "mobile.app.paired",
			connected: "mobile.app.connected",
			onboardingStarted: "mobile.app.onboarding_started",
			onboardingCompleted: "mobile.app.onboarding_completed",
			onboardingSkipped: "mobile.app.onboarding_skipped",
			notificationOpened: "mobile.app.notification_opened",
			featureUsed: "mobile.app.feature_used",
		});
		for (const name of Object.values(MOBILE_EVENTS)) expect(MOBILE_ALLOWLIST[name]).toBeDefined();
		expect(Object.keys(MOBILE_ALLOWLIST).some((name) => name.startsWith("ao."))).toBe(false);
	});
});
