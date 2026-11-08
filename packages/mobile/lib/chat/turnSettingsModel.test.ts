import { describe, expect, it } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";
import { nativeModelLabel, NOT_REPORTED, providerChoiceLabel, providerTurnControlKind, resolveDefaultChoices, splitModelChoices,
	turnSettingsParts, turnSettingsRows, turnSettingTopic, turnSettingWrite, readableModelId } from "./turnSettingsModel";

// What the controls row names, all three parts read together: the model's
// capsule, the effort's speedometer and the approval mode's symbol.
const turnSettingsSummary = (...args: Parameters<typeof turnSettingsParts>) => {
	const parts = turnSettingsParts(...args);
	return [parts.model, parts.effort, parts.approval].filter(Boolean).join(" · ");
};
const turnSettingsModelLabel = (...args: Parameters<typeof turnSettingsParts>) => turnSettingsParts(...args).model;

const snapshot = (over: Partial<ConversationSnapshot> = {}): ConversationSnapshot => ({
	conversationId: "c",
	sessionId: "s",
	harness: "codex",
	mode: "chat",
	controller: { state: "ready" },
	latestSequence: 0,
	oldestSequence: 0,
	hasMoreBefore: false,
	turns: [],
	items: [],
	settings: {},
	capabilities: ["config_options"],
	...over,
});

describe("turnSettingsRows", () => {
	it("identifies the native controls Android promotes above generic provider options", () => {
		const controls: ChatConfigOption[] = [
			{ id: "fast", name: "Fast mode", type: "boolean", currentBoolean: true, choices: [] },
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "gpt", choices: [{ value: "gpt", name: "GPT" }] },
			{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "high", choices: [{ value: "high", name: "High" }] },
			{ id: "permission_mode", name: "Permissions", category: "mode", type: "select", currentValue: "ask", choices: [{ value: "ask", name: "Ask" }] },
		];

		expect(controls.map(providerTurnControlKind)).toEqual(["fast", "model", "effort", "permissions"]);
	});

	it("treats a provider's binary Fast mode select as one direct on/off control", () => {
		const fastMode: ChatConfigOption = {
			id: "fast_mode",
			name: "Fast mode",
			type: "select",
			currentValue: "off",
			choices: [
				{ value: "off", name: "Off" },
				{ value: "on", name: "On" },
			],
		};

		expect(providerTurnControlKind(fastMode)).toBe("fast");
	});

	it("orders provider controls as fast mode, model, effort, permissions, then remaining controls", () => {
		const options: ChatConfigOption[] = [
			{ id: "sandbox", name: "Sandbox", type: "select", currentValue: "safe", choices: [{ value: "safe", name: "Safe" }] },
			{ id: "fast", name: "Fast mode", type: "boolean", currentBoolean: true, choices: [] },
			{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "high", choices: [{ value: "high", name: "High" }] },
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "gpt", choices: [{ value: "gpt", name: "GPT" }] },
			{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "agent", choices: [{ value: "agent", name: "Agent" }, { value: "bypass", name: "Bypass permissions" }] },
		];

		expect(turnSettingsRows(snapshot(), [], options).map((row) => row.label)).toEqual([
			"Fast mode",
			"Model",
			"Effort",
			"Mode",
			"Sandbox",
		]);
	});

	it("keeps its own approvals row when the provider's mode catalog is execution modes", () => {
		const options: ChatConfigOption[] = [
			{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "build", choices: [{ value: "build", name: "build" }, { value: "plan", name: "plan" }] },
		];
		const rows = turnSettingsRows(snapshot({ settings: { approvalMode: "bypass-permissions" } }), [], options);
		expect(rows.map((row) => row.label)).toContain("Approvals");
		expect(rows.find((row) => row.label === "Approvals")?.value).toBe("Never ask");
	});

	it("uses a remembered provider catalog alone while the chat's snapshot still reports no capabilities", () => {
		const options: ChatConfigOption[] = [
			{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "auto", choices: [{ value: "auto", name: "Auto" }, { value: "default", name: "Default" }] },
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "opus", choices: [{ value: "opus", name: "Opus 5.5" }] },
		];
		const hibernated = snapshot({ harness: "claude-code", controller: { state: "hibernated" }, capabilities: [], settings: { approvalMode: "auto" } });

		expect(turnSettingsRows(hibernated, [], options).map((row) => row.id)).toEqual(["option:model", "option:mode"]);
		expect(turnSettingsSummary(hibernated, [], options)).toBe("Opus 5.5 · Auto");
	});

	it("builds model, effort, and approvals drill-down rows when provider controls are unavailable", () => {
		const models: ChatModel[] = [{
			id: "opus",
			displayName: "Opus",
			default: true,
			efforts: ["medium", "high"],
			defaultEffort: "medium",
		}];
		const rows = turnSettingsRows(snapshot({ capabilities: [], settings: { model: "opus", reasoningEffort: "high" } }), models, []);

		expect(rows.map((row) => [row.id, row.value, row.kind])).toEqual([
			["model", "Opus", "select"],
			["effort", "High", "select"],
			["approvals", "Full access", "select"],
		]);
		expect(rows[1]?.choices.map((choice) => choice.label)).toEqual(["Medium", "High"]);
	});

	it("summarizes the selected model and permission level without repeating the harness", () => {
		const models: ChatModel[] = [{ id: "opus", displayName: "Opus", default: true }];
		expect(turnSettingsSummary(
			snapshot({ harness: "claude-code", capabilities: [], settings: { model: "opus", approvalMode: "auto" } }),
			models,
			[],
		)).toBe("Opus · Ask when unsure");
	});

	it("uses a provider permission control in the turn-settings summary", () => {
		const options: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "sonnet", choices: [{ value: "sonnet", name: "Sonnet" }] },
			{ id: "permission_mode", name: "Permissions", category: "mode", type: "select", currentValue: "plan", choices: [{ value: "plan", name: "Plan only" }] },
		];
		expect(turnSettingsSummary(snapshot(), [], options)).toBe("Sonnet · Plan only");
	});
});

