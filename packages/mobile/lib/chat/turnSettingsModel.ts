import type { ApprovalMode, ChatConfigChoice, ChatConfigOption, ChatModel, ConversationSnapshot, TurnSettings } from "./types";
import { can } from "./types";
import { effortLabel } from "../modelEffort";
import { approvalChoiceCopy, approvalCopy, approvalOrder } from "./approvalCopy";
import { displayConfigOptions, executionChoiceValues, isFastOption, isModeOption, optionIsEnabled, partitionConfigOptions, toggleValue } from "../../../shared/chat/config-options";
import { agentModelDisplayLabel, isDefaultPlaceholderLabel } from "../../../shared/models/agent-model-choices";

export type TurnSettingChoice = {
	value: string;
	label: string;
	description?: string;
	selected: boolean;
	/** The AO approval mode a permissions choice applies, when it maps to one. */
	permissionMode?: ApprovalMode;
};

export type TurnSettingRow = {
	id: string;
	label: string;
	description?: string;
	value: string;
	kind: "select" | "boolean";
	enabled?: boolean;
	/**
	 * How the provider categorised this control. Derived once, from the option
	 * itself — the summary used to re-guess it from the label and missed a Mode
	 * option called "Mode", so it showed AO's approvalMode while the sheet was
	 * editing the provider's.
	 */
	providerKind?: ProviderTurnControlKind;
	/**
	 * The choice the agent runs with when none is picked. Picking it clears the
	 * pick rather than saving it, so the conversation follows the agent's
	 * default again, even after that default changes.
	 */
	defaultValue?: string;
	/**
	 * For an on/off a provider sends as a select (Fast mode as On/Off): the
	 * choice each state sends.
	 */
	toggle?: { on?: string; off?: string };
	/**
	 * On AO's own model row, the models that have a Fast mode: picking one of
	 * them keeps Fast mode on, as the desktop does, and any other clears it.
	 */
	fastModels?: string[];
	choices: TurnSettingChoice[];
	target:
		| { kind: "settings"; key: keyof TurnSettings }
		| { kind: "option"; optionId: string };
};

/**
 * The value of a setting nobody has named: the provider did not report it and
 * the user has not picked one. Shown instead of guessing (#5834).
 */
export const NOT_REPORTED = "Not reported";

/** Labels for a provider's `default` choice when nothing says what it resolves to (desktop's wording, #5849). */
const FOLLOW_AGENT = {
	model: "Use agent model",
	effort: "Use agent effort",
	other: "Use agent setting",
} as const;

/**
 * AO's own approval modes, by name, in desktop's words for the agent (see
 * approvalCopy). Codex's default is full access: AO runs it with approval
 * "never" and sandbox "danger-full-access" (codexappserver
 * `approvalSettings`), so it is not the agent's own permissions there.
 */
export function approvalLabel(mode: ApprovalMode, harness: string): string {
	return approvalChoiceCopy(harness, mode)?.label ?? mode;
}

/** A mode by AO's generic name, for a provider's own choice that maps to it. */
function approvalModeLabel(mode: ApprovalMode): string {
	return approvalCopy(undefined)[mode]?.label ?? mode;
}

/** A native catalog's effort level by name, worded as desktop words it; "default" names no level. */
export function effortChoiceLabel(effort: string): string {
	return isDefaultValue(effort) ? FOLLOW_AGENT.effort : effortLabel(effort);
}

/** "Use agent model" and the like, including "Use agent model (Opus 5.5)": a choice that follows the agent. */
export function followsAgentLabel(label: string): boolean {
	return Object.values(FOLLOW_AGENT).some((follow) => label === follow || label.startsWith(`${follow} (`));
}

export type ProviderTurnControlKind = "fast" | "model" | "effort" | "permissions" | "other";

/**
 * The options as they arrive, with a permission mode that names nothing named
 * by the mode it applies (see `namePermissionChoice`). Everything else stays as
 * the agent sent it: the rows read the model, effort and on/off settings as the
 * desktop does (packages/shared/chat/config-options), and reading them twice
 * would fold or rename them twice.
 */
