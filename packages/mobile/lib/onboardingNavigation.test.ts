import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { completeOnboarding } from "./onboardingNavigation";

describe("completeOnboarding", () => {
	it.each([
		["first pairing", ["onboarding", "pair"]],
		["skipping onboarding", ["onboarding"]],
	])("makes Workers the only root route after %s", (_flow, initialRoutes) => {
		let routes = initialRoutes;
		let actionType = "";
		completeOnboarding({
			dispatch: (action) => {
				actionType = action.type;
				routes = action.payload.routes.map((route) => route.name);
			},
		});
		expect(actionType).toBe("RESET");
		expect(routes).toEqual(["(tabs)"]);
	});

	// Onboarding has no skip: pairing or signing in are the ways on, and each completes it.
	it("is used after pairing from onboarding", () => {
		const pair = readFileSync(fileURLToPath(new URL("../app/pair.tsx", import.meta.url)), "utf8");
		expect(pair).toContain("if (fromOnboarding) completeOnboarding(navigation);");
		expect(pair).toContain("else backOr(router);");
	});

	it("is used after signing in from onboarding brings in a machine", () => {
		const onboarding = readFileSync(fileURLToPath(new URL("../app/onboarding.tsx", import.meta.url)), "utf8");
		expect(onboarding).toContain('title="Sign in"');
		expect(onboarding).toMatch(/await syncAccountHosts\(account\);[\s\S]*completeOnboarding\(navigation\);/);
	});
});
