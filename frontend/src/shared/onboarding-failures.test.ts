import { expect, it } from "vitest";
import { ONBOARDING_ACTIONS, ONBOARDING_SENTRY_REASONS, onboardingFailureCode, onboardingSentryTarget, parseOnboardingFailureCode } from "./onboarding-failures";

it("round-trips every reported action and reason", () => {
	for (const action of ONBOARDING_ACTIONS) {
		for (const reason of ONBOARDING_SENTRY_REASONS) {
			expect(parseOnboardingFailureCode(onboardingFailureCode(action, reason))).toEqual({ action, reason });
		}
	}
});

it("rejects setup states, unknown names and anything else in the code", () => {
	for (const code of [
		"agent_sign_in:not_signed_in",
		"gh_install:unsupported",
		"agent_sign_in:documentation",
		"gh_install:private-repo",
		"private:install_failed",
		"gh_install:install_failed:extra",
		"gh_install",
		"",
		undefined,
		42,
	]) {
		expect(parseOnboardingFailureCode(code)).toBeUndefined();
	}
});

it("tags only listed targets, and anything else as other", () => {
	expect(onboardingSentryTarget("claude-code")).toBe("claude-code");
	expect(onboardingSentryTarget("gh")).toBe("gh");
	expect(onboardingSentryTarget("/Users/someone/private")).toBe("other");
	expect(onboardingSentryTarget("")).toBeUndefined();
	expect(onboardingSentryTarget(undefined)).toBeUndefined();
	expect(onboardingSentryTarget(42)).toBeUndefined();
});