export function resolveDefaultChoices(options: ChatConfigOption[]): ChatConfigOption[] {
	return options.map((option) => {
		if (option.type !== "select" || !isPermissionKind(option)) return option;
		const choices = option.choices.map(namePermissionChoice);
		return choices.every((choice, index) => choice === option.choices[index]) ? option : { ...option, choices };
	});
}

/** A provider's permission control: its mode option, or one named for permissions or approvals. */
function isPermissionKind(option: ChatConfigOption): boolean {
	const id = option.id.toLowerCase();
	return isModeOption(option) || id.includes("permission") || id.includes("approval");
}

/**
 * A permission choice whose name says nothing is named by the mode the daemon
 * maps it to, because picking it applies that mode: a "Default" mapped to
 * bypass-permissions reads "Bypass permissions", not "Default" (review on #6071). An
 * unmapped one reads "Use agent permissions". "Default approvals" is what
 * daemons before #5849 call OpenCode's default tier, and a phone can be paired
 * with one. The wire value is kept.
 */
function namePermissionChoice(choice: ChatConfigChoice): ChatConfigChoice {
	const placeholder = isDefaultPlaceholderLabel(choice.name) || /^(?:default approvals|use agent permissions)$/i.test(choice.name.trim());
	if (!placeholder) return choice;
	const name = approvalModeLabel(choice.permissionMode ?? "default");
	return name === choice.name ? choice : { ...choice, name };
}

/**
 * A provider option's current value, by the name of its choice. A value no
 * choice explains is shown as sent; "default" or no value at all reads "Not
 * reported" rather than "Default" (#5834).
 */
export function providerChoiceLabel(option: ChatConfigOption): string {
	if (option.type === "boolean") return option.currentBoolean ? "On" : "Off";
	const selected = option.choices.find((choice) => choice.value === option.currentValue);
	if (selected) return selected.name;
	return sentValue(option.currentValue) ?? NOT_REPORTED;
}

/**
 * The native model row's value: `model` by name (the catalog entry picked, else
 * the one the catalog marks as the provider's default). Without one, the
 * setting as sent, except "default", which reads "Not reported". Display only:
 * the setting itself is sent unchanged.
 */
export function nativeModelLabel(model: ChatModel | undefined, setting: string | undefined): string {
	const sent = sentValue(setting);
	return model?.displayName ?? (sent ? readableModelId(sent) : undefined) ?? NOT_REPORTED;
}

/**
 * A Claude model id as its name, for when the catalog that names it is not
 * there (a stopped session reports none): "claude-opus-5-5" reads "Opus 5.5",
 * and a dated snapshot drops its date. Any other id is shown as sent.
 */
export function readableModelId(id: string): string {
	const claude = /^claude-([a-z]+)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$/.exec(id);
	if (!claude) return id;
	const [, family, major, minor] = claude;
	return `${capitalize(family)} ${minor ? `${major}.${minor}` : major}`;
}

/** A value as sent, or undefined when there is none or it is "default", which names nothing. */
function sentValue(value: string | undefined): string | undefined {
	return value && !isDefaultValue(value) ? value : undefined;
}

/**
 * A provider `mode` option replaces the Approvals row only when it offers
 * approval choices. OpenCode advertises build/plan there — execution modes —
 * and mistaking them for approvals leaves the session with no way to bypass.
 */
function isPermissionModeOption(option: ChatConfigOption): boolean {
	if (option.category !== "mode" && option.id !== "mode") return false;
	return option.choices.some((choice) =>
		![choice.value, choice.name].some((text) => /^(?:plan|agent|build)(?:[\s_-]mode)?$/i.test(text.trim())));
}

/**
 * Whether the provider's own controls stand in for AO's. A catalog it reported
 * counts as well as the capability: a hibernated chat's snapshot reports no
 * capabilities, and the catalog remembered from an earlier visit would otherwise
 * show beside AO's Approvals row until the wake lands.
 */
export function usesProviderControls(snapshot: ConversationSnapshot, options: readonly ChatConfigOption[]): boolean {
	return can(snapshot, "config_options") || options.length > 0;
}

/**
 * The composer's settings rows for a chat, read as the desktop reads them
 * (packages/shared/chat/config-options): an agent's catalog named and folded
 * for display, then split into its model, effort, on/off settings (Fast mode
 * among them, however the agent sends it), permission modes and the rest. An
 * ACP `agent` option is the agent's own internals and never a row.
 */