describe("turn settings menus", () => {
	const options: ChatConfigOption[] = [
		{ id: "agent", name: "Agent", category: "model", type: "select", currentValue: "build", choices: [{ value: "build", name: "Build" }] },
		{ id: "model", name: "Model", category: "model", type: "select", currentValue: "gpt", choices: [{ value: "gpt", name: "GPT" }, { value: "o3", name: "o3" }] },
		{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "high", choices: [{ value: "high", name: "High" }] },
		{ id: "fast", name: "Fast mode", type: "boolean", currentBoolean: false, choices: [] },
		{ id: "sandbox", name: "Sandbox", type: "select", currentValue: "safe", choices: [{ value: "safe", name: "Safe" }] },
	];
	const rows = () => turnSettingsRows(snapshot(), [], options);

	it("draws a glyph for what each row adjusts, and none for an agent picker", () => {
		expect(rows().map((row) => [row.id, turnSettingTopic(row)])).toEqual([
			["option:fast", "fast"],
			["option:agent", undefined],
			["option:model", "model"],
			["option:effort", "effort"],
			["approvals", "permissions"],
			["option:sandbox", "setting"],
		]);
	});

	it("saves a provider choice by option and AO's own by settings", () => {
		const [fast, , model] = rows();
		expect(turnSettingWrite({}, model, "o3")).toEqual({ kind: "option", id: "model", value: { value: "o3" } });
		expect(turnSettingWrite({}, fast, true)).toEqual({ kind: "option", id: "fast", value: { enabled: true } });
		const approvals = rows().find((row) => row.id === "approvals")!;
		expect(turnSettingWrite({ model: "gpt" }, approvals, "auto")).toEqual({ kind: "settings", settings: { model: "gpt", approvalMode: "auto" } });
	});
});

