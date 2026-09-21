import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const store = readFileSync(new URL("./store.tsx", import.meta.url), "utf8");

describe("initial Local configuration scheduling", () => {
	it("defers endpoint resolution until Local is the resolved environment", () => {
		expect(store).toContain('configLoadPlan(environment)');
		expect(store).toContain("shouldPublishConfigLoad");
	});
});