export function turnSettingsRows(snapshot: ConversationSnapshot, models: ChatModel[], options: ChatConfigOption[]): TurnSettingRow[] {
	const harness = snapshot.harness;
	// "Opus 5.5", not "Claude Opus 5.5": the agent says the maker already.
	const catalog = models.map((model) => ({ ...model, displayName: agentModelDisplayLabel(harness, model.displayName) }));
	const shown = displayConfigOptions(options, harness, catalog, (mode) => approvalModeLabel(mode as ApprovalMode));
	const grouped = partitionConfigOptions(shown, harness !== "agy");
	// Claude Code's catalog lists "default" as a model; it is no model to pick.
	const nativeModels = harness === "claude-code" ? catalog.filter((model) => model.id !== "default") : catalog;
	const selectedModel = nativeModels.find((model) => model.id === snapshot.settings.model) ?? nativeModels.find((model) => model.default);
	const providerMode = options.some(isPermissionModeOption);
	const providerControls = usesProviderControls(snapshot, options);
	const rows: TurnSettingRow[] = [];

	// AO's own model menu, where the agent offers none of its own.
	if (!grouped.model.length && nativeModels.length) {
		rows.push({
			id: "model",
			label: "Model",
			value: nativeModelLabel(selectedModel, snapshot.settings.model),
			kind: "select",
			// No "Provider default" hint: with nothing picked, the provider's model
			// is the selected row already (#5834).
			choices: nativeModels.map((model) => ({
				value: model.id,
				label: model.displayName,
				description: model.description || undefined,
				selected: model.id === selectedModel?.id,
			})),
			fastModels: nativeModels.filter((model) => model.supportsFast).map((model) => model.id),
			target: { kind: "settings", key: "model" },
		});
		// A "default" level names none; the model's own default is the level shown.
		const efforts = selectedModel?.efforts?.filter((value) => !isDefaultValue(value)) ?? [];
		if (efforts.length) {
			const picked = sentValue(snapshot.settings.reasoningEffort);
			const effort = picked ?? selectedModel?.defaultEffort;
			rows.push({
				id: "effort",
				label: "Effort",
				value: effort ? effortChoiceLabel(effort) : NOT_REPORTED,
				kind: "select",
				defaultValue: selectedModel?.defaultEffort,
				choices: efforts.map((value) => ({ value, label: effortChoiceLabel(value), selected: value === effort })),
				target: { kind: "settings", key: "reasoningEffort" },
			});
		}
		// Fast mode, where the model has one: desktop's toggle beside the model.
		// A saved model the catalog no longer lists has no Fast mode to offer, so
		// the catalog's default is asked only when nothing is saved, as on desktop.
		const fastModel = snapshot.settings.model
			? nativeModels.find((model) => model.id === snapshot.settings.model)
			: nativeModels.find((model) => model.default);
		if (fastModel?.supportsFast) {
			const on = Boolean(snapshot.settings.fastMode);
			rows.push({
				id: "fast",
				label: "Fast mode",
				value: on ? "On" : "Off",
				kind: "boolean",
				enabled: on,
				choices: [],
				target: { kind: "settings", key: "fastMode" },
			});
		}
	}

	if (!providerControls || !providerMode) {
		const approval = snapshot.settings.approvalMode ?? "default";
		rows.push({
			id: "approvals",
			label: "Approvals",
			value: approvalLabel(approval, snapshot.harness),
			kind: "select",
			choices: approvalOrder(snapshot.harness, approval).map((mode) => ({
				value: mode,
				label: approvalLabel(mode, snapshot.harness),
				description: approvalChoiceCopy(snapshot.harness, mode).description,
				selected: mode === approval,
				permissionMode: mode,
			})),
			target: { kind: "settings", key: "approvalMode" },
		});
	}

	// Nothing picked: the provider's model is the agent's own, whatever it
	// reports running, so the choice that follows the agent is the one ticked.
	const followsAgentModel = !sentValue(snapshot.settings.model);
	const [model, ...otherModels] = grouped.model;
	const [effort, ...otherEfforts] = grouped.effort;
	if (model) rows.push(providerRow(model, "model", followsAgentModel));
	if (effort) rows.push(providerEffortRow(effort));
	for (const toggle of grouped.toggles) rows.push(providerToggleRow(toggle));
	// The permission modes as the agent names them (the picker reads its own
	// copy); a mode option offering only execution modes (OpenCode's build and
	// plan) is a setting, and AO's Approvals row stays.
	const modeOption = options.find((option) => isModeOption(option) && isPermissionModeOption(option));
	if (modeOption) rows.push(providerRow(permissionModes(harness === "agy" ? shown.find((option) => option.id === modeOption.id) ?? modeOption : modeOption), "permissions", false));
	if (grouped.executionMode && !modeOption) rows.push(providerRow(grouped.executionMode, "other", false));
	// What is left: a second model or effort, and anything else the agent offers.
	for (const option of [...otherModels, ...otherEfforts, ...grouped.extra]) rows.push(providerRow(option, "other", false));
	return rows.sort((a, b) => controlPriority(a) - controlPriority(b));
}

