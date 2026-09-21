import { describe, expect, it } from "vitest";
import { configLoadPlan, shouldPublishConfigLoad } from "./configLoad";

describe("config load scheduling", () => {
	it("waits while the persisted environment is unresolved", () => {
		expect(configLoadPlan(null)).toBe("wait");
	});

	it("hydrates the saved Local pairing in Cloud without resolving endpoints", () => {
		expect(configLoadPlan("cloud")).toBe("hydrate");
	});

	it("allows endpoint resolution only for Local", () => {
		expect(configLoadPlan("local")).toBe("resolve");
	});

	it("drops a late Local result after a switch to Cloud", () => {
		expect(shouldPublishConfigLoad(
		{ environment: "local", generation: 4 },
		{ environment: "cloud", generation: 5 },
	)).toBe(false);
		expect(shouldPublishConfigLoad(
		{ environment: "local", generation: 4 },
		{ environment: "local", generation: 4 },
	)).toBe(true);
	});
});
