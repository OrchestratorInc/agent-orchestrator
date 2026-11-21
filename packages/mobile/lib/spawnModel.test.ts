import { describe, expect, it } from "vitest";
import { claudeAliasModel, defaultEffortModel, defaultRowModelToSend, listedModels, menuModelName, menuModels, modelOverride, resolveSpawnAgent, resolveSpawnModel, spawnModelSourceChanged } from "./spawnModel";

describe("spawn agent resolution", () => {
	it("prefers the project's worker agent when it is available", () => {
		expect(resolveSpawnAgent({
			projectWorkerAgent: "codex",
			projectAgent: "claude-code",
			availableAgents: ["claude-code", "codex"],
		})).toBe("codex");
	});

	it("falls back through the project agent and catalog order", () => {
		expect(resolveSpawnAgent({
			projectWorkerAgent: "missing",
			projectAgent: "claude-code",
			availableAgents: ["codex", "claude-code"],
		})).toBe("claude-code");
		expect(resolveSpawnAgent({
			availableAgents: ["codex", "claude-code"],
		})).toBe("codex");
	});
});

describe("spawn model resolution", () => {
	it("prefers the project worker model for its configured worker agent", () => {
		expect(resolveSpawnModel({
			selectedAgent: "codex",
			projectWorkerAgent: "codex",
			projectWorkerModel: "gpt-5",
		})).toBe("gpt-5");
	});

	// The catalog's `isDefault` entry is not a source here. It says what the
	// provider *claims* it will pick, and codex claimed `gpt-6-astra` while a new
	// thread came up on `gpt-5.6-sol` — the sheet named one model and the chat
	// named the other.
	it("ignores another agent's project model", () => {
		expect(resolveSpawnModel({
			selectedAgent: "claude-code",
			projectWorkerAgent: "codex",
			projectWorkerModel: "gpt-5",
		})).toBe("");
	});

	it("leaves automatic selection empty when the project pins nothing", () => {
		expect(resolveSpawnModel({ selectedAgent: "codex" })).toBe("");
		expect(resolveSpawnModel({
			selectedAgent: "codex",
			projectWorkerAgent: "codex",
			projectWorkerModel: "  ",
		})).toBe("");
	});

	// Sending the pick even when it matches the project's own pin is the point:
	// the old rule dropped it, the session stored no model, and the chat then had
	// nothing to resolve and showed whatever the thread was running.
	it("sends a touched value, including one that matches the project's pin", () => {
		expect(modelOverride("opus", true)).toBe("opus");
		expect(modelOverride("sonnet", true)).toBe("sonnet");
		expect(modelOverride("  opus  ", true)).toBe("opus");
	});

	it("sends nothing for a project's pin, which the server applies", () => {
		expect(modelOverride("opus", false)).toBeUndefined();
		expect(modelOverride("opus", false, "sonnet")).toBeUndefined();
	});

	// The default row names the catalog's default ("Opus 5.5 · Default"), and
	// that is the only way the menu offers it: sending nothing let Claude Code
	// start on the account's own default (Sonnet on some), so the chat named
	// another model than New task had. The desktop's New task sends it too.
	it("sends the default row's model when nothing is picked", () => {
		expect(modelOverride("", false, "opus")).toBe("opus");
		expect(modelOverride("__auto__", false, " gpt-6-astra ")).toBe("gpt-6-astra");
		expect(modelOverride("", true, "opus")).toBe("opus");
		expect(modelOverride("__auto__", true, "opus")).toBe("opus");
	});

	it("takes the default row's model from the agent's own loaded catalog", () => {
		const models = [{ id: "opus", label: "Opus 5.5", isDefault: true }, { id: "sonnet", label: "Sonnet 5.5" }];
		expect(defaultRowModelToSend({ catalog: { selectionMode: "catalog", models } })).toBe("opus");
		expect(defaultRowModelToSend({ catalog: undefined })).toBeUndefined();
		expect(defaultRowModelToSend({ catalog: { selectionMode: "catalog", models: [{ id: "sonnet", label: "Sonnet 5.5" }] } })).toBeUndefined();
	});

	// Desktop sends the catalog default only for a model list with nothing
	// pinned (TaskComposer: !catalogUsesModes && !projectModelOrMode). A mode
	// list's default sent as the model replaced a project's pinned mode: an Amp
	// project pinned to "high" ran "medium".
	it("sends no default for a mode list, or over a project's pinned model or mode", () => {
		const modes = { selectionMode: "mode" as const, models: [{ id: "medium", label: "Medium", isDefault: true }, { id: "high", label: "High" }] };
		expect(defaultRowModelToSend({ catalog: modes })).toBeUndefined();
		const models = { selectionMode: "catalog" as const, models: [{ id: "opus", label: "Opus 5.5", isDefault: true }] };
		expect(defaultRowModelToSend({ catalog: models, projectPin: "high" })).toBeUndefined();
		expect(defaultRowModelToSend({ catalog: models, projectPin: "sonnet" })).toBeUndefined();
		expect(defaultRowModelToSend({ catalog: models, projectPin: "  " })).toBe("opus");
	});

	it("sends nothing when the catalog names no concrete default", () => {
		expect(modelOverride("", false)).toBeUndefined();
		expect(modelOverride("", true)).toBeUndefined();
		expect(modelOverride("__auto__", true)).toBeUndefined();
		expect(modelOverride("", false, "default")).toBeUndefined();
		expect(modelOverride("", false, "  ")).toBeUndefined();
	});

	it("reloads the model catalog only when its project or agent source changes", () => {
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-1", agentId: "codex" },
		)).toBe(false);
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-2", agentId: "codex" },
		)).toBe(true);
		expect(spawnModelSourceChanged(
			{ projectId: "project-1", agentId: "codex" },
			{ projectId: "project-1", agentId: "claude-code" },
		)).toBe(true);
	});
});