/**
 * The summary's parts, for a control that shows the approval mode apart from
 * the model. Effort is the setting people change most after the model, and it
 * was the one this line never mentioned. A value that names nothing is "":
 * saying it spends the row's width on nothing (#5834).
 */
export function turnSettingsParts(snapshot: ConversationSnapshot, models: ChatModel[], options: ChatConfigOption[]): { model: string; effort: string; approval: string } {
	const rows = turnSettingsRows(snapshot, models, options);
	const effort = rows.find(isEffortRow);
	const permissions = rows.find(isPermissionRow);
	return {
		model: modelLabel(rows, snapshot, models),
		effort: named(effort ? effort.value : effortLabel(snapshot.settings.reasoningEffort ?? "")),
		approval: named(permissions?.value ?? approvalLabel(snapshot.settings.approvalMode ?? "default", snapshot.harness)),
	};
}

function modelLabel(rows: TurnSettingRow[], snapshot: ConversationSnapshot, models: ChatModel[]): string {
	// Names the model that answered when the provider swapped in another, as the
	// desktop's picker does. A reroute recorded against an earlier pick no longer applies.
	const reroute = snapshot.modelReroute;
	if (reroute?.toModel && (!reroute.fromModel || reroute.fromModel === snapshot.settings.model)) {
		return models.find((model) => model.id === reroute.toModel)?.displayName ?? readableModelId(reroute.toModel);
	}
	const model = rows.find(isModelRow);
	return named(model ? model.value : readableModelId(snapshot.settings.model ?? ""));
}

/** The value, or "" when it names nothing: unreported, a bare "default", or "Use agent …". */
function named(value: string): string {
	return value === NOT_REPORTED || isDefaultValue(value) || followsAgentLabel(value) ? "" : value;
}

function isDefaultValue(value: string): boolean {
	return value.trim().toLowerCase() === "default";
}

function isModelRow(row: TurnSettingRow): boolean {
	if (row.target.kind === "settings") return row.target.key === "model";
	return row.providerKind === "model";
}

function isEffortRow(row: TurnSettingRow): boolean {
	if (row.target.kind === "settings") return row.target.key === "reasoningEffort";
	return row.providerKind === "effort";
}

function isPermissionRow(row: TurnSettingRow): boolean {
	if (row.target.kind === "settings") return row.target.key === "approvalMode";
	return row.providerKind === "permissions";
}

/**
 * A provider option as a row. `followsAgentModel`: no model was picked in AO
 * (the daemon saves every pick), so a model row ticks the agent's own choice
 * even while the provider reports the model that resolves to. Claude Code
 * reports "opus" for a session nobody pointed at Opus when its own settings
 * name it. A choice folded into another (one model under two ids) ticks the
 * one that stands for it.
 */