describe("turnSettingsSummary", () => {
	// The four controls a Codex session actually reports, verbatim from the
	// daemon: the Mode option is called "Mode", with nothing in its id or name
	// resembling "permission".
	const providerControls: ChatConfigOption[] = [
		{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "bypassPermissions", choices: [{ value: "bypassPermissions", name: "Never ask" }] },
		{ id: "model", name: "Model", category: "model", type: "select", currentValue: "opus", choices: [{ value: "opus", name: "Opus" }] },
		{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "xhigh", choices: [{ value: "xhigh", name: "Extra high" }] },
		{ id: "fast", name: "Fast mode", category: "model_config", type: "select", currentValue: "off", choices: [{ value: "off", name: "Off" }] },
	];

	// The bug this pins: the summary matched a permission row by looking for
	// "permission" or "approval" in the label, so a provider Mode option never
	// matched and the line fell back to AO's approvalMode — a different value
	// from the one the sheet was editing.
	it("reads the provider's own Mode rather than AO's approvalMode", () => {
		const summary = turnSettingsSummary(
			snapshot({ settings: { model: "opus", reasoningEffort: "xhigh", approvalMode: "default" } }),
			[],
			providerControls,
		);
		expect(summary).toContain("Never ask");
		expect(summary).not.toContain("Default permissions");
	});

	it("names the effort, which the line never used to mention", () => {
		const summary = turnSettingsSummary(snapshot(), [], providerControls);
		expect(summary).toBe("Opus · Extra high · Never ask");
	});

	it("drops an effort of Default rather than spending width on it", () => {
		const models: ChatModel[] = [
			{ id: "opus", displayName: "Opus", default: true, efforts: ["default", "high"], defaultEffort: "default" },
		];
		const summary = turnSettingsSummary(
			snapshot({ capabilities: [], settings: { model: "opus", reasoningEffort: "default" } }),
			models,
			[],
		);
		expect(summary).toBe("Opus · Full access");
	});

	it("still reports a model when the catalogue has not loaded", () => {
		const summary = turnSettingsSummary(snapshot({ capabilities: [], settings: { model: "opus" } }), [], []);
		expect(summary).toBe("opus · Full access");
	});
});