describe("menuModels", () => {
	const catalog = Array.from({ length: 20 }, (_, index) => ({ id: `m${index}`, label: `Model ${index}`, isDefault: index === 15 }));

	// A menu builds every row up front; opencode's catalog alone is thousands.
	it("lists the first few models and counts them all", () => {
		const menu = menuModels(catalog, "__auto__", 3);
		expect(menu.models.map((model) => model.id)).toEqual(["m0", "m1", "m2"]);
		expect(menu.total).toBe(20);
	});

	it("keeps the picked model in the list, so the menu can check it, and names the catalog's default", () => {
		expect(menuModels(catalog, "m17", 3).models.map((model) => model.id)).toEqual(["m0", "m1", "m2", "m17"]);
		expect(menuModels(catalog, "m1", 3).models.map((model) => model.id)).toEqual(["m0", "m1", "m2"]);
		expect(menuModels(catalog, "__auto__", 3).defaultLabel).toBe("Model 15");
		expect(menuModels([{ id: "a", label: "A" }], "__auto__").defaultLabel).toBeUndefined();
	});

	// The default row names the catalog's default, so no row repeats it, even
	// under the id's longer name; one pinned by the project stays to be ticked.
	it("leaves the catalog's default and its other names to the default row", () => {
		const claude = [
			{ id: "claude-fable-5-1", label: "Claude Fable 5.1" },
			{ id: "claude-opus-5-5", label: "Claude Opus 5.5" },
			{ id: "opus", label: "Opus 5.5", isDefault: true },
		];
		expect(menuModels(claude, "__auto__").models.map((model) => model.label)).toEqual(["Fable 5.1"]);
		expect(menuModels(claude, "__auto__").defaultLabel).toBe("Opus 5.5");
		expect(menuModels(claude, "claude-opus-5-5").models.map((model) => model.id)).toEqual(["claude-fable-5-1", "claude-opus-5-5"]);
	});

	// New task and chat name and order one model the same way (#7419 review):
	// by family as Claude Code lists them, newest first within one.
	it("names and orders Claude's models as the chat's menu does", () => {
		const alphabetical = [
			{ id: "claude-fable-5", label: "Claude Fable 5" },
			{ id: "claude-fable-5-1", label: "Claude Fable 5.1" },
			{ id: "claude-haiku-4-5", label: "Claude Haiku 4.5" },
			{ id: "claude-opus-4-8", label: "Claude Opus 4.8" },
			{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
		];
		expect(menuModels(alphabetical, "__auto__").models.map((model) => model.label)).toEqual(["Fable 5.1", "Fable 5", "Opus 4.8", "Sonnet 5.5", "Haiku 4.5"]);
		expect(menuModels(alphabetical, "__auto__").older).toEqual([]);
		expect(menuModelName("Claude Opus 5.5")).toBe("Opus 5.5");
		expect(menuModelName("Claude Code")).toBe("Claude Code");
	});

	// The live catalog round 4 saw: chat folds the older versions away, and so does New task.
	it("puts each family's older versions under Older models in a long menu, as chat does", () => {
		const catalog = [
			{ id: "opus", label: "Opus 5.5", isDefault: true },
			{ id: "claude-haiku-5-5", label: "Claude Haiku 5.5" },
			{ id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5" },
			{ id: "claude-fable-5-1", label: "Claude Fable 5.1" },
			{ id: "claude-fable-5", label: "Claude Fable 5" },
			{ id: "claude-opus-5", label: "Claude Opus 5" },
			{ id: "claude-sonnet-5", label: "Claude Sonnet 5" },
			{ id: "claude-opus-4-8", label: "Claude Opus 4.8" },
			{ id: "claude-haiku-4-5", label: "Claude Haiku 4.5" },
		];
		const menu = menuModels(catalog, "__auto__");
		// The default row is Opus's newest, as chat's default choice is, so Opus 5 is an older one.
		expect(menu.models.map((model) => model.label)).toEqual(["Fable 5.1", "Sonnet 5.5", "Haiku 5.5"]);
		expect(menu.older.map((model) => model.label)).toEqual(["Fable 5", "Opus 5", "Opus 4.8", "Sonnet 5", "Haiku 4.5"]);
		// A picked older version stays on top, where the menu shows it ticked.
		expect(menuModels(catalog, "claude-opus-4-8").models.map((model) => model.label)).toEqual(["Fable 5.1", "Opus 4.8", "Sonnet 5.5", "Haiku 5.5"]);
		expect(listedModels(menu)).toBe(8);
	});

	it("leaves other providers' catalogs in their own order and names", () => {
		const codex = [{ id: "gpt-6-astra", label: "GPT-6 Astra" }, { id: "gpt-5.6-sol", label: "GPT-5.6 Sol" }];
		expect(menuModels(codex, "__auto__").models.map((model) => model.label)).toEqual(["GPT-6 Astra", "GPT-5.6 Sol"]);
	});
});

describe("default effort model", () => {
	const models = [
		{ id: "claude-haiku-4-5", label: "Haiku 4.5" },
		{ id: "claude-sonnet-5", label: "Sonnet 5", efforts: ["low", "medium", "high"] },
		{ id: "claude-opus-5", label: "Opus 5", efforts: ["low", "medium", "high", "xhigh", "max"] },
	];

	// Claude Code offers Opus's levels, as desktop opens on Opus.
	it("offers Claude Code's Opus, else the first model that takes effort, else none", () => {
		expect(defaultEffortModel("claude-code", models)?.id).toBe("claude-opus-5");
		expect(defaultEffortModel("codex", models)?.id).toBe("claude-sonnet-5");
		expect(defaultEffortModel("claude-code", [models[0]])).toBeUndefined();
	});
});

describe("claude alias model", () => {
	const models = [
		{ id: "claude-opus-4-8", label: "Claude Opus 4.8", efforts: ["low", "high"] },
		{ id: "claude-opus-5-5", label: "Claude Opus 5.5", efforts: ["low", "medium", "high", "xhigh", "max"] },
		{ id: "claude-opus-5", label: "Claude Opus 5", efforts: ["low", "medium", "high"] },
		{ id: "claude-sonnet-4-5-20250929", label: "Claude Sonnet 4.5" },
		{ id: "claude-sonnet-4-6", label: "Claude Sonnet 4.6", efforts: ["low", "max"] },
		{ id: "opus", label: "Opus 5.5", isDefault: true },
	];

	it("resolves an alias to its family's newest model that takes effort", () => {
		expect(claudeAliasModel(models, "opus")?.id).toBe("claude-opus-5-5");
		expect(claudeAliasModel(models, "opus[1m]")?.id).toBe("claude-opus-5-5");
		expect(claudeAliasModel(models, "sonnet")?.id).toBe("claude-sonnet-4-6");
	});

	it("resolves nothing for a concrete model or an unknown family", () => {
		expect(claudeAliasModel(models, "claude-sonnet-4-5-20250929")).toBeUndefined();
		expect(claudeAliasModel(models, "fable")).toBeUndefined();
	});
});
