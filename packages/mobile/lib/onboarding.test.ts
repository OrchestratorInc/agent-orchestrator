import { describe, expect, it } from "vitest";
import { shouldOnboard } from "./onboarding";

describe("shouldOnboard", () => {
	it("onboards a fresh install", () => {
		expect(shouldOnboard({ configured: false, skipped: false })).toBe(true);
	});

	it("does not onboard once a server is configured", () => {
		expect(shouldOnboard({ configured: true, skipped: false })).toBe(false);
	});

	it("does not onboard after the user skipped", () => {
		expect(shouldOnboard({ configured: false, skipped: true })).toBe(false);
	});

	it("does not onboard when configured and skipped", () => {
		expect(shouldOnboard({ configured: true, skipped: true })).toBe(false);
	});

	// Both flags load asynchronously. Acting on half-loaded state would bounce a
	// paired user onto the welcome screen for a frame on every cold start.
	it("waits while the config is still loading", () => {
		expect(shouldOnboard({ configured: null, skipped: false })).toBe(false);
	});

	it("waits while the skip flag is still loading", () => {
		expect(shouldOnboard({ configured: false, skipped: null })).toBe(false);
	});

	it("waits while both are still loading", () => {
		expect(shouldOnboard({ configured: null, skipped: null })).toBe(false);
	});

	// A signed-in cloud user never needs onboarding, even on a device with no
	// local daemon paired.
	it("does not onboard a signed-in cloud user", () => {
		expect(shouldOnboard({ configured: false, skipped: false, cloudSignedIn: true })).toBe(false);
	});

	it("does not onboard a signed-in cloud user even if they also skipped", () => {
		expect(shouldOnboard({ configured: false, skipped: true, cloudSignedIn: true })).toBe(false);
	});

	it("onboards when signed out of cloud and unconfigured", () => {
		expect(shouldOnboard({ configured: false, skipped: false, cloudSignedIn: false })).toBe(true);
	});

	it("waits while cloudSignedIn is still loading, even if the other flags are known", () => {
		expect(shouldOnboard({ configured: false, skipped: false, cloudSignedIn: null })).toBe(false);
	});

	// Callers that have not been taught about cloud yet (or a local-only
	// build) omit the field entirely; that must not change existing behavior.
	it("treats an omitted cloudSignedIn like not signed into cloud", () => {
		expect(shouldOnboard({ configured: false, skipped: false })).toBe(true);
	});
});
