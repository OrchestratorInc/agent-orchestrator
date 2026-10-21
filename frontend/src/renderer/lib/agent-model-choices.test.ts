import { describe, expect, it } from "vitest";
import { claudeChoiceModel, filterDisabledModels, foldClaudeAliasDefault, splitClaudeModels } from "./agent-model-choices";

type TestModel = { id: string; label: string; isDefault?: boolean };
const model = (label: string, id = label.toLowerCase().replace(/[ .]/g, "-"), extra: { isDefault?: boolean } = {}): TestModel => ({
	id,
	label,
	...extra,
});

describe("splitClaudeModels", () => {
	it("leads with the newest model of each family in Fable, Opus, Sonnet, Haiku order", () => {
		const { current, other } = splitClaudeModels(
			["Opus 4.9", "Haiku 4.5", "Opus 4.10", "Sonnet 5", "Opus 5.5", "Fable 5.1", "Fable 5"].map((label) => model(label)),
		);
		expect(current.map((item) => item.label)).toEqual(["Fable 5.1", "Opus 5.5", "Sonnet 5", "Haiku 4.5"]);
		expect(other.map((item) => item.label)).toEqual(["Fable 5", "Opus 4.10", "Opus 4.9"]);
	});

	it("reads versions from provider IDs, ignoring snapshot dates and suffixes", () => {
		const ids = [
			"us.anthropic.claude-opus-4-5-v1:0",
			"claude-opus-4-8-20260101",
			"us.anthropic.claude-opus-5-5-v1:0",
		];
		const { current, other } = splitClaudeModels(ids.map((id) => model(id, id)));
		expect(current.map((item) => item.id)).toEqual(["us.anthropic.claude-opus-5-5-v1:0"]);
		expect(other.map((item) => item.id)).toEqual(["claude-opus-4-8-20260101", "us.anthropic.claude-opus-4-5-v1:0"]);
	});

	it("keeps models without a family version in the current list", () => {
		const { current, other } = splitClaudeModels([model("Opus (1M context)", "opus[1m]"), model("Opus 5.5"), model("My gateway model", "gw")]);
		expect(current.map((item) => item.label)).toEqual(["Opus 5.5", "Opus (1M context)", "My gateway model"]);
		expect(other).toEqual([]);
	});

	// Fallback aliases now carry the version discovery resolved, so a context
	// variant of the newest Opus must stay a current choice, not an older one.
	it("keeps a versioned context variant alongside its family's newest model", () => {
		const aliases = [model("Sonnet 5.5", "sonnet"), model("Fable 5.1", "fable"), model("Opus 5.5", "opus"), model("Haiku 5.5", "haiku"), model("Opus 5.5 (1M context)", "opus[1m]")];
		const { current, other } = splitClaudeModels(aliases);
		expect(current.map((item) => item.id)).toEqual(["fable", "opus", "opus[1m]", "sonnet", "haiku"]);
		expect(other).toEqual([]);
	});
});

describe("foldClaudeAliasDefault", () => {
	it("marks the newest model of the configured alias family as the default instead of listing the alias", () => {
		const alias = model("Sonnet", "sonnet", { isDefault: true });
		const folded = foldClaudeAliasDefault([model("Sonnet 4.6"), model("Sonnet 5.5"), model("Opus 5.5"), alias]);
		expect(folded.map((item) => item.id)).toEqual(["sonnet-4-6", "sonnet-5-5", "opus-5-5"]);
		expect(folded.find((item) => item.isDefault)?.label).toBe("Sonnet 5.5");
		const noSibling = [alias, model("Opus 5.5")];
		expect(foldClaudeAliasDefault(noSibling)).toBe(noSibling);
	});

	it("never folds a configured alias into a context variant", () => {
		const models = [model("Opus 5.5", "opus", { isDefault: true }), model("Opus 5.5 (1M context)", "opus[1m]"), model("Sonnet 5.5", "sonnet")];
		expect(foldClaudeAliasDefault(models)).toBe(models);
	});
});

