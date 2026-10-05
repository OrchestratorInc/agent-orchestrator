import { describe, expect, it } from "vitest";
import { deriveOnboardingInput, shouldOnboard } from "./onboarding";

describe("shouldOnboard", () => {
	it("onboards a fresh install", () => {
		expect(shouldOnboard({ configured: false, skipped: false, cloudSignedIn: false })).toBe(true);
	});

	it("does not onboard once a server is configured", () => {
		expect(shouldOnboard({ configured: true, skipped: false, cloudSignedIn: false })).toBe(false);
	});

	it("does not onboard after the user skipped", () => {
		expect(shouldOnboard({ configured: false, skipped: true, cloudSignedIn: false })).toBe(false);
	});

	it("does not onboard when configured and skipped", () => {
		expect(shouldOnboard({ configured: true, skipped: true, cloudSignedIn: false })).toBe(false);
	});

	// Both flags load asynchronously. Acting on half-loaded state would bounce a
	// paired user onto the welcome screen for a frame on every cold start.
	it("waits while the config is still loading", () => {
		expect(shouldOnboard({ configured: null, skipped: false, cloudSignedIn: false })).toBe(false);
	});

	it("waits while the skip flag is still loading", () => {
		expect(shouldOnboard({ configured: false, skipped: null, cloudSignedIn: false })).toBe(false);
	});

	it("waits while both are still loading", () => {
		expect(shouldOnboard({ configured: null, skipped: null, cloudSignedIn: false })).toBe(false);
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

	// cloudSignedIn loading must take priority even over otherwise-decisive
	// local state (configured+skipped both true) — an in-flight cloud check
	// must never be treated as "definitely not signed in".
	it("waits on cloudSignedIn even when configured and skipped are both known", () => {
		expect(shouldOnboard({ configured: true, skipped: true, cloudSignedIn: null })).toBe(false);
	});

	// Regression: OnboardingGate.tsx has no cloud auth wired in, so it is
	// definitively not signed in to cloud, not "still loading" — it must pass
	// `cloudSignedIn: false`, not `null`. Passing `null` here silently kills
	// onboarding entirely, since every call from that gate would then defer
	// forever. This mirrors the exact values OnboardingGate.tsx passes for a
	// fresh, unpaired install.
	it("onboards a fresh install using OnboardingGate's actual call shape", () => {
		expect(shouldOnboard({ configured: false, skipped: false, cloudSignedIn: false })).toBe(true);
	});
});

describe("deriveOnboardingInput", () => {
	it("produces onboarding-ready input for a fresh unpaired install with no cloud auth wired", () => {
		const input = deriveOnboardingInput({ config: null, skipped: null, cloud: null });
		expect(input).toEqual({ configured: null, skipped: null, cloudSignedIn: false });
		expect(shouldOnboard(input)).toBe(false); // Waits for config and skipped to load
	});

	it("marks configured: true when a non-empty host is present", () => {
		const input = deriveOnboardingInput({ config: { host: "10.0.0.2" }, skipped: false, cloud: null });
		expect(input.configured).toBe(true);
	});

	it("marks configured: false when the host is whitespace-only", () => {
		const input = deriveOnboardingInput({ config: { host: "   " }, skipped: false, cloud: null });
		expect(input.configured).toBe(false);
	});

	it("marks cloudSignedIn: false when cloud auth is not wired in", () => {
		const input = deriveOnboardingInput({ config: null, skipped: null, cloud: null });
		expect(input.cloudSignedIn).toBe(false);
	});

	it("marks cloudSignedIn: null when cloud auth is wired but still loading", () => {
		const input = deriveOnboardingInput({ config: null, skipped: null, cloud: { signedIn: null } });
		expect(input.cloudSignedIn).toBe(null);
	});

	it("marks cloudSignedIn: true when the user is signed in to cloud", () => {
		const input = deriveOnboardingInput({ config: null, skipped: false, cloud: { signedIn: true } });
		expect(input.cloudSignedIn).toBe(true);
	});

	it("marks cloudSignedIn: false when the user is signed out of cloud", () => {
		const input = deriveOnboardingInput({ config: null, skipped: false, cloud: { signedIn: false } });
		expect(input.cloudSignedIn).toBe(false);
	});
});