function providerRow(option: ChatConfigOption, kind: ProviderTurnControlKind, followsAgentModel: boolean): TurnSettingRow {
	const follows = followsAgentModel && kind === "model" && option.choices.some((choice) => choice.value === "default");
	const current = option.currentValue;
	const stands = (choice: ChatConfigChoice) => choice.value === current || Boolean(current && aliasesOf(choice).includes(current));
	const selected = option.choices.find(stands);
	return {
		id: `option:${option.id}`,
		label: option.name,
		description: option.description,
		value: selected?.name ?? providerChoiceLabel(option),
		kind: option.type,
		enabled: Boolean(option.currentBoolean),
		choices: option.choices.map((choice) => ({
			value: choice.value,
			label: choice.name,
			description: choice.description,
			selected: follows ? choice.value === "default" : choice === selected,
			permissionMode: choice.permissionMode,
		})),
		target: { kind: "option", optionId: option.id },
		providerKind: kind,
	};
}

/**
 * The permission picker's choices: the modes the daemon maps to one of AO's
 * permission modes. Execution modes (Claude's Plan Mode, OpenCode's build and
 * plan) and modes AO cannot name (Claude's Don't Ask) are left out. An agent
 * that maps none of its modes keeps its own, less the execution modes; a mode
 * the chat is in stays, so the picker never shows none selected.
 */
function permissionModes(option: ChatConfigOption): ChatConfigOption {
	const execution = executionChoiceValues(option.choices);
	const mapped = option.choices.some((choice) => choice.permissionMode);
	const choices = option.choices.filter((choice) => choice.value === option.currentValue
		|| (!execution.has(choice.value) && (!mapped || Boolean(choice.permissionMode))));
	return choices.length === option.choices.length ? option : { ...option, choices };
}

/** The other ids a choice stands for, when one model was listed under two. */
function aliasesOf(choice: ChatConfigChoice): string[] {
	return (choice as ChatConfigChoice & { aliases?: string[] }).aliases ?? [];
}

/**
 * The agent's effort levels. A "default" level names none and is left out, as
 * on desktop; a level the agent leaves unnamed, or names only by its id
 * capitalized (Claude Code's "Xhigh"), is named as AO names it ("Extra high").
 */
function providerEffortRow(option: ChatConfigOption): TurnSettingRow {
	const current = sentValue(option.currentValue);
	const choices = option.choices.filter((choice) => !isDefaultValue(choice.value));
	const levelName = (choice: ChatConfigChoice | undefined, value: string) =>
		choice?.name && choice.name.toLowerCase() !== value.toLowerCase() ? choice.name : effortChoiceLabel(value);
	return {
		id: `option:${option.id}`,
		label: option.name,
		description: option.description,
		value: current ? levelName(choices.find((choice) => choice.value === current), current) : NOT_REPORTED,
		kind: "select",
		choices: choices.map((choice) => ({
			value: choice.value,
			label: levelName(choice, choice.value),
			description: choice.description,
			selected: choice.value === current,
		})),
		target: { kind: "option", optionId: option.id },
		providerKind: "effort",
	};
}

/**
 * An on/off the agent offers: a boolean option, or a select whose choices read
 * as on and off (Fast mode as On/Off, Cursor's thinking). Fast mode is told
 * apart from the rest, so the effort slider carries it as its bolt.
 */
function providerToggleRow(option: ChatConfigOption): TurnSettingRow {
	const on = optionIsEnabled(option);
	const sends = (enabled: boolean) => {
		const value = toggleValue(option, enabled);
		return value && "value" in value ? value.value : undefined;
	};
	return {
		id: `option:${option.id}`,
		label: option.name,
		description: option.description,
		value: on ? "On" : "Off",
		kind: "boolean",
		enabled: on,
		...(option.type === "select" ? { toggle: { on: sends(true), off: sends(false) } } : {}),
		choices: [],
		target: { kind: "option", optionId: option.id },
		providerKind: isFastOption(option) ? "fast" : "other",
	};
}

function controlPriority(row: TurnSettingRow): number {
	if (row.target.kind === "option") {
		return { fast: 0, model: 1, effort: 2, permissions: 3, other: 10 }[row.providerKind ?? "other"];
	}
	const key = row.id;
	if (key === "fast") return 0;
	if (key === "model") return 1;
	if (key === "effort") return 2;
	if (key === "approvals") return 4;
	return 10;
}

/** What a turn-settings row adjusts, for the glyph the menus draw beside it. */
export type TurnSettingTopic = "model" | "effort" | "fast" | "permissions" | "setting";