// #5834: no value, choice or summary says a bare "Default".
describe("default labels", () => {
	// GET /api/v1/sessions/{id}/conversation/config-options for a Claude Code chat,
	// verbatim from AO 0.13.1-nightly.202609191635 on 2026-09-30. That session had
	// picked Opus and Xhigh; `claude()` swaps currentValue to model the rest.
	const response: { options: ChatConfigOption[] } = JSON.parse(String.raw`{"options":[{"id":"mode","name":"Mode","description":"Session permission mode","category":"mode","type":"select","currentValue":"auto","choices":[{"permissionMode":"auto","value":"auto","name":"Auto","description":"Use a model classifier to approve/deny permission prompts"},{"permissionMode":"default","value":"default","name":"Manual","description":"Standard behavior, prompts for dangerous operations"},{"permissionMode":"accept-edits","value":"acceptEdits","name":"Accept Edits","description":"Auto-accept file edit operations"},{"value":"plan","name":"Plan Mode","description":"Planning mode, no actual tool execution"},{"value":"dontAsk","name":"Don't Ask","description":"Don't prompt for permissions, deny if not pre-approved"},{"permissionMode":"bypass-permissions","value":"bypassPermissions","name":"Bypass Permissions","description":"Bypass all permission checks"}]},{"id":"model","name":"Model","description":"AI model to use","category":"model","type":"select","currentValue":"opus","choices":[{"value":"default","name":"Default (recommended)","description":"Opus 5.5"},{"value":"opus","name":"Opus 5.5","description":"For complex work and everyday tasks"},{"value":"claude-fable-5-1","name":"Fable 5.1","description":"For your toughest challenges"},{"value":"sonnet","name":"Sonnet 5.5","description":"Most efficient for simpler tasks"},{"value":"haiku","name":"Haiku 4.5","description":"Fastest for quick answers"},{"value":"claude-sonnet-5","name":"Sonnet 5","description":"Efficient for routine tasks"},{"value":"claude-opus-5","name":"Opus 5","description":"Best for everyday, complex tasks"},{"value":"claude-fable-5","name":"Fable 5","description":"Most capable for your hardest and longest-running tasks"},{"value":"claude-opus-4-8","name":"Opus 4.8","description":"Best for everyday, complex tasks"},{"value":"claude-opus-4-7","name":"Opus 4.7","description":"Best for everyday, complex tasks"},{"value":"claude-opus-4-6","name":"Opus 4.6","description":"Best for everyday, complex tasks"},{"value":"claude-sonnet-4-6","name":"Sonnet 4.6","description":"Efficient for routine tasks"}]},{"id":"effort","name":"Effort","description":"Available effort levels for this model","category":"thought_level","type":"select","currentValue":"xhigh","choices":[{"value":"default","name":"Default"},{"value":"low","name":"Low"},{"value":"medium","name":"Medium"},{"value":"high","name":"High"},{"value":"xhigh","name":"Xhigh"},{"value":"max","name":"Max"}]},{"id":"fast","name":"Fast mode","description":"Faster responses on supported models","category":"model_config","type":"select","currentValue":"off","choices":[{"value":"on","name":"On"},{"value":"off","name":"Off"}]}]}`);
	const claude = (current: Record<string, string> = {}): ChatConfigOption[] =>
		response.options.map((option) => option.id in current ? { ...option, currentValue: current[option.id] } : option);
	const choices = (options: ChatConfigOption[], id: string) =>
		options.find((option) => option.id === id)?.choices.map((choice) => [choice.value, choice.name]);
	const claudeChat = snapshot({ harness: "claude-code", settings: {} });

	it("names the model a fresh Claude chat runs on and keeps its wire value", () => {
		const options = resolveDefaultChoices(claude({ model: "default", effort: "default" }));
		expect(choices(options, "model")?.slice(0, 3)).toEqual([
			["default", "Opus 5.5"],
			["claude-fable-5-1", "Fable 5.1"],
			["sonnet", "Sonnet 5.5"],
		]);
		expect(choices(options, "model")?.some(([value]) => value === "opus")).toBe(false);
		expect(choices(options, "effort")?.[0]).toEqual(["default", "Use agent effort"]);
	});

	it("says nothing is Default on a fresh Claude chat, and the summary names only what is known", () => {
		const options = resolveDefaultChoices(claude({ model: "default", effort: "default" }));
		for (const row of turnSettingsRows(claudeChat, [], options)) {
			expect(row.value).not.toMatch(/\bdefault\b/i);
			// The one "Default" is the tag on the model the agent runs by itself.
			for (const choice of row.choices) expect(choice.label.replace(/ · Default$/, "")).not.toMatch(/\bdefault\b/i);
		}
		const model = turnSettingsRows(claudeChat, [], options).find((row) => row.id === "option:model");
		expect(model?.choices[0]).toMatchObject({ value: "default", label: "Opus 5.5 · Default", selected: true });
		expect(turnSettingsSummary(claudeChat, [], options)).toBe("Opus 5.5 · Auto");
		expect(turnSettingsModelLabel(claudeChat, [], options)).toBe("Opus 5.5");
	});

	// The menu never lists a model twice: the agent's own choice stands in for
	// the model it names, and is ticked while that model runs.
	it("lists the agent's model once when it is picked explicitly", () => {
		const options = resolveDefaultChoices(claude());
		expect(choices(options, "model")?.filter(([, name]) => name === "Opus 5.5")).toEqual([["default", "Opus 5.5"]]);
		const picked = snapshot({ harness: "claude-code", settings: { model: "opus" } });
		const model = turnSettingsRows(picked, [], options).find((row) => row.id === "option:model");
		expect(model?.choices.filter((choice) => choice.selected).map((choice) => choice.value)).toEqual(["default"]);
		expect(turnSettingsSummary(claudeChat, [], options)).toBe("Opus 5.5 · Xhigh · Auto");
	});

	// Claude Code reports "opus" for a session nobody pointed at Opus when its
	// own settings name it. Nothing was picked in AO, so the chat follows the
	// agent's model, and the menu says so (review on #7419).
	it("ticks the agent's model when nothing was picked, whatever the provider reports", () => {
		const options = resolveDefaultChoices(claude());
		const model = turnSettingsRows(claudeChat, [], options).find((row) => row.id === "option:model");
		expect(model?.choices.filter((choice) => choice.selected).map((choice) => choice.label)).toEqual(["Opus 5.5 · Default"]);
		expect(model?.value).toBe("Opus 5.5");
		expect(turnSettingsModelLabel(claudeChat, [], options)).toBe("Opus 5.5");
	});

	// AO hands Claude Code its catalog's full ids on top of the aliases Claude
	// lists by itself, so the newest Opus arrives twice as "Opus 5.5" (review
	// on #7419). Both fold into the agent's own choice, which names them.
	describe("with a model listed by alias and by id", () => {
		const twice = (current: string): ChatConfigOption[] => claude({ model: current }).map((option) => option.id === "model"
			? { ...option, choices: [...option.choices, { value: "claude-opus-5-5", name: "Opus 5.5", description: "Best for everyday, complex tasks" }] }
			: option);
		const opusRows = (current: string) => choices(resolveDefaultChoices(twice(current)), "model")?.filter(([, name]) => name.includes("Opus 5.5"));

		it("folds both into the agent's own choice, whichever runs", () => {
			expect(opusRows("opus")).toEqual([["default", "Opus 5.5"]]);
			expect(opusRows("claude-opus-5-5")).toEqual([["default", "Opus 5.5"]]);
			expect(opusRows("default")).toEqual([["default", "Opus 5.5"]]);
			expect(opusRows("sonnet")).toEqual([["default", "Opus 5.5"]]);
		});
	});

	// Same as desktop: with another model picked, the one Opus row is the
	// provider's own, so choosing it follows the provider.
	it("folds the named model into the default choice while another model is picked", () => {
		const options = resolveDefaultChoices(claude({ model: "sonnet" }));
		expect(choices(options, "model")?.filter(([, name]) => name === "Opus 5.5")).toEqual([["default", "Opus 5.5"]]);
		expect(turnSettingsModelLabel(claudeChat, [], options)).toBe("Sonnet 5.5");
	});

	// Another Claude Code build names the newest model by its family and keeps the
	// version in the description: [value, name, description] rows as quoted in #6013.
	it("keeps the version visible when the named model goes by its family name", () => {
		const rows = [
			["default", "Default (recommended)", "Opus"],
			["opus", "Opus", "Opus 5.5 · Best for everyday, complex tasks · ~2× usage vs Sonnet"],
			["sonnet", "Sonnet", "Sonnet 5 · Efficient for routine tasks"],
			["claude-opus-5", "Opus 5", "Newer version available · select Opus for Opus 5.5"],
		];
		const [model] = resolveDefaultChoices([{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "default",
			choices: rows.map(([value, name, description]) => ({ value, name, description })),
		}]);
		expect(model.choices.map((choice) => [choice.value, choice.name, choice.description])).toEqual([
			["default", "Opus", "Opus 5.5 · Best for everyday, complex tasks · ~2× usage vs Sonnet"],
			rows[2],
			rows[3],
		]);
	});

	it("leaves a named permission mode alone and renames the ones that name nothing, dropping none", () => {
		expect(choices(resolveDefaultChoices(claude()), "mode")).toEqual(choices(claude(), "mode"));
		const legacy = resolveDefaultChoices([{
			id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "ao-default",
			choices: [
				{ value: "build", name: "build" },
				// OpenCode's AO default tier as daemons before #5849 label it.
				{ value: "ao-default", name: "Default approvals", permissionMode: "default" },
				// A mode the daemon does not map.
				{ value: "default", name: "Default" },
			],
		}]);
		expect(choices(legacy, "mode")).toEqual([
			["build", "build"],
			["ao-default", "Use agent permissions"],
			["default", "Use agent permissions"],
		]);
	});

	// Reviews on #6071: a placeholder the daemon maps to another permission mode
	// first claimed to follow the agent, then kept "Default" while picking it
	// applied that mode. It now says what it applies.
	it("names a placeholder by the permission mode the daemon maps it to", () => {
		const options = resolveDefaultChoices([{
			id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "yolo",
			choices: [
				{ value: "default", name: "Default", permissionMode: "default" },
				{ value: "yolo", name: "Default", description: "Skips every prompt", permissionMode: "bypass-permissions" },
				{ value: "edits", name: "Default (recommended)", permissionMode: "accept-edits" },
				{ value: "ao-auto", name: "Default approvals", permissionMode: "auto" },
				{ value: "trust", name: "Use agent permissions", permissionMode: "bypass-permissions" },
				{ value: "bypassPermissions", name: "Bypass Permissions", permissionMode: "bypass-permissions" },
			],
		}]);
		expect(choices(options, "mode")).toEqual([
			["default", "Use agent permissions"],
			["yolo", "Never ask"],
			["edits", "Ask outside worktree"],
			["ao-auto", "Ask when unsure"],
			["trust", "Never ask"],
			["bypassPermissions", "Bypass Permissions"],
		]);
		const [mode] = options;
		expect(mode.currentValue).toBe("yolo");
		expect(mode.choices[1]).toMatchObject({ description: "Skips every prompt", permissionMode: "bypass-permissions" });
		// The row and the summary print the selected choice's name.
		expect(providerChoiceLabel(mode)).toBe("Never ask");
		expect(turnSettingsSummary(claudeChat, [], options)).toContain("Never ask");
		expect(resolveDefaultChoices(options)[0]).toBe(mode);
	});

	it("does not call an agent picker a model", () => {
		const options = resolveDefaultChoices([{ id: "agent", name: "Agent", type: "select", currentValue: "default", choices: [{ value: "default", name: "Default" }, { value: "review", name: "Review" }] }]);
		expect(choices(options, "agent")?.[0]).toEqual(["default", "Use agent setting"]);
	});

	it("leaves Fast mode alone, whose Off may be spelled default", () => {
		const fast: ChatConfigOption[] = [{ id: "fast", name: "Fast mode", type: "select", currentValue: "default", choices: [{ value: "on", name: "On" }, { value: "default", name: "Default" }] }];
		expect(resolveDefaultChoices(fast)).toEqual(fast);
	});

	it("gives the same answer when applied twice", () => {
		const cases: Array<Record<string, string>> = [{ model: "default", effort: "default" }, {}, { model: "sonnet" }];
		for (const current of cases) {
			const once = resolveDefaultChoices(claude(current));
			expect(resolveDefaultChoices(once)).toEqual(once);
		}
	});

	it("says a value is not reported rather than printing default or nothing", () => {
		const effort = (currentValue?: string): ChatConfigOption => ({ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue, choices: [{ value: "high", name: "High" }] });
		expect(providerChoiceLabel(effort("default"))).toBe(NOT_REPORTED);
		expect(providerChoiceLabel(effort(undefined))).toBe(NOT_REPORTED);
		expect(providerChoiceLabel(effort("ultra"))).toBe("ultra");
		expect(turnSettingsSummary(snapshot({ harness: "claude-code", settings: { approvalMode: "auto" } }), [], [effort("default")])).toBe("Ask when unsure");
	});

	it("names Codex's default approval mode as the full access it is", () => {
		const codex = turnSettingsRows(snapshot({ harness: "codex", capabilities: [], settings: {} }), [], []);
		expect(codex.find((row) => row.id === "approvals")?.value).toBe("Full access");
		const other = turnSettingsRows(snapshot({ harness: "gemini", capabilities: [], settings: {} }), [], []);
		expect(other.find((row) => row.id === "approvals")?.choices.map((choice) => choice.label)).toEqual([
			"Use agent permissions", "Ask outside worktree", "Ask when unsure", "Never ask",
		]);
	});

	it("shows the provider's model as selected instead of hinting at it", () => {
		const models: ChatModel[] = [
			{ id: "a", displayName: "Alpha", default: false },
			{ id: "b", displayName: "Beta", default: true, description: "" },
		];
		const model = turnSettingsRows(snapshot({ capabilities: [], settings: {} }), models, []).find((row) => row.id === "model");
		expect(model?.value).toBe("Beta");
		expect(model?.choices.map((choice) => [choice.label, choice.description, choice.selected])).toEqual([
			["Alpha", undefined, false],
			["Beta", undefined, true],
		]);
	});

	it("says a native model or effort is not reported, and leaves it out of the summary", () => {
		// Codex's model/list leaves no model marked default when the configured
		// one is not listed (codexappserver ListModels).
		const models: ChatModel[] = [{ id: "a", displayName: "Alpha", default: false, efforts: ["low", "high"] }];
		const s = snapshot({ harness: "codex", capabilities: [], settings: {} });
		const rows = turnSettingsRows(s, models, []);
		expect(rows.find((row) => row.id === "model")?.value).toBe(NOT_REPORTED);
		expect(turnSettingsSummary(s, models, [])).toBe("Full access");
		expect(turnSettingsModelLabel(s, models, [])).toBe("");
		const picked = snapshot({ harness: "codex", capabilities: [], settings: { model: "a" } });
		expect(turnSettingsRows(picked, models, []).find((row) => row.id === "effort")?.value).toBe(NOT_REPORTED);
		expect(turnSettingsSummary(picked, models, [])).toBe("Alpha · Full access");
	});

	// Review on #6071: a native setting of "default" that nothing in the catalog
	// resolves was printed verbatim in the Model row.
	it("does not print a native model setting of default that nothing resolves", () => {
		const unmarked: ChatModel[] = [{ id: "a", displayName: "Alpha", default: false }];
		const s = snapshot({ harness: "codex", capabilities: [], settings: { model: "default" } });
		const row = turnSettingsRows(s, unmarked, []).find((r) => r.id === "model");
		expect(row?.value).toBe(NOT_REPORTED);
		expect(row?.choices.some((choice) => choice.selected)).toBe(false);
		expect(turnSettingsSummary(s, unmarked, [])).toBe("Full access");
		const marked: ChatModel[] = [...unmarked, { id: "b", displayName: "Beta", default: true }];
		expect(turnSettingsRows(s, marked, []).find((r) => r.id === "model")?.value).toBe("Beta");
		expect(nativeModelLabel(undefined, "default")).toBe(NOT_REPORTED);
		expect(nativeModelLabel(undefined, "gpt-custom")).toBe("gpt-custom");
		expect(nativeModelLabel(undefined, undefined)).toBe(NOT_REPORTED);
	});

	it("names a native default effort as following the agent", () => {
		const models: ChatModel[] = [{ id: "a", displayName: "Alpha", default: true, efforts: ["default", "high"] }];
		const effort = turnSettingsRows(snapshot({ capabilities: [], settings: { reasoningEffort: "default" } }), models, []).find((row) => row.id === "effort");
		expect(effort?.value).toBe("Use agent effort");
		expect(effort?.choices.map((choice) => choice.label)).toEqual(["Use agent effort", "High"]);
	});

	it("names the model for the actions sheet only when something names one", () => {
		expect(turnSettingsModelLabel(snapshot({ capabilities: [], settings: { model: "opus" } }), [], [])).toBe("opus");
		expect(turnSettingsModelLabel(snapshot({ capabilities: [], settings: { model: "default" } }), [], [])).toBe("");
		expect(turnSettingsModelLabel(snapshot({ capabilities: [], settings: {} }), [], [])).toBe("");
	});
});

