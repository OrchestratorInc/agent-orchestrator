import { describe, expect, it } from "vitest";
import { staleSavedTuning } from "./stale-agent-tuning";

const catalog = {
	refreshState: "idle",
	models: [
		{ id: "claude-sonnet-5-5", efforts: ["low", "high"] },
		{ id: "claude-fable-5-1", efforts: ["high"], requiresVersion: "3.0.0" },
	],
};

describe("staleSavedTuning", () => {
	it("resets a model the account's catalog does not list, with its effort", () => {
		expect(staleSavedTuning(catalog, "claude-gone-4", "high")).toEqual({ model: true, effort: true });
	});
	it("resets a model that needs a newer Claude Code", () => {
		expect(staleSavedTuning(catalog, "claude-fable-5-1", "")).toEqual({ model: true, effort: false });
	});
	it("resets only an effort the listed model does not support", () => {
		expect(staleSavedTuning(catalog, "claude-sonnet-5-5", "max")).toEqual({ model: false, effort: true });
	});
	it("keeps valid settings, the default, and typed IDs where the catalog allows them", () => {
		expect(staleSavedTuning(catalog, "claude-sonnet-5-5", "high")).toBeUndefined();
		expect(staleSavedTuning(catalog, "", "high")).toBeUndefined();
		expect(staleSavedTuning({ ...catalog, customModelEntry: "direct" }, "my-gateway-model", "")).toBeUndefined();
	});
	it("never resets against a catalog that cannot answer", () => {
		expect(staleSavedTuning({ ...catalog, stale: true }, "claude-gone-4", "")).toBeUndefined();
		expect(staleSavedTuning({ ...catalog, refreshState: "refreshing" }, "claude-gone-4", "")).toBeUndefined();
		expect(staleSavedTuning({ models: [] }, "claude-gone-4", "")).toBeUndefined();
		expect(staleSavedTuning(undefined, "claude-gone-4", "")).toBeUndefined();
	});
});
