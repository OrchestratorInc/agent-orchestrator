import { describe, expect, it, vi } from "vitest";
import type { ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";
import { nativeModelLabel, NOT_REPORTED, providerChoiceLabel, resolveDefaultChoices, splitModelChoices,
	turnSettingsParts, turnSettingsRows, turnSettingTopic, turnSettingWrite, readableModelId } from "./turnSettingsModel";

// Fast mode is off in the app for now (fastMode.ts); its behaviour is still tested.
vi.mock("./fastMode", () => ({ FAST_MODE_SHOWN: true }));

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
	// Desktop's Fast mode toggle, for a model that has a fast mode.
	it("offers fast mode as an on/off for a model that has one, and none for one that does not", () => {
		const models = [
			{ id: "opus", displayName: "Opus", default: true, supportsFast: true },
			{ id: "sonnet", displayName: "Sonnet", default: false },
		] as ChatModel[];
		const on = snapshot({ capabilities: [], settings: { model: "opus", fastMode: true } });
		const fast = turnSettingsRows(on, models, []).find((row) => row.id === "fast")!;
		expect(fast).toMatchObject({ kind: "boolean", enabled: true, label: "Fast mode" });
		expect(turnSettingWrite(on.settings, fast, false)).toEqual({ kind: "settings", settings: { model: "opus", fastMode: undefined } });
		expect(turnSettingWrite({ model: "opus" }, fast, true)).toEqual({ kind: "settings", settings: { model: "opus", fastMode: true } });
		expect(turnSettingsRows(snapshot({ capabilities: [], settings: { model: "sonnet" } }), models, []).some((row) => row.id === "fast")).toBe(false);
		// A new model starts without the old one's fast mode.
		const model = turnSettingsRows(on, models, []).find((row) => row.id === "model")!;
		expect(turnSettingWrite(on.settings, model, "sonnet")).toEqual({ kind: "settings", settings: { model: "sonnet", reasoningEffort: undefined, fastMode: undefined } });
	});

	it("identifies the native controls Android promotes above generic provider options", () => {
		const controls: ChatConfigOption[] = [
			{ id: "fast", name: "Fast mode", type: "boolean", currentBoolean: true, choices: [] },
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "gpt", choices: [{ value: "gpt", name: "GPT" }] },
			{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "high", choices: [{ value: "high", name: "High" }] },
			{ id: "permission_mode", name: "Permissions", category: "mode", type: "select", currentValue: "ask", choices: [{ value: "ask", name: "Ask" }] },
		];

		expect(turnSettingsRows(snapshot(), [], controls).map((row) => turnSettingTopic(row))).toEqual(["fast", "model", "effort", "permissions"]);
	});

	// Claude Code sends Fast mode as a select (AO does not ask for ACP booleans),
	// and Cursor sends its thinking and fast as "true"/"false" selects. Each is one
	// on/off, which sends the choice that state stands for.
	it("treats a provider's binary select as one direct on/off control, and sends its choice", () => {
		const fastMode: ChatConfigOption = {
			id: "fast",
			name: "Fast mode",
			category: "model_config",
			type: "select",
			currentValue: "off",
			choices: [
				{ value: "on", name: "On" },
				{ value: "off", name: "Off" },
			],
		};
		const thinking: ChatConfigOption = { id: "thinking", name: "Thinking", type: "select", currentValue: "true", choices: [{ value: "true", name: "On" }, { value: "false", name: "Off" }] };
		const rows = turnSettingsRows(snapshot(), [], [fastMode, thinking]);
		const fast = rows.find((row) => row.id === "option:fast")!;
		expect(fast).toMatchObject({ kind: "boolean", enabled: false, value: "Off" });
		expect(turnSettingTopic(fast)).toBe("fast");
		expect(turnSettingWrite({}, fast, true)).toEqual({ kind: "option", id: "fast", value: { value: "on" } });
		const think = rows.find((row) => row.id === "option:thinking")!;
		expect(think).toMatchObject({ kind: "boolean", enabled: true });
		expect(turnSettingTopic(think)).toBe("setting");
		expect(turnSettingWrite({}, think, false)).toEqual({ kind: "option", id: "thinking", value: { value: "false" } });
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
		expect(rows.find((row) => row.label === "Approvals")?.value).toBe("Bypass permissions");
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
		)).toBe("Opus · Auto-approve");
	});

	it("names the model that answered when the provider rerouted the pick", () => {
		const models: ChatModel[] = [{ id: "opus", displayName: "Opus", default: true }, { id: "claude-sonnet-5-5", displayName: "Sonnet 5.5", default: false }];
		const modelReroute = { fromModel: "opus", toModel: "claude-sonnet-5-5", at: "" };
		expect(turnSettingsModelLabel(snapshot({ harness: "claude-code", capabilities: [], settings: { model: "opus" }, modelReroute }), models, [])).toBe("Sonnet 5.5");
		// Picking another model leaves the old reroute behind.
		models.push({ id: "haiku", displayName: "Haiku", default: false });
		expect(turnSettingsModelLabel(snapshot({ harness: "claude-code", capabilities: [], settings: { model: "haiku" }, modelReroute }), models, [])).toBe("Haiku");
	});

	it("uses a provider permission control in the turn-settings summary", () => {
		const options: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "sonnet", choices: [{ value: "sonnet", name: "Sonnet" }] },
			{ id: "permission_mode", name: "Permissions", category: "mode", type: "select", currentValue: "acceptEdits", choices: [{ value: "acceptEdits", name: "Accept edits" }, { value: "plan", name: "Plan only" }] },
		];
		expect(turnSettingsSummary(snapshot(), [], options)).toBe("Sonnet · Accept edits");
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

	// An ACP `agent` option is the agent's own internals (Claude's main-thread
	// persona), not a model and not AO's agent switch: as on desktop, no row.
	it("draws a glyph for what each row adjusts, and leaves out an agent's own agent option", () => {
		expect(rows().map((row) => [row.id, turnSettingTopic(row)])).toEqual([
			["option:fast", "fast"],
			["option:model", "model"],
			["option:effort", "effort"],
			["approvals", "permissions"],
			["option:sandbox", "setting"],
		]);
	});

	it("saves a provider choice by option and AO's own by settings", () => {
		const [fast, model] = rows();
		expect(turnSettingWrite({}, model, "o3")).toEqual({ kind: "option", id: "model", value: { value: "o3" } });
		expect(turnSettingWrite({}, fast, true)).toEqual({ kind: "option", id: "fast", value: { enabled: true } });
		const approvals = rows().find((row) => row.id === "approvals")!;
		expect(turnSettingWrite({ model: "gpt" }, approvals, "auto")).toEqual({ kind: "settings", settings: { model: "gpt", approvalMode: "auto" } });
		// The daemon replaces the whole set: what the phone does not show rides along.
		expect(turnSettingWrite({ model: "gpt", planMode: true, autoCompactWindow: "1m" }, approvals, "auto")).toEqual({ kind: "settings", settings: { model: "gpt", planMode: true, autoCompactWindow: "1m", approvalMode: "auto" } });
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
	// A row's choices as the menus and wheels list them: [value, label].
	const rowChoices = (chat: ConversationSnapshot, options: ChatConfigOption[], id: string) =>
		turnSettingsRows(chat, [], options).find((row) => row.id === `option:${id}`)?.choices.map((choice) => [choice.value, choice.label]);

	it("names the model a fresh Claude chat runs on and keeps its wire value", () => {
		const options = claude({ model: "default", effort: "default" });
		expect(rowChoices(claudeChat, options, "model")?.slice(0, 3)).toEqual([
			["default", "Opus 5.5"],
			["claude-fable-5-1", "Fable 5.1"],
			["sonnet", "Sonnet 5.5"],
		]);
		expect(rowChoices(claudeChat, options, "model")?.some(([value]) => value === "opus")).toBe(false);
		// A "default" level names none: the agent's levels alone, as on desktop.
		expect(rowChoices(claudeChat, options, "effort")?.map(([value]) => value)).toEqual(["low", "medium", "high", "xhigh", "max"]);
	});

	it("says nothing is Default on a fresh Claude chat, and the summary names only what is known", () => {
		const options = claude({ model: "default", effort: "default" });
		for (const row of turnSettingsRows(claudeChat, [], options)) {
			expect(row.value).not.toMatch(/\bdefault\b/i);
			for (const choice of row.choices) expect(choice.label).not.toMatch(/\bdefault\b/i);
		}
		const model = turnSettingsRows(claudeChat, [], options).find((row) => row.id === "option:model");
		expect(model?.choices[0]).toMatchObject({ value: "default", label: "Opus 5.5", selected: true });
		expect(turnSettingsSummary(claudeChat, [], options)).toBe("Opus 5.5 · Auto");
		expect(turnSettingsModelLabel(claudeChat, [], options)).toBe("Opus 5.5");
	});

	// The menu never lists a model twice. Picked explicitly, the model is its own
	// row, ticked, and the agent's own choice, which names it, goes.
	it("lists the agent's model once when it is picked explicitly", () => {
		const options = claude();
		const picked = snapshot({ harness: "claude-code", settings: { model: "opus" } });
		expect(rowChoices(picked, options, "model")?.filter(([, name]) => name === "Opus 5.5")).toEqual([["opus", "Opus 5.5"]]);
		const model = turnSettingsRows(picked, [], options).find((row) => row.id === "option:model");
		expect(model?.choices.filter((choice) => choice.selected).map((choice) => choice.value)).toEqual(["opus"]);
		expect(turnSettingsSummary(claudeChat, [], options)).toBe("Opus 5.5 · Xhigh · Auto");
	});

	// Claude Code reports "opus" for a session nobody pointed at Opus when its
	// own settings name it: the menu ticks the model running, by its name.
	it("ticks the agent's model when nothing was picked, whatever the provider reports", () => {
		const options = claude();
		const model = turnSettingsRows(claudeChat, [], options).find((row) => row.id === "option:model");
		expect(model?.choices.filter((choice) => choice.selected).map((choice) => choice.label)).toEqual(["Opus 5.5"]);
		expect(model?.value).toBe("Opus 5.5");
		expect(turnSettingsModelLabel(claudeChat, [], options)).toBe("Opus 5.5");
	});

	// AO hands Claude Code its catalog's full ids on top of the aliases Claude
	// lists by itself, so the newest Opus arrives twice as "Opus 5.5" (review
	// on #7419). It is one row, whichever runs, ticked while either does.
	describe("with a model listed by alias and by id", () => {
		const twice = (current: string): ChatConfigOption[] => claude({ model: current }).map((option) => option.id === "model"
			? { ...option, choices: [...option.choices, { value: "claude-opus-5-5", name: "Opus 5.5", description: "Best for everyday, complex tasks" }] }
			: option);
		const opusRows = (current: string) => turnSettingsRows(snapshot({ harness: "claude-code", settings: { model: current } }), [], twice(current))
			.find((row) => row.id === "option:model")?.choices.filter((choice) => choice.label === "Opus 5.5");

		it("folds both into one row, whichever runs", () => {
			for (const current of ["opus", "claude-opus-5-5", "default", "sonnet"]) expect(opusRows(current)).toHaveLength(1);
			expect(opusRows("claude-opus-5-5")?.[0]).toMatchObject({ value: "claude-opus-5-5", selected: true });
			expect(opusRows("sonnet")?.[0]).toMatchObject({ value: "default", selected: false });
		});
	});

	// Same as desktop: with another model picked, the one Opus row is the
	// provider's own, so choosing it follows the provider.
	it("folds the named model into the default choice while another model is picked", () => {
		const options = claude({ model: "sonnet" });
		const picked = snapshot({ harness: "claude-code", settings: { model: "sonnet" } });
		expect(rowChoices(picked, options, "model")?.filter(([, name]) => name === "Opus 5.5")).toEqual([["default", "Opus 5.5"]]);
		expect(turnSettingsModelLabel(picked, [], options)).toBe("Sonnet 5.5");
	});

	// Another Claude Code build names the newest model by its family: the agent's
	// own choice takes that name, as on desktop, and lists it once.
	it("names the agent's own choice by the model it stands for when that goes by its family name", () => {
		const rows = [
			["default", "Default (recommended)", "Opus"],
			["opus", "Opus", "Opus 5.5 · Best for everyday, complex tasks · ~2× usage vs Sonnet"],
			["sonnet", "Sonnet", "Sonnet 5 · Efficient for routine tasks"],
			["claude-opus-5", "Opus 5", "Newer version available · select Opus for Opus 5.5"],
		];
		const options: ChatConfigOption[] = [{
			id: "model", name: "Model", category: "model", type: "select", currentValue: "default",
			choices: rows.map(([value, name, description]) => ({ value, name, description })),
		}];
		expect(rowChoices(claudeChat, options, "model")).toEqual([["default", "Opus"], ["sonnet", "Sonnet"], ["claude-opus-5", "Opus 5"]]);
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
			["yolo", "Bypass permissions"],
			["edits", "Accept edits"],
			["ao-auto", "Auto-approve"],
			["trust", "Bypass permissions"],
			["bypassPermissions", "Bypass Permissions"],
		]);
		const [mode] = options;
		expect(mode.currentValue).toBe("yolo");
		expect(mode.choices[1]).toMatchObject({ description: "Skips every prompt", permissionMode: "bypass-permissions" });
		// The row and the summary print the selected choice's name.
		expect(providerChoiceLabel(mode)).toBe("Bypass permissions");
		expect(turnSettingsSummary(claudeChat, [], options)).toContain("Bypass permissions");
		expect(resolveDefaultChoices(options)[0]).toBe(mode);
	});

	it("leaves out an agent's own agent option, which is no model", () => {
		const options: ChatConfigOption[] = [...claude(), { id: "agent", name: "Agent", description: "Main-thread agent persona", type: "select", currentValue: "default", choices: [{ value: "default", name: "Default", description: "Standard Claude Code agent" }, { value: "review", name: "Review" }] }];
		expect(turnSettingsRows(claudeChat, [], options).some((row) => row.id === "option:agent")).toBe(false);
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
		expect(turnSettingsSummary(snapshot({ harness: "claude-code", settings: { approvalMode: "auto" } }), [], [effort("default")])).toBe("Auto-approve");
	});

	it("names Codex's default approval mode as the full access it is", () => {
		const codex = turnSettingsRows(snapshot({ harness: "codex", capabilities: [], settings: {} }), [], []);
		expect(codex.find((row) => row.id === "approvals")?.value).toBe("Full access");
		const other = turnSettingsRows(snapshot({ harness: "gemini", capabilities: [], settings: {} }), [], []);
		expect(other.find((row) => row.id === "approvals")?.choices.map((choice) => choice.label)).toEqual([
			"Accept edits", "Auto-approve", "Bypass permissions", "Use agent permissions",
		]);
	});

	// Desktop's names, per agent (TurnSettingsBar's APPROVAL_COPY and its
	// variants), each described in one short line.
	it("names each mode as desktop does, per agent, with a line on what it does", () => {
		const approvals = (harness: string) => turnSettingsRows(snapshot({ harness, capabilities: [], settings: {} }), [], [])
			.find((row) => row.id === "approvals")?.choices.map((choice) => [choice.value, choice.label, choice.description]);
		// "Use agent permissions" hands the choice back, so it comes last.
		expect(approvals("claude-code")).toEqual([
			["accept-edits", "Accept edits", "Edits files without asking"],
			["auto", "Auto-approve", "Asks only about risky actions"],
			["bypass-permissions", "Bypass permissions", "Never asks before acting"],
			["default", "Use agent permissions", "Follows the agent's own settings"],
		]);
		expect(approvals("codex")).toEqual([
			["default", "Full access", "Edits and runs commands freely"],
			["accept-edits", "Ask for approval", "Asks before edits and commands"],
			["auto", "Approve for me", "Asks only about unsafe actions"],
			["bypass-permissions", "Bypass permissions", "Never asks before acting"],
		]);
		// Antigravity has no mode that never asks.
		expect(approvals("agy")).toEqual([
			["default", "Ask for approval", "Asks before acting"],
			["accept-edits", "Accept edits", "Edits files without asking"],
			["auto", "Auto-approve", "Asks only about risky actions"],
		]);
		expect(approvals("hermes")?.map(([value]) => value)).toEqual(["accept-edits", "auto", "bypass-permissions", "default"]);
		expect(approvals("hermes")?.at(-1)?.[2]).toBe("Asks before file edits only");
	});

	// Antigravity is not offered bypass-permissions, but a chat saved with it
	// still runs in it: the picker must show it as the mode in force.
	it("keeps a saved mode the agent is not offered, selected, rather than showing none", () => {
		const approvals = turnSettingsRows(snapshot({ harness: "agy", capabilities: [], settings: { approvalMode: "bypass-permissions" } }), [], []).find((row) => row.id === "approvals");
		// Named generically: Antigravity's copy calls it "Auto-approve", as `auto` is.
		expect(approvals?.value).toBe("Bypass permissions");
		expect(approvals?.choices.map((choice) => [choice.value, choice.selected])).toEqual([
			["default", false], ["accept-edits", false], ["auto", false], ["bypass-permissions", true],
		]);
		expect(approvals?.choices.at(-1)).toMatchObject({ label: "Bypass permissions", description: "Never asks before acting", permissionMode: "bypass-permissions" });
		const labels = approvals?.choices.map((choice) => choice.label) ?? [];
		expect(new Set(labels).size).toBe(labels.length);
		// A mode the agent offers adds nothing.
		const offered = turnSettingsRows(snapshot({ harness: "agy", capabilities: [], settings: { approvalMode: "auto" } }), [], []).find((row) => row.id === "approvals");
		expect(offered?.choices.map((choice) => choice.value)).toEqual(["default", "accept-edits", "auto"]);
	});

	// A chat that never set a mode runs in the agent's default; the picker
	// must show that as the mode in force and offer the way back to it.
	it("shows the agent's own permissions as in force for a chat that never set a mode", () => {
		const approvals = turnSettingsRows(snapshot({ harness: "claude-code", capabilities: [], settings: {} }), [], []).find((row) => row.id === "approvals");
		expect(approvals?.choices.filter((choice) => choice.selected).map((choice) => choice.value)).toEqual(["default"]);
		const set = turnSettingsRows(snapshot({ harness: "claude-code", capabilities: [], settings: { approvalMode: "auto" } }), [], []).find((row) => row.id === "approvals");
		expect(set?.choices.map((choice) => choice.value)).toContain("default");
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

	// As on desktop: a native "default" level names none, so it is no choice, and
	// with none picked the model's own default level is the one shown.
	it("lists a native model's levels without a default level", () => {
		const models: ChatModel[] = [{ id: "a", displayName: "Alpha", default: true, efforts: ["default", "high"] }];
		const effort = turnSettingsRows(snapshot({ capabilities: [], settings: { reasoningEffort: "default" } }), models, []).find((row) => row.id === "effort");
		expect(effort?.value).toBe(NOT_REPORTED);
		expect(effort?.choices.map((choice) => choice.label)).toEqual(["High"]);
		const withDefault: ChatModel[] = [{ ...models[0], defaultEffort: "high" }];
		expect(turnSettingsRows(snapshot({ capabilities: [], settings: {} }), withDefault, []).find((row) => row.id === "effort")?.value).toBe("High");
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

// Each agent's catalog as the daemon passes it on (backend chatdriver tests and
// the bundled ACP agents), read as the desktop reads it.
describe("each agent's settings", () => {
	const rowsFor = (harness: string, options: ChatConfigOption[], models: ChatModel[] = [], settings: ConversationSnapshot["settings"] = {}) =>
		turnSettingsRows(snapshot({ harness, settings, capabilities: options.length ? ["config_options"] : [] }), models, options);
	const topics = (rows: ReturnType<typeof rowsFor>) => rows.map((row) => [row.label, turnSettingTopic(row)]);

	it("Codex: AO's own model, effort and Fast mode, with no options of the agent's", () => {
		const models: ChatModel[] = [
			{ id: "gpt-6.2-sol", displayName: "GPT-6.2-Sol", default: true, efforts: ["low", "medium", "high"], defaultEffort: "medium", supportsFast: true },
			{ id: "gpt-6.2-astra", displayName: "GPT-6.2-Astra", default: false, efforts: ["low", "high"] },
		];
		const rows = rowsFor("codex", [], models);
		expect(topics(rows)).toEqual([["Fast mode", "fast"], ["Model", "model"], ["Effort", "effort"], ["Approvals", "permissions"]]);
		expect(rows.find((row) => row.id === "effort")?.value).toBe("Medium");
	});

	it("Claude Code: the model by version, levels without Default, Fast as an on/off, and no agent persona row", () => {
		const options: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "default", choices: [
				{ value: "default", name: "Default (recommended)", description: "Opus 5.5" },
				{ value: "opus", name: "Opus 5.5", description: "For complex work" },
				{ value: "haiku", name: "Haiku 4.5" },
			] },
			{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "default", choices: [{ value: "default", name: "Default" }, { value: "high", name: "High" }, { value: "xhigh", name: "Xhigh" }] },
			{ id: "fast", name: "Fast mode", category: "model_config", type: "select", currentValue: "off", choices: [{ value: "on", name: "On" }, { value: "off", name: "Off" }] },
			{ id: "agent", name: "Agent", description: "Main-thread agent persona", type: "select", currentValue: "default", choices: [{ value: "default", name: "Default" }, { value: "reviewer", name: "reviewer" }] },
			{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "default", choices: [{ value: "default", name: "Manual", permissionMode: "default" }, { value: "plan", name: "Plan Mode" }, { value: "bypassPermissions", name: "Bypass Permissions", permissionMode: "bypass-permissions" }] },
		];
		const rows = rowsFor("claude-code", options);
		expect(topics(rows)).toEqual([["Fast mode", "fast"], ["Model", "model"], ["Effort", "effort"], ["Mode", "permissions"]]);
		expect(rows.find((row) => row.id === "option:model")?.choices.map((choice) => choice.label)).toEqual(["Opus 5.5", "Haiku 4.5"]);
		expect(rows.find((row) => row.id === "option:effort")?.choices.map((choice) => choice.label)).toEqual(["High", "Xhigh"]);
	});

	it("Pi: provider-prefixed models under their provider, and its Thinking levels as the effort", () => {
		const options: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "anthropic/claude-sonnet-4", choices: [
				{ value: "anthropic/claude-sonnet-4", name: "anthropic/claude-sonnet-4" },
				{ value: "openai/gpt-5.3-codex", name: "openai/gpt-5.3-codex" },
			] },
			{ id: "thought_level", name: "Thinking", category: "thought_level", type: "select", currentValue: "low", choices: [{ value: "minimal", name: "minimal" }, { value: "low", name: "low" }, { value: "high", name: "high" }] },
		];
		const rows = rowsFor("pi", options);
		expect(topics(rows)).toEqual([["Model", "model"], ["Thinking", "effort"], ["Approvals", "permissions"]]);
		expect(rows.find((row) => row.id === "option:model")?.choices.map((choice) => choice.label)).toEqual(["Claude Sonnet 4", "GPT-5.3 Codex"]);
	});

	it("OpenCode: provider routes stay apart, and build/plan beside AO's own permission choices stay one permission row", () => {
		const options: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "opencode/grok-code", choices: [
				{ value: "opencode/grok-code", name: "Grok Code" },
				{ value: "opencode-go/grok-code", name: "Grok Code" },
			] },
			{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "default", choices: [{ value: "default", name: "Default" }] },
			{ id: "mode", name: "Mode", category: "mode", type: "select", currentValue: "ao-default", choices: [
				{ value: "build", name: "build" },
				{ value: "plan", name: "plan" },
				{ value: "ao-default", name: "Use agent permissions", permissionMode: "default" },
				{ value: "ao-bypass", name: "Bypass permissions", permissionMode: "bypass-permissions" },
			] },
		];
		const rows = rowsFor("opencode", options);
		// A Default-only effort names no level: no effort row.
		expect(topics(rows)).toEqual([["Model", "model"], ["Mode", "permissions"]]);
		expect(rows.find((row) => row.id === "option:model")?.choices).toHaveLength(2);
	});

	it("OpenCode v2: a mode option of only build and plan is a setting, and AO's Approvals row stays", () => {
		const options: ChatConfigOption[] = [
			{ id: "mode", name: "Session Mode", category: "mode", type: "select", currentValue: "build", choices: [{ value: "build", name: "Build" }, { value: "plan", name: "Plan" }] },
		];
		expect(topics(rowsFor("opencode-v2", options))).toEqual([["Approvals", "permissions"], ["Session Mode", "setting"]]);
	});

	it("Cursor: thinking and fast as on/offs, Fast told apart for the effort slider", () => {
		const options: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "claude-opus-4-8", choices: [{ value: "claude-opus-4-8", name: "Claude Opus 4.8" }, { value: "gpt-6", name: "GPT-6" }] },
			{ id: "effort", name: "Effort", category: "thought_level", type: "select", currentValue: "high", choices: [{ value: "low", name: "Low" }, { value: "high", name: "High" }] },
			{ id: "thinking", name: "Thinking", type: "select", currentValue: "false", choices: [{ value: "true", name: "On" }, { value: "false", name: "Off" }] },
			{ id: "fast", name: "Fast", type: "select", currentValue: "true", choices: [{ value: "true", name: "On" }, { value: "false", name: "Off" }] },
		];
		const rows = rowsFor("cursor", options);
		expect(topics(rows)).toEqual([["Fast", "fast"], ["Model", "model"], ["Effort", "effort"], ["Approvals", "permissions"], ["Thinking", "setting"]]);
		expect(rows.find((row) => row.id === "option:fast")).toMatchObject({ kind: "boolean", enabled: true });
	});

	// The bolt shows when, and only when, the agent has Fast mode: a boolean, an
	// On/Off or true/false select, or a native model that supports it. A select
	// that only mentions "fast" is a setting like any other.
	it("finds Fast mode only where the agent has it", () => {
		const fastModel: ChatConfigOption = { id: "fast_model", name: "Fast model", type: "select", currentValue: "haiku", choices: [{ value: "haiku", name: "Haiku 4.5" }, { value: "sonnet", name: "Sonnet 5.5" }] };
		const rows = rowsFor("droid", [fastModel]);
		expect(rows.some((row) => turnSettingTopic(row) === "fast")).toBe(false);
		expect(rows.find((row) => row.id === "option:fast_model")).toMatchObject({ kind: "select" });
		const onOff: ChatConfigOption = { id: "fast", name: "Fast mode", type: "select", currentValue: "default", choices: [{ value: "on", name: "On" }, { value: "default", name: "Default" }] };
		expect(rowsFor("claude-code", [onOff]).filter((row) => turnSettingTopic(row) === "fast")).toHaveLength(1);
		const native: ChatModel[] = [{ id: "a", displayName: "A", default: true, supportsFast: false }, { id: "b", displayName: "B", default: false, supportsFast: true }];
		expect(rowsFor("codex", [], native).some((row) => turnSettingTopic(row) === "fast")).toBe(false);
		expect(rowsFor("codex", [], native, { model: "b" }).some((row) => turnSettingTopic(row) === "fast")).toBe(true);
		// Claude drops the option for a model without Fast mode: no bolt.
		expect(rowsFor("claude-code", [{ id: "model", name: "Model", category: "model", type: "select", currentValue: "haiku", choices: [{ value: "haiku", name: "Haiku 4.5" }] }]).some((row) => turnSettingTopic(row) === "fast")).toBe(false);
	});

	it("Antigravity: the effort drawn out of its model ids, each model listed once", () => {
		const options: ChatConfigOption[] = [
			{ id: "model", name: "Model", category: "model", type: "select", currentValue: "gemini-3.8-flash-high", choices: [
				{ value: "gemini-3.8-flash-low", name: "Gemini 3.8 Flash (Low)" },
				{ value: "gemini-3.8-flash-high", name: "Gemini 3.8 Flash (High)" },
				{ value: "gemini-3.8-pro-high", name: "Gemini 3.8 Pro (High)" },
			] },
		];
		const rows = rowsFor("agy", options);
		expect(rows.find((row) => row.id === "option:model")?.choices.map((choice) => choice.label)).toEqual(["Gemini 3.8 Flash", "Gemini 3.8 Pro"]);
		expect(rows.find((row) => turnSettingTopic(row) === "effort")?.choices.map((choice) => choice.label)).toEqual(["Low", "High"]);
	});
});