describe("readableModelId", () => {
	// A stopped session reports no catalog, so the control named the model by
	// its raw id ("claude-opus-5-5"); a Claude id reads as the model's name.
	it("names a Claude model id the way its catalog would", () => {
		expect(readableModelId("claude-opus-5-5")).toBe("Opus 5.5");
		expect(readableModelId("claude-sonnet-4-5-20250929")).toBe("Sonnet 4.5");
		expect(readableModelId("claude-haiku-5")).toBe("Haiku 5");
	});

	it("leaves any other id as sent", () => {
		expect(readableModelId("gpt-5.6-sol")).toBe("gpt-5.6-sol");
		expect(readableModelId("claude-3-5-sonnet")).toBe("claude-3-5-sonnet");
	});
});

describe("splitModelChoices", () => {
	const list = (labels: string[], selected?: string) => labels.map((label) => ({ value: label, label, selected: label === selected }));
	const labels = (choices: { label: string }[]) => choices.map((choice) => choice.label);

	it("keeps the newest of each family on top and the rest below", () => {
		const { latest, older } = splitModelChoices(list(["Opus 5.5", "Fable 5.1", "Sonnet 5.5", "Haiku 5.5", "Haiku 4.5", "Sonnet 5", "Opus 4.8"]));
		expect(labels(latest)).toEqual(["Opus 5.5", "Fable 5.1", "Sonnet 5.5", "Haiku 5.5"]);
		expect(labels(older)).toEqual(["Haiku 4.5", "Sonnet 5", "Opus 4.8"]);
	});

	it("keeps the running model on top wherever it is listed", () => {
		const { latest, older } = splitModelChoices(list(["Opus 5.5", "Fable 5.1", "Sonnet 5.5", "Haiku 5.5", "Haiku 4.5", "Sonnet 5", "Opus 4.8"], "Opus 4.8"));
		expect(labels(latest)).toEqual(["Opus 5.5", "Fable 5.1", "Sonnet 5.5", "Haiku 5.5", "Opus 4.8"]);
		expect(labels(older)).toEqual(["Haiku 4.5", "Sonnet 5"]);
	});

	it("leaves a short menu, or one with no older versions, whole", () => {
		expect(splitModelChoices(list(["Opus 5.5", "Opus 4.8", "Sonnet 5"])).older).toEqual([]);
		expect(splitModelChoices(list(["a", "b", "c", "d", "e", "f", "g"])).older).toEqual([]);
	});
});