describe("filterDisabledModels", () => {
	const catalog = [{ id: "a" }, { id: "b" }, { id: "c" }];

	it("offers every model when nothing is switched off", () => {
		expect(filterDisabledModels(catalog, undefined)).toEqual({ models: catalog, narrowed: false });
		expect(filterDisabledModels(catalog, []).models).toBe(catalog);
		expect(filterDisabledModels(catalog, ["gone"])).toEqual({ models: catalog, narrowed: false });
	});

	it("leaves out switched-off models but keeps the ones already in use, in catalog order", () => {
		expect(filterDisabledModels(catalog, ["b"])).toEqual({ models: [{ id: "a" }, { id: "c" }], narrowed: true });
		expect(filterDisabledModels(catalog, ["b", "c"], ["b", undefined]).models.map((item) => item.id)).toEqual(["a", "b"]);
	});

	it("falls back to the full catalog when every model is switched off", () => {
		expect(filterDisabledModels(catalog, ["a", "b", "c"])).toEqual({ models: catalog, narrowed: false });
	});

	it("matches entries through modelID", () => {
		const aliases = [{ id: "opus" }, { id: "sonnet" }];
		expect(filterDisabledModels(aliases, ["claude-opus-5-5"], [], (item) => `claude-${item.id}-5-5`).models).toEqual([{ id: "sonnet" }]);
	});
});

describe("claudeChoiceModel", () => {
	const catalog = [model("Claude Opus 5.5", "claude-opus-5-5"), model("Claude Opus 4.9", "claude-opus-4-9"), model("Claude Sonnet 5.5", "claude-sonnet-5-5")];

	it("maps a family alias to its newest model and an id to itself", () => {
		const choiceModel = claudeChoiceModel(catalog)!;
		expect(choiceModel("opus")?.id).toBe("claude-opus-5-5");
		expect(choiceModel("sonnet[1m]")).toBeUndefined();
		expect(choiceModel("claude-opus-4-9")?.id).toBe("claude-opus-4-9");
		expect(choiceModel("default")).toBeUndefined();
		expect(claudeChoiceModel([])).toBeUndefined();
	});
});

describe("Claude context variant selection", () => {
	it("keeps each exact alias on its own Settings switch", () => {
		const catalog = [model("Opus 5.5", "opus"), model("Opus 5.5 (1M context)", "opus[1m]"), model("Sonnet 5.5", "sonnet")];
		const resolve = claudeChoiceModel(catalog)!;
		const offered = (disabled: string) => filterDisabledModels(catalog, [disabled], [], (item) => resolve(item.id)?.id ?? item.id).models.map((item) => item.id);
		expect(resolve("OPUS[1M]")?.id).toBe("opus[1m]");
		expect(offered("opus")).toEqual(["opus[1m]", "sonnet"]);
		expect(offered("opus[1m]")).toEqual(["opus", "sonnet"]);
	});

	it("resolves family aliases only to the same context variant", () => {
		const catalog = [
			model("Claude Opus 5.5 (1M context)", "claude-opus-5-5[1m]"),
			model("Claude Opus 4.9 (1M context)", "claude-opus-4-9[1m]"),
			model("Claude Opus 5.5", "claude-opus-5-5"),
			model("Claude Sonnet 5.5 (1M context)", "claude-sonnet-5-5-long"),
		];
		const resolve = claudeChoiceModel(catalog)!;
		expect(resolve("opus")?.id).toBe("claude-opus-5-5");
		expect(resolve("opus[1m]")?.id).toBe("claude-opus-5-5[1m]");
		expect(resolve("sonnet[1m]")?.id).toBe("claude-sonnet-5-5-long");
		expect(resolve("sonnet")).toBeUndefined();
		expect(resolve("opus[2m]")).toBeUndefined();
		expect(resolve("claude-opus-4-9[1m]")?.id).toBe("claude-opus-4-9[1m]");
	});
});
