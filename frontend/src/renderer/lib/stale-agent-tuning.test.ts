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
	it("checks effort against the catalog default without clearing the absent saved model", () => {
		expect(staleSavedTuning({ models: [{ id: "claude-haiku-4-5", isDefault: true, efforts: [] }] }, "", "high", "claude-code")).toEqual({ model: false, effort: true });
	});
	it("keeps unlisted dynamic models for agent-side validation", () => {
		expect(staleSavedTuning(catalog, "claude-gone-4", "high", "claude-code")).toBeUndefined();
		expect(staleSavedTuning({ ...catalog, customModelEntry: "configured" }, "other", "high", "codex")).toBeUndefined();
		expect(staleSavedTuning({ ...catalog, source: "official-modes", customModelEntry: "none" }, "removed", "high", "memcode")).toEqual({ model: true, effort: true });
	});
	it("preserves a model that needs a newer Claude Code", () => {
		expect(staleSavedTuning(catalog, "claude-fable-5-1", "max", "claude-code")).toBeUndefined();
	});
	it("resets only an effort the listed model does not support", () => {
		expect(staleSavedTuning(catalog, "claude-sonnet-5-5", "max", "claude-code")).toEqual({ model: false, effort: true });
	});
	it("keeps valid settings, the default, and typed IDs where the catalog allows them", () => {
		expect(staleSavedTuning(catalog, "claude-sonnet-5-5", "high", "claude-code")).toBeUndefined();
		expect(staleSavedTuning(catalog, "", "high", "claude-code")).toBeUndefined();
		expect(staleSavedTuning({ ...catalog, customModelEntry: "direct" }, "my-gateway-model", "", "claude-code")).toBeUndefined();
	});
	it.each([[], undefined])("resets effort for a concrete Claude model with no efforts (%j)", (efforts) => {
		const noEffort = { ...catalog, models: [{ id: "claude-haiku-4-5", efforts }] };
		expect(staleSavedTuning(noEffort, "claude-haiku-4-5", "high", "claude-code")).toEqual({ model: false, effort: true });
		expect(staleSavedTuning(noEffort, "claude-haiku-4-5", "", "claude-code")).toBeUndefined();
	});
	it("keeps unknown provider capabilities separate from known empty efforts", () => {
		expect(staleSavedTuning({ models: [{ id: "custom" }] }, "custom", "high", "codex")).toBeUndefined();
		expect(staleSavedTuning({ models: [{ id: "custom", efforts: [] }] }, "custom", "high", "codex")).toEqual({ model: false, effort: true });
		expect(staleSavedTuning({ ...catalog, customModelEntry: "direct" }, "my-gateway-model", "high", "claude-code")).toBeUndefined();
	});
	it.each(["sonnet", "sonnet[1m]"])("validates %s against the newest effort-capable family model", (alias) => {
		const aliases = { models: [
			{ id: alias, efforts: [] },
			{ id: "claude-sonnet-4-5", efforts: ["low"] },
			{ id: "claude-sonnet-5-5", efforts: ["high"] },
			{ id: "claude-sonnet-6", efforts: [] },
		] };
		expect(staleSavedTuning(aliases, alias, "high", "claude-code")).toBeUndefined();
		expect(staleSavedTuning(aliases, alias, "low", "claude-code")).toEqual({ model: false, effort: true });
	});
	it("resolves alias versions from labels using numeric major and minor ordering", () => {
		const aliases = { models: [
			{ id: "opus" },
			{ id: "provider-old", label: "Claude Opus 4.9", efforts: ["low"] },
			{ id: "provider-new", label: "Claude Opus 4.10 (1M context)", efforts: ["high"] },
		] };
		expect(staleSavedTuning(aliases, "opus", "high", "claude-code")).toBeUndefined();
		expect(staleSavedTuning(aliases, "opus", "low", "claude-code")).toEqual({ model: false, effort: true });
	});

	it("uses alias efforts when supplied and rejects effort when no concrete fallback exists", () => {
		expect(staleSavedTuning({ models: [{ id: "sonnet", efforts: ["low"] }, ...catalog.models] }, "sonnet", "high", "claude-code")).toEqual({ model: false, effort: true });
		expect(staleSavedTuning({ models: [{ id: "haiku", efforts: [] }, { id: "claude-haiku-4-5" }] }, "haiku", "high", "claude-code")).toEqual({ model: false, effort: true });
	});

	it("never resets against a catalog that cannot answer", () => {
		expect(staleSavedTuning({ ...catalog, stale: true }, "claude-gone-4", "", "claude-code")).toBeUndefined();
		expect(staleSavedTuning({ ...catalog, refreshState: "refreshing" }, "claude-gone-4", "", "claude-code")).toBeUndefined();
		expect(staleSavedTuning({ models: [] }, "claude-gone-4", "", "claude-code")).toBeUndefined();
		expect(staleSavedTuning(undefined, "claude-gone-4", "", "claude-code")).toBeUndefined();
	});
});