/**
 * The row's topic, or none for an agent picker: providers class an `agent`
 * option with models, but no glyph says "agent" without saying "model".
 */
export function turnSettingTopic(row: TurnSettingRow): TurnSettingTopic | undefined {
	// A provider's row says what it is: the catalog was read once, as desktop reads it.
	if (row.target.kind === "option" && row.providerKind) {
		return row.providerKind === "other" ? "setting" : row.providerKind;
	}
	const id = row.id.toLowerCase();
	if (id.includes("agent")) return undefined;
	if (row.providerKind === "model" || id.includes("model")) return "model";
	if (row.providerKind === "permissions" || id.includes("mode") || id.includes("approval")) return "permissions";
	if (id.includes("effort") || id.includes("thought") || id.includes("reason")) return "effort";
	if (id.includes("fast")) return "fast";
	return "setting";
}

/** What picking in a row saves: a provider option by its id, or AO's own settings. */
export type TurnSettingWrite =
	| { kind: "option"; id: string; value: { value: string } | { enabled: boolean } }
	| { kind: "settings"; settings: TurnSettings };

/** The write for a row's choice: a select's value, or a boolean row's new state. */
export function turnSettingWrite(settings: TurnSettings, row: TurnSettingRow, choice: string | boolean): TurnSettingWrite {
	if (row.target.kind === "option") {
		// An on/off the agent sends as a select: the choice that state sends.
		const sent = typeof choice === "boolean" && row.toggle ? (choice ? row.toggle.on : row.toggle.off) : undefined;
		if (sent !== undefined) return { kind: "option", id: row.target.optionId, value: { value: sent } };
		return { kind: "option", id: row.target.optionId, value: typeof choice === "boolean" ? { enabled: choice } : { value: choice } };
	}
	// An on/off setting of AO's own (fast mode): off clears it rather than saving false.
	if (typeof choice === "boolean" && row.target.kind === "settings") return { kind: "settings", settings: { ...settings, [row.target.key]: choice || undefined } };
	return { kind: "settings", settings: applyTurnSettingChoice(settings, row, String(choice)) };
}

function applyTurnSettingChoice(settings: TurnSettings, row: TurnSettingRow, value: string): TurnSettings {
	if (row.target.kind !== "settings") return settings;
	// A new model starts on its own effort. Fast mode stays on only where the
	// new model has one too, as on desktop; elsewhere it would be sent to a
	// model that refuses it.
	if (row.target.key === "model") {
		const fastMode = row.fastModels?.includes(value) ? settings.fastMode : undefined;
		return { ...settings, model: value, reasoningEffort: undefined, fastMode };
	}
	if (row.defaultValue !== undefined && value === row.defaultValue) return { ...settings, [row.target.key]: undefined };
	return { ...settings, [row.target.key]: value } as TurnSettings;
}

/** A model menu shorter than this is listed whole: there is nothing to tuck away. */
const SPLIT_MODEL_MENU_OVER = 6;

/**
 * A model menu's choices in two parts: the newest of each family first, in the
 * provider's order, and the rest, older versions, to open one level down.
 * Providers list their newest model of a family before its older ones (Claude:
 * "Opus 5.5" before "Opus 4.8"), so the first of each family by the name's
 * first word is its newest. The running model stays on top wherever it is
 * listed. A short menu, or one with no older versions, is left whole.
 */
export function splitModelChoices(choices: readonly TurnSettingChoice[]): { latest: TurnSettingChoice[]; older: TurnSettingChoice[] } {
	if (choices.length <= SPLIT_MODEL_MENU_OVER) return { latest: [...choices], older: [] };
	const families = new Set<string>();
	const latest: TurnSettingChoice[] = [];
	const older: TurnSettingChoice[] = [];
	for (const choice of choices) {
		const family = choice.label.trim().split(/\s+/)[0].toLowerCase();
		if (!families.has(family) || choice.selected) latest.push(choice);
		else older.push(choice);
		families.add(family);
	}
	return older.length ? { latest, older } : { latest: [...choices], older: [] };
}

function capitalize(value: string): string {
	return value ? value[0].toUpperCase() + value.slice(1) : value;
}
